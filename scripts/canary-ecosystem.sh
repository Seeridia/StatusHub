#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
statushub_binary="${STATUSHUB_BINARY:-$(mktemp "${TMPDIR:-/tmp}/statushub-m3.XXXXXX")}"
if [[ -z "${STATUSHUB_BINARY:-}" ]]; then
  trap 'rm -f -- "$statushub_binary"' EXIT
fi

cd "$project_dir"
go build -o "$statushub_binary" ./cmd/statushub

canary() {
  local name="$1"
  local target_url="$2"
  local provider="$3"
  shift 3
  echo "[$name]"
  "$statushub_binary" -url "$target_url" -provider "$provider" -operation canary -timeout 30s "$@" |
    tee "/tmp/statushub-${name}-canary.json" |
    jq -e '.healthy == true and (.endpoints | length > 0)'
}

canary instatus https://status.instatus.com instatus
canary betterstack https://status.betterstack.com betterstack
canary statusio https://api.status.io/1.0/status/51f6f2088643809b7200000d status-io
canary cachet https://demo.cachethq.io cachet
canary gatus https://status.twin.sh gatus
canary cstate https://cstate.mnts.lt cstate

if [[ -n "${INCIDENT_IO_WIDGET_URL:-}" ]]; then
  canary incidentio "$INCIDENT_IO_WIDGET_URL" incident-io
else
  echo "[incidentio] skipped: set INCIDENT_IO_WIDGET_URL supplied by the page owner"
fi

if [[ -n "${HTML_RECIPE_URL:-}" && -n "${HTML_RECIPES_FILE:-}" ]]; then
  canary html-recipe "$HTML_RECIPE_URL" html-recipe -html-recipes-file "$HTML_RECIPES_FILE"
elif [[ -n "${HTML_RECIPE_URL:-}" || -n "${HTML_RECIPES_FILE:-}" ]]; then
  echo "[html-recipe] both HTML_RECIPE_URL and HTML_RECIPES_FILE are required" >&2
  exit 1
else
  canary html-recipe https://www.githubstatus.com html-recipe \
    -html-recipes-file "$project_dir/scripts/html-recipes.canary.json"
fi
