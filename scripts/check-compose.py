#!/usr/bin/env python3
"""Validate production/development Compose with synthetic configuration."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import re

root = Path(__file__).resolve().parents[1]
source = (root / "compose.yaml").read_text()
assert not re.search(r"^\s*<<\s*:|[&*][A-Za-z_][\w-]*", source, re.M), "Keep production service fields explicit; no YAML aliases or merge keys"
assert "traefik." not in source and "dokploy-network" not in source, "Platform routing must stay outside the generic Compose"
env = os.environ.copy()
env.update(
    STATUSMON_IMAGE="ghcr.io/seeridia/statusmon:sha-test",
    POSTGRES_PASSWORD="a" * 64,
    STATUSMON_CONFIG_KEY="a" * 44,
    STATUSMON_API_KEY="b" * 44,
    STATUSMON_PUBLIC_URL="https://status.example.test",
    STATUSMON_SMTP_ADDRESS="smtp.example.test:587",
    STATUSMON_SMTP_FROM="status@example.test",
    STATUSMON_BIND_ADDRESS="127.0.0.1",
    STATUSMON_HTTP_PORT="8080",
)
result = subprocess.run(
    ["docker", "compose", "--env-file", "/dev/null", "-f", "compose.yaml", "config", "--format", "json"],
    cwd=root, env=env, capture_output=True, text=True, check=True,
)
config = json.loads(result.stdout)
services = config["services"]
assert set(services) == {"api", "worker", "migrate", "postgres", "nats"}
for name, service in services.items():
    assert "build" not in service, name
    assert set(service["networks"]) == {"backend"}, name
    if name != "api":
        assert not service.get("ports"), name
for name in ("api", "worker", "migrate"):
    assert services[name]["image"] == env["STATUSMON_IMAGE"]
for name in ("api", "worker"):
    assert services[name]["depends_on"]["migrate"]["condition"] == "service_completed_successfully"
assert services["api"]["ports"][0]["host_ip"] == "127.0.0.1"
assert services["api"]["ports"][0]["target"] == 8080
subprocess.run(
    ["docker", "compose", "--env-file", "/dev/null", "-f", "deploy/compose.dev.yaml", "config", "--quiet"],
    cwd=root, env=env, check=True,
)
# Model a deployment platform appending a proxy network to api only.
with tempfile.TemporaryDirectory() as directory:
    override = Path(directory) / "proxy.yaml"
    override.write_text("services:\n  api:\n    networks: [backend, proxy]\nnetworks:\n  proxy:\n    external: true\n    name: statusmon-test-proxy\n")
    patched = subprocess.run(
        ["docker", "compose", "--env-file", "/dev/null", "-f", "compose.yaml", "-f", str(override), "config", "--format", "json"],
        cwd=root, env=env, capture_output=True, text=True, check=True,
    )
    patched_services = json.loads(patched.stdout)["services"]
    assert set(patched_services["api"]["networks"]) == {"backend", "proxy"}
    for name in ("postgres", "nats", "worker", "migrate"):
        assert set(patched_services[name]["networks"]) == {"backend"}, name
print("Compose checks passed, including API-only proxy network attachment.")
