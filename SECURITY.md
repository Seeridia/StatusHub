# Security

## Reporting a vulnerability

Do not post credentials, private tenant data, or exploit details in a public issue. If the repository's Security tab offers **Report a vulnerability**, use that private reporting channel. Otherwise, ask the maintainer through their GitHub profile for a private contact before sharing details. Private vulnerability reporting is not guaranteed to be enabled.

Include the affected commit, configuration assumptions, a minimal reproduction using synthetic data, and the impact you observed. Redact tokens, cookies, invitation links, SMTP credentials, and database connection strings. No response-time or supported-release SLA is currently published.

## Operating securely

- Terminate public traffic over HTTPS and set the correct `STATUSMON_PUBLIC_URL`. Local HTTP and Mailpit settings are for development only.
- Store encryption keys and SMTP credentials outside Git. Back up keys separately from database backups; losing the configuration key can make stored channel credentials unrecoverable.
- Limit database and trusted operator CLI access. CLI operations use database authority and are not constrained by the current browser user's role.
- Use the least privileged member and service-account roles. Rotate compromised service tokens and revoke affected browser sessions.
- Preserve CSRF protection, tenant authorization, and outbound URL validation. Do not disable them to bypass integration errors.
- Review dependencies and rehearse upgrades and restores in an isolated environment. Static AES key management currently has no managed KMS/Vault or online multi-key rotation support.

Operational guidance: [team accounts](docs/operations/team-accounts.md), [configuration](docs/operations/configuration.md), and [maintenance](docs/operations/maintenance.md).
