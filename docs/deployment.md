# Container deployment

[README](../README.md) · [Dokploy](dokploy.md)

The root `compose.yaml` runs PostgreSQL, NATS JetStream, a one-shot migration job, API, and worker. It pulls published images; it does not build on the deployment server. All three application services use the exact same `STATUSHUB_IMAGE`. This is a single-server setup, not a high-availability cluster.

## Publish an image

The image name is `ghcr.io/seeridia/statushub` (registry paths use lowercase). Older packages under `statushub` are not renamed automatically.

The frontend is built once on the native build platform. Go cross-compiles the same source for each target architecture; only runtime image setup may use emulation. BuildKit caches dependency and build layers between workflow runs. Initial cache population and registry uploads still add time.

The `Publish container` GitHub Actions workflow runs the shared test suite before publishing `linux/amd64` and `linux/arm64` images to GHCR. It uses `GITHUB_TOKEN` with `packages: write`; do not create or commit a registry password.

- Push a version tag such as `v0.1.0` to publish that tag and `sha-<full-commit-sha>`.
- Run the workflow manually from Actions to publish a commit image without declaring a release.
- Read the job summary for the immutable `ghcr.io/seeridia/statushub@sha256:...` reference. Use that digest for reproducible deployment. No `latest` tag is published.

The workflow definition alone does not mean an image exists. Wait for a successful publish job before deploying it. On first publication, check the GHCR package visibility: a public Git repository does not automatically guarantee a public package. Set package visibility to public if anonymous pulls are intended; otherwise configure registry credentials in Dokploy or `docker login ghcr.io` using a credential with `read:packages`.

Version tags should not be moved or reused. This workflow publishes containers, not a GitHub Release announcement or a deployment to your server.

## Configure and start

On a normal Docker server, obtain `compose.yaml` and `.env.example` from the matching repository revision. Copy `.env.example` to `.env`, restrict it to the deployment user (`chmod 600 .env`), and replace every placeholder:

| Variable | Value |
| --- | --- |
| `STATUSHUB_IMAGE` | Published GHCR tag or digest |
| `POSTGRES_PASSWORD` | URL-safe password, generated with `openssl rand -hex 32` |
| `STATUSHUB_CONFIG_KEY` | Independent key from `openssl rand -base64 32` |
| `STATUSHUB_API_KEY` | Another independently generated base64 key |
| `STATUSHUB_CONFIG_KEY_ID` | Stable identifier such as `production-v1` |
| `STATUSHUB_PUBLIC_URL` | Your public HTTPS origin, without `/ui` |
| `STATUSHUB_SMTP_ADDRESS` | STARTTLS SMTP host and port, typically `smtp.example.com:587` |
| `STATUSHUB_SMTP_FROM` | Sender email |
| `STATUSHUB_SMTP_USERNAME` / `STATUSHUB_SMTP_PASSWORD` | SMTP credentials when required |

Keep the encryption keys unchanged across deployments and back them up securely. Changing `POSTGRES_PASSWORD` alone does not update an existing PostgreSQL volume's user password. Implicit TLS SMTP on port 465 is not supported; plaintext identity SMTP is disabled.

```sh
docker compose pull
docker compose up -d
docker compose ps -a
docker compose logs migrate
```

PostgreSQL and NATS must be healthy, `migrate` must exit with code 0, and API/worker must remain running. The API binds to host loopback `127.0.0.1:8080` by default; configure a host reverse proxy to forward HTTPS to this address. Do not enable the API's local HTTP development flag. For a containerized proxy, attach **only api** to that proxy's network using a local override; see [Dokploy](dokploy.md).

No database, NATS, or metrics ports are exposed on the host. The backend network must retain outbound access for status pages, SMTP, and notification destinations. Keep the Compose project name stable so redeployments reuse the existing named volumes. A persistent volume is not a backup.

## Initialize the workspace

Use `docker compose exec api sh`, or the **api container terminal** in Dokploy after deployment. These commands run inside the container, where `DATABASE_URL` is already configured:

```bash
statushub-admin setup-link
```

在 API 容器内执行上述命令并打开返回的一次性链接，填写管理员与首个工作区。仅空实例允许初始化。管理员日后在网页直接发送邀请邮件。此版本使用全新数据库基线，不支持旧账号或业务数据自动升级。

## Verify and maintain

- `https://status.example.com/healthz` should return success.
- `/readyz` should return success after the database and NATS live bridge connect.
- Check the source's last successful collection and next scheduled poll in the console. Initial polling establishes a baseline; old incidents do not all generate new notifications.
- Verify a real identity email and a notification to your own destination before inviting the team.
- Back up the PostgreSQL volume/database, NATS JetStream data, and application keys. A volume is persistent storage, not a backup. Monitor disk space and arrange off-server backups.
- Dokploy/Compose restarts exited services; a failed health check alone does not automatically restart a running container. Investigate unhealthy API status and worker logs.

### Schema upgrades

`statushub-migrate` embeds the up migrations and records each filename and SHA-256 checksum in `statushub_schema_migrations`. A database advisory lock serializes concurrent migration jobs. Each migration and its ledger entry commit in the same transaction. Rerunning an unchanged deployment skips applied migrations.

It rejects edited applied migrations, gaps, a database with newer migrations, and an existing untracked schema. It does **not** automatically adopt a database initialized using the old `make migrate-up` command. This release has a new identity baseline and requires a fresh installation; it does not migrate previous accounts or business data. Never fabricate a migration ledger to bypass this check.

For upgrades, back up first and arrange a maintenance window. Stop API and worker before deploying schema changes that are incompatible with running code. Compose startup dependencies do not stop old containers before a new migration begins. Ensure the migration job is recreated from the newly pulled image on each deployment; inspect its logs and image version. A manual Compose workflow from the server checkout is:

```sh
# Run with the same Compose project name and environment used by Dokploy.
docker compose stop api worker
docker compose pull
docker compose run --rm migrate
docker compose up -d --force-recreate api worker
```

Do not run these commands with an unrelated project name: that can create different volumes. If migration fails, leave application services stopped and resolve the issue before continuing. No automatic down migration is provided. Restoring an older application can be incompatible with the current schema; restore a tested matching backup when required.


## Validation scope

Validate builds and Compose configuration before deployment. Verify health, SMTP delivery and account flows in your own environment.
