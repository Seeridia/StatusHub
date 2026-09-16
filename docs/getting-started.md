# Local installation

[English README](../README.md) · [中文安装指南](operations/getting-started.md)

This guide creates a new local installation. For an existing database, follow the [upgrade procedure](operations/maintenance.md#版本升级). Run commands from the repository root. You need Go 1.25+, Node.js 22.12+, npm, Python 3, Docker Compose v2, and network access to public status pages.

## 1. Create local configuration

Skip this step if `.env` already exists. This script refuses to overwrite it and generates two independent keys:

```bash
python3 - <<'PY'
from pathlib import Path
import base64
import os

text = Path('deploy/local.env.example').read_text()
for key in ('STATUSHUB_CONFIG_KEY', 'STATUSHUB_API_KEY'):
    text = text.replace(key + '=\n', key + '=' + base64.b64encode(os.urandom(32)).decode() + '\n')
text += '\nDATABASE_URL=postgres://statushub:statushub_local_only@127.0.0.1:55432/statushub?sslmode=disable\n'
text += 'NATS_URL=nats://127.0.0.1:54222\n'
fd = os.open('.env', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as output:
    output.write(text)
PY

set -a
. ./.env
set +a
```

Keep these keys for subsequent runs and backups. Go processes do not load `.env` automatically; load it in every terminal that runs a service or admin command. The database password and HTTP URL above are local development settings.

## 2. Start dependencies and initialize an empty database

```bash
make infra-up
make infra-status
```

PostgreSQL and NATS should be healthy; Mailpit should be running. **Only for a new, empty database**, run:

```bash
make migrate-up
make bootstrap-top5
```

The baseline is for fresh installations. The previous identity schema is unsupported; do not apply it to an old database. The bootstrap target registers GitHub, Cloudflare, OpenAI, Anthropic, and AWS public sources; it does not insert simulated incidents.

## 3. Build the console and start the API

```bash
make ui-install
make ui-build
export STATUSHUB_SMTP_ADDRESS=127.0.0.1:51025
export STATUSHUB_SMTP_ALLOW_LOCAL_PLAINTEXT=true
go run ./cmd/statushub-api -allow-local-http
```

Keep this terminal open. The SMTP settings route identity emails into local Mailpit. `-allow-local-http` permits local HTTP; use HTTPS for public deployments.

In a second terminal, load the same configuration and start the worker:

```bash
set -a
. ./.env
set +a
go run ./cmd/statushubd -worker-id local-live
```

In another terminal, check health:

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

The API serves the console; the worker performs collection and notification delivery. Readiness checks database and live-event connectivity, not whether every source has been collected successfully.

## 4. Initialize the first Admin

With the same environment loaded, run:

```bash
go run ./cmd/statushub-admin setup-link
```

Open the returned URL within 30 minutes. Enter your email, a 12–128 character password, workspace name and slug. This atomically creates the first Admin and workspace and signs you in. Setup is available only on an empty instance; generating another link invalidates the previous one. Keep the link private.

Setup does not require SMTP and does not mark your email verified. Use Personal account to verify email later. Admins send invitations from Settings; open local invitation and reset emails in [Mailpit](http://127.0.0.1:58025). There is no default password. Normal login asks only for email and password.

## 5. Use the workspace

Open [the console](http://127.0.0.1:8080/ui/?tenant=local). Inspect source collection health, add a status-page URL, create a Slack or webhook channel, then create a notification rule. Channel tests send a real request to the destination you configure. Initial collection establishes a baseline instead of notifying every historical incident.

For later restarts, reuse the existing `.env`, run `make infra-up`, and restart the API and worker. Do not regenerate keys, recreate the tenant, or rerun all migrations.

## Next steps

- [Console operations (中文)](operations/console-guide.md)
- [Team accounts and SMTP (中文)](operations/team-accounts.md)
- [Configuration and polling (中文)](operations/configuration.md)
- [Maintenance and deployment (中文)](operations/maintenance.md)
- [Development](../CONTRIBUTING.md)

For frontend development, run `make ui-dev` with the API on port 8080. Vite serves port 5173. Production assets are embedded in Go: rebuild the frontend and restart the API after changes.
