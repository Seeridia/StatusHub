# Contributing to StatusHub

Start with the [installation guide](docs/getting-started.md). Use an isolated development workspace and database; never run fixtures, reset migrations, or load tests against a live installation.

The project license is pending. Discuss substantial contributions with the maintainer before submitting code; no contributor license agreement or automatic relicensing policy is implied.

## Development layout

- `cmd/`: API, worker, operator CLI, and private agent entry points.
- `internal/adapter/`: capability detection and normalized status-page adapters.
- `internal/pipeline/`: event processing, subscription fanout, and delivery.
- `internal/store/postgres/`: persistence, leases, and transactions.
- `internal/controlplane/`: HTTP handlers and embedded console assets.
- `web/`: React, TypeScript, TDesign, and translation resources.
- `migrations/`: paired PostgreSQL up/down migrations.
- `api/openapi.yaml`: public API contract.

## Before a pull request

1. Describe the user-visible problem and a reproducible example. Discuss broad API or behavior changes in an issue first.
2. Keep changes focused. Add regression coverage for behavior changes, especially tenant isolation, authorization, replay, and failure recovery.
3. Run `make ui-install` when dependencies change, then `make verify` in an isolated environment. Run `python3 scripts/check-public-docs.py` for documentation changes.
4. Update OpenAPI and operational documentation when interfaces or configuration change. Frontend text must support English and Simplified Chinese; use the existing TDesign tokens.
5. Include expected behavior, relevant test results, and migration impact in the pull request. Keep generated console assets in sync with the frontend build.

Adapter changes should include sanitized fixtures and cover unknown fields, partial snapshots, upstream failures, and duplicate observations. Live canaries contact external status pages; use them sparingly and honor their request limits.

## Repository content

Commit product documentation, operator instructions, and contributor references. Keep internal planning, research notes, design review records, local test reports, logs, screenshots containing customer data, credentials, and database exports outside the repository. The documentation check rejects known private document paths and common local-only file types; it does not replace reviewing the staged diff.

Use fictional domains and accounts in examples. Do not include invitation URLs, session cookies, webhook credentials, or service tokens in issues and pull requests. Follow [SECURITY.md](SECURITY.md) for vulnerabilities.
