#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$(mktemp "${TMPDIR:-/tmp}/statusmon-canary.XXXXXX")"
trap 'rm -f "$binary"' EXIT

go build -o "$binary" "$project_dir/cmd/statusmon"

for target in \
  https://www.githubstatus.com \
  https://www.cloudflarestatus.com \
  https://status.openai.com \
  https://status.anthropic.com \
  https://health.aws.amazon.com/health/status
do
  "$binary" -url "$target" -operation canary -timeout 30s
done
