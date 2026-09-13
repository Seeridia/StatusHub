#!/usr/bin/env python3
"""Check tracked publication paths and relative Markdown links (no network)."""
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
files = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode().split("\0")
files = {name for name in files if name}
errors = []
private_path = re.compile(
    r"(^|/)(private-docs|private|work|tmp|node_modules)(/|$)|"
    r"(^|/)\.env(?:\.|$)|"
    r"(^|/)(?:implementation-plan|.*technical-research|design-qa|m\d+-runbook)\.md$|"
    r"^docs/(?:internal/|.*(?:plan|research).*\.md$)|"
    r"(^|/)(?:owner-invite\.json|audit\.ndjson)$|\.(?:pem|key|p12|pfx|dump)$"
)

def anchors(text):
    result = set()
    seen = {}
    for heading in re.findall(r"^#{1,6}\s+(.+?)\s*#*\s*$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", heading.lower()).replace(" ", "-")
        count = seen.get(slug, 0)
        seen[slug] = count + 1
        result.add(slug + (f"-{count}" if count else ""))
    return result

for name in sorted(files):
    if name != ".env.example" and private_path.search(name):
        errors.append(f"Local-only path is tracked: {name}")
    if not name.endswith(".md"):
        continue
    path = ROOT / name
    if not path.is_file():
        errors.append(f"Tracked document missing from working tree: {name}")
        continue
    text = path.read_text()
    if re.search(r"/Users/[^/\s]+/|/home/[^/\s]+/|^#{1,6} .*?(?:实施计划|推进计划|技术研究|Design QA|M\d+)", text, re.M):
        errors.append(f"Internal content or personal path in {name}")
    for target in re.findall(r"\[[^\]\n]*\]\(([^\s)]+)\)", text):
        url = urlsplit(target.strip("<>"))
        if url.scheme or url.netloc:
            continue
        linked = (path.parent / unquote(url.path)).resolve() if url.path else path
        if not linked.is_relative_to(ROOT):
            errors.append(f"Link leaves repository: {name}: {target}")
        elif linked.relative_to(ROOT).as_posix() not in files:
            errors.append(f"Link target is not tracked: {name}: {target}")
        elif url.fragment and linked.suffix == ".md" and linked.is_file():
            if unquote(url.fragment) not in anchors(linked.read_text()):
                errors.append(f"Missing heading: {name}: {target}")
if errors:
    print("\n".join(errors), file=sys.stderr)
    sys.exit(1)
print(f"Public documentation check passed ({sum(p.endswith('.md') for p in files)} Markdown files).")
