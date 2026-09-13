#!/usr/bin/env bash
set -euo pipefail

: "${STATUSMON_API_BASE_URL:?set STATUSMON_API_BASE_URL, for example http://127.0.0.1:8080}"
: "${STATUSMON_TENANT:?set STATUSMON_TENANT to a tenant UUID or slug}"
: "${STATUSMON_SERVICE_ACCOUNT_TOKEN:?set STATUSMON_SERVICE_ACCOUNT_TOKEN}"

requests="${STATUSMON_M5_LOAD_REQUESTS:-600}"
concurrency="${STATUSMON_M5_LOAD_CONCURRENCY:-24}"
p95_budget_seconds="${STATUSMON_M5_P95_BUDGET_SECONDS:-0.100}"

case "$requests:$concurrency:$p95_budget_seconds" in
  *[!0-9.:]*) echo "load settings must be numeric" >&2; exit 2 ;;
esac
if (( requests < 30 || concurrency < 1 || concurrency > requests )); then
  echo "requests must be >= 30 and concurrency must be in [1, requests]" >&2
  exit 2
fi

base_url="${STATUSMON_API_BASE_URL%/}/v1/tenants/${STATUSMON_TENANT}"
result_file="$(mktemp "${TMPDIR:-/tmp}/statusmon-m5-results.XXXXXX")"
curl_config="$(mktemp "${TMPDIR:-/tmp}/statusmon-m5-curl.XXXXXX")"
cleanup() { rm -f -- "$result_file" "$curl_config"; }
trap cleanup EXIT
chmod 600 "$curl_config"
printf 'header = "Authorization: Bearer %s"\nheader = "Accept: application/json"\nsilent\nshow-error\nfail\n' \
  "$STATUSMON_SERVICE_ACCOUNT_TOKEN" >"$curl_config"

run_one() {
  local sequence="$1" path
  case $(( sequence % 3 )) in
    0) path="/vendors" ;;
    1) path="/sources?limit=50" ;;
    *) path="/incidents?limit=50" ;;
  esac
  curl --config "$curl_config" --output /dev/null --write-out "%{time_total}\n" "${base_url}${path}"
}
export -f run_one
export base_url curl_config

# Warm connection pools and query plans before recording the distribution.
for sequence in $(seq 1 15); do run_one "$sequence" >/dev/null; done

seq 1 "$requests" | xargs -P "$concurrency" -I '{}' bash -c 'run_one "$1"' _ '{}' >"$result_file"
count="$(wc -l <"$result_file" | tr -d ' ')"
if [[ "$count" != "$requests" ]]; then
  echo "expected $requests timing samples, got $count" >&2
  exit 1
fi

percentile() {
  local percentile="$1" rank
  rank="$(awk -v n="$count" -v p="$percentile" 'BEGIN { print int(n*p + 0.999999) }')"
  sort -n "$result_file" | sed -n "${rank}p"
}

p50="$(percentile 0.50)"
p95="$(percentile 0.95)"
p99="$(percentile 0.99)"
printf 'M5 API load: requests=%s concurrency=%s p50=%.2fms p95=%.2fms p99=%.2fms budget=%.2fms\n' \
  "$requests" "$concurrency" \
  "$(awk -v value="$p50" 'BEGIN { print value*1000 }')" \
  "$(awk -v value="$p95" 'BEGIN { print value*1000 }')" \
  "$(awk -v value="$p99" 'BEGIN { print value*1000 }')" \
  "$(awk -v value="$p95_budget_seconds" 'BEGIN { print value*1000 }')"

if ! awk -v observed="$p95" -v budget="$p95_budget_seconds" 'BEGIN { exit !(observed <= budget) }'; then
  echo "M5 API p95 latency gate failed" >&2
  exit 1
fi
