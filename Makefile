.PHONY: test test-race test-integration test-m4-load test-m4-million test-m5-load fmt fmt-check vet verify infra-up infra-down infra-status migrate-up migrate-down migrate-check bootstrap-github bootstrap-top5 bootstrap-ecosystem canary canary-top5 canary-ecosystem run-once run-api

LOCAL_DATABASE_URL ?= postgres://statushub:statushub_local_only@127.0.0.1:55432/statushub?sslmode=disable
LOCAL_NATS_URL ?= nats://127.0.0.1:54222

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

test-integration:
	TEST_DATABASE_URL='$(LOCAL_DATABASE_URL)' STATUSHUB_TEST_NATS_URL='$(LOCAL_NATS_URL)' \
		go test -race -count=1 ./internal/store/postgres ./internal/bus/jetstream ./internal/pipeline/e2e

test-m4-load:
	STATUSHUB_RUN_M4_LOAD=1 TEST_DATABASE_URL='$(LOCAL_DATABASE_URL)' \
		go test -run TestIntegrationFanoutLoadBaseline -v -count=1 ./internal/pipeline/e2e

test-m4-million:
	STATUSHUB_RUN_M4_MILLION=1 TEST_DATABASE_URL='$(LOCAL_DATABASE_URL)' \
		go test -run TestIntegrationFanoutLoadBaseline -v -count=1 -timeout=10m ./internal/pipeline/e2e

test-m5-load:
	./scripts/m5-api-load.sh

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal -name '*.go' -type f))"

vet:
	go vet ./...

verify: ui-check infra-up fmt-check vet test-race migrate-check test-integration

infra-up:
	docker compose -f deploy/compose.dev.yaml up -d --wait

infra-down:
	docker compose -f deploy/compose.dev.yaml down

infra-status:
	docker compose -f deploy/compose.dev.yaml ps

migrate-up:
	@for migration in migrations/*.up.sql; do \
		docker compose -f deploy/compose.dev.yaml exec -T postgres \
			psql -v ON_ERROR_STOP=1 -U "$${POSTGRES_USER:-statushub}" -d "$${POSTGRES_DB:-statushub}" \
			< "$$migration" || exit 1; \
	done

migrate-down:
	@find migrations -maxdepth 1 -name '*.down.sql' -print | sort -r | while read migration; do \
		docker compose -f deploy/compose.dev.yaml exec -T postgres \
			psql -v ON_ERROR_STOP=1 -U "$${POSTGRES_USER:-statushub}" -d "$${POSTGRES_DB:-statushub}" \
			< "$$migration" || exit 1; \
	done

migrate-check:
	./scripts/check-migrations.sh

bootstrap-github:
	docker compose -f deploy/compose.dev.yaml exec -T postgres \
		psql -v ON_ERROR_STOP=1 -U "$${POSTGRES_USER:-statushub}" -d "$${POSTGRES_DB:-statushub}" \
		< scripts/bootstrap-github-canary.sql

bootstrap-top5:
	docker compose -f deploy/compose.dev.yaml exec -T postgres \
		psql -v ON_ERROR_STOP=1 -U "$${POSTGRES_USER:-statushub}" -d "$${POSTGRES_DB:-statushub}" \
		< scripts/bootstrap-top5.sql

bootstrap-ecosystem:
	docker compose -f deploy/compose.dev.yaml exec -T postgres \
		psql -v ON_ERROR_STOP=1 -U "$${POSTGRES_USER:-statushub}" -d "$${POSTGRES_DB:-statushub}" \
		< scripts/bootstrap-ecosystem.sql

canary:
	go run ./cmd/statushub -url https://www.githubstatus.com -operation canary -timeout 30s

canary-top5:
	./scripts/canary-top5.sh

canary-ecosystem:
	./scripts/canary-ecosystem.sh

run-once:
	go run ./cmd/statushubd -database-url '$(LOCAL_DATABASE_URL)' -nats-url '$(LOCAL_NATS_URL)' -worker-id local -once

run-api:
	go run ./cmd/statushub-api -database-url '$(LOCAL_DATABASE_URL)' -nats-url '$(LOCAL_NATS_URL)' -allow-http-oidc

.PHONY: ui-install ui-build ui-check ui-dev
ui-install:
	cd web && npm ci

ui-build:
	cd web && npm run build

ui-check:
	cd web && npm test && npm run build

ui-dev:
	cd web && npm run dev
