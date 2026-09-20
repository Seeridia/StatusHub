# StatusHub

**One workspace for the status of the services your team depends on.**

[简体中文](README.zh-CN.md) · [Getting started](docs/getting-started.md) · [Operations (中文)](docs/operations/README.md) · [API](api/openapi.yaml) · [Changelog](CHANGELOG.md) · [Contributing](CONTRIBUTING.md)

StatusHub is a self-hosted vendor status monitoring and notification platform. Add a public status-page URL, inspect the detected adapter, and subscribe to the events that matter to your team. The React and TDesign console brings vendor status, incident timelines, collection health, and notification delivery into one place.

## What you can do

- **Connect status pages by URL.** Reuse adapters for common status-page engines instead of building a separate integration for every vendor.
- **Understand incidents.** Read normalized updates and component impact while keeping vendor timestamps separate from collection timestamps.
- **Monitor collection health.** See successful checks, scheduled polls, backoff, and failure classifications separately from vendor outages.
- **Route notifications.** Configure subscriptions, create Slack, Feishu/Lark, SMTP email, and signed webhook channels in the console, and inspect delivery attempts and retries.
- **Work as a team.** Invite members, use email/password, assign Viewer/Operator/Admin roles, and manage service accounts.
- **Use English or Chinese.** The responsive console defaults to English and supports Simplified Chinese, light mode, and dark mode.

## Status-page compatibility

Adapters cover Atlassian Statuspage, incident.io Widget API, Instatus, Better Stack, Status.io, Cachet, Gatus, cState, controlled HTML recipes, and AWS public Health. Some engines require a specific API URL or page-owner configuration. AWS public Health uses an experimental adapter with RSS fallback.

Common engines make it possible to extend coverage across thousands of services. This is an integration model, **not a claim that thousands of individual sites have been verified**. Compatibility depends on the URL probe and the upstream endpoint. Ecosystem logos indicate brands, not affiliation or guaranteed support.

Collection uses adaptive polling and upstream cache constraints. Stable incident resources normally poll every 4–5 minutes; active incident resources every 60–90 seconds. This is not a second-by-second availability probe. See the [adapter reference](docs/adapters-and-channels.md) and [polling configuration](docs/operations/configuration.md#默认采集策略).

## Get started

Requirements: Go 1.25+, Node.js 22.12+, npm, Python 3, and Docker Compose v2. Exact Go requirements are in [go.mod](go.mod).

```bash
git clone https://github.com/Seeridia/StatusHub.git
cd StatusHub
```

Follow the [local installation guide](docs/getting-started.md) to generate keys, start PostgreSQL/NATS/Mailpit, initialize an empty database, and run the API and worker. It also walks through the one-time setup link for the first Admin and using Mailpit for later invitations and password recovery.

The console runs at `http://127.0.0.1:8080/ui/?tenant=local`. There is no default password or public self-registration. Existing installations should follow the [upgrade guide](docs/operations/maintenance.md#版本升级), rather than rerunning initialization commands.

## How it works

```text
Status pages → adapters → normalized events → subscriptions → notification channels
                    │             │                                  │
              collection health   PostgreSQL + NATS             delivery ledger
                                  │
                             API + web console
```

Shared sources are collected independently of tenant subscriptions. PostgreSQL holds checkpoints, events, memberships, and delivery records; NATS JetStream carries durable event signals. Database uniqueness and transactional writes handle replay. A provider accepting a notification does not prove that a person received or read it.

| Component | Purpose |
| --- | --- |
| `statushub-api` | Management API, embedded web console, browser authentication, identity mail jobs |
| `statushubd` | Collection, event processing, and notification delivery |
| `statushub-admin` | Trusted operator CLI for workspace initialization and advanced administration |
| `statushub-agent` | Optional outbound delivery agent for private networks |

## Documentation

| Guide | Audience |
| --- | --- |
| [Getting started](docs/getting-started.md) / [安装与启动](docs/operations/getting-started.md) | First-time operators |
| [Console guide](docs/operations/console-guide.md) | Workspace members; Chinese |
| [Team accounts](docs/operations/team-accounts.md) | Workspace administrators; Chinese |
| [Platform administration](docs/operations/platform-administration.md) | Instance administrators; Chinese |
| [Configuration](docs/operations/configuration.md), [maintenance](docs/operations/maintenance.md), [troubleshooting](docs/operations/troubleshooting.md) | Self-hosting operators; Chinese |
| [API reference](docs/api-guide.md), [OpenAPI schema](api/openapi.yaml) | Integrators |
| [Collection](docs/collection.md), [notifications](docs/notifications.md), [advanced operations](docs/advanced-operations.md) | Backend contributors and operators; Chinese |
| [Frontend development](docs/react-console.md), [brand attribution](docs/brand-assets.md) | Frontend contributors; Chinese |

## Development

```bash
make ui-install
make ui-dev       # Vite on port 5173; requires the API on port 8080
make ui-check     # Frontend type checking, translations, and production build
make verify       # Go build/vet plus frontend type, translation, and production build checks
```

Use a disposable development database for migration or manual integration checks. The frontend build is embedded in the Go API, so rebuild and restart the API after changing production web assets. See [CONTRIBUTING.md](CONTRIBUTING.md) for code layout and review expectations.

## Deployment and security

Use the root `compose.yaml` with a published GHCR image. GitHub Actions builds and publishes versioned images; deployment servers only pull them. See [container deployment](docs/deployment.md) or [Dokploy](docs/dokploy.md).

Configure a public HTTPS URL, independent SMTP credentials, persistent storage, and protected encryption keys before deployment. The root Compose file is the production image deployment; local infrastructure uses `deploy/compose.dev.yaml`. Key management currently uses a static AES backend; managed KMS/Vault and online multi-key rotation are not provided. Review [operations](docs/operations/maintenance.md) and [security reporting](SECURITY.md).

## License and trademarks

StatusHub is licensed under the [GNU Affero General Public License v3.0](LICENSE) (`AGPL-3.0-only`). Network deployments that modify the program must make the corresponding source available as required by the license. Third-party assets retain their own terms; see [brand attribution](docs/brand-assets.md). Vendor names and logos belong to their respective owners.
