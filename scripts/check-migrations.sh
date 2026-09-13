#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
db_user="${POSTGRES_USER:-statusmon}"
migration_db="statusmon_migration_test_$$"

case "$migration_db" in
  statusmon_migration_test_[0-9]*) ;;
  *)
    echo "refusing unsafe migration database name: $migration_db" >&2
    exit 1
    ;;
esac

compose() {
  docker compose -f "$project_dir/deploy/compose.yaml" "$@"
}

cleanup() {
  compose exec -T postgres dropdb --if-exists -U "$db_user" "$migration_db" >/dev/null
}
trap cleanup EXIT

compose exec -T postgres createdb -U "$db_user" "$migration_db"
for migration in "$project_dir"/migrations/*.up.sql; do
  compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$migration_db" \
    < "$migration" >/dev/null
done

up_count="$(compose exec -T postgres psql -U "$db_user" -d "$migration_db" -Atc \
  "SELECT count(*) FROM pg_tables WHERE schemaname='public';")"
if [[ "$up_count" != "42" ]]; then
  echo "expected 42 tables after up migration, got $up_count" >&2
  exit 1
fi

while IFS= read -r migration; do
  compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$migration_db" \
    < "$migration" >/dev/null
done < <(find "$project_dir/migrations" -maxdepth 1 -name '*.down.sql' -print | sort -r)

down_count="$(compose exec -T postgres psql -U "$db_user" -d "$migration_db" -Atc \
  "SELECT count(*) FROM pg_tables WHERE schemaname='public';")"
if [[ "$down_count" != "0" ]]; then
  echo "expected 0 tables after down migration, got $down_count" >&2
  exit 1
fi

echo "migration round trip passed (42 tables up, 0 tables down)"
