# Changelog

All notable user-visible changes to StatusHub are recorded here. The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and intends to use [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-09-21

### Added

- URL-based status-page discovery with reusable adapters and a curated service address catalog.
- Normalized incident timelines, collection-health diagnostics, adaptive polling, and durable PostgreSQL/NATS processing.
- React 19 and TDesign console with English as the default language, Simplified Chinese, responsive layouts, and light/dark themes.
- Email/password accounts, one-time first-instance setup, three workspace roles, invitations, password recovery, and service accounts.
- Workspace-managed data sources with platform/shared source linking, replacement, archive/restore, and dependency-aware notification rules.
- Slack, Feishu/Lark interactive cards, SMTP email, and signed generic webhook channels with delivery history and retries.
- Multi-architecture GHCR publishing and optional verified Dokploy deployment from the manual publish workflow.

### Changed

- Renamed the project, repository, container image, CLI commands, configuration prefix, and product UI to StatusHub.
- Simplified browser authentication to user-level server sessions and workspace selection after login.
- Replaced Owner/Admin role overlap with one active human Admin per workspace, plus Operator and Viewer roles.
- Updated GitHub checkout and toolchain setup actions to Node.js 24 runtime versions.

### Fixed

- Treat an empty JetStream consumer wait deadline as an idle poll instead of repeatedly logging it as a worker failure.

### Security

- Added CSRF-protected server sessions, Argon2id password storage, encrypted channel secrets, SSRF-aware outbound delivery, and tenant-scoped authorization checks.

[Unreleased]: https://github.com/Seeridia/StatusHub/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Seeridia/StatusHub/releases/tag/v0.1.0
