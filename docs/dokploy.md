# Deploying on Dokploy

[README](../README.md) · [中文部署步骤](#中文部署步骤)

Use a **Docker Compose** service sourced from this Git repository. The deployment file is `deploy/dokploy.compose.yaml`, with the build context set to the repository root (`..` relative to the Compose file). It builds the web console and Go binaries from source. This configuration is for a single-server deployment, not a high-availability cluster.

The stack has PostgreSQL 17, NATS JetStream, a one-shot schema migration job, an API, and a collection/delivery worker. Only the API joins the external `dokploy-network`. There are no host port mappings for the database, NATS, or metrics. Keep the Dokploy project's identity stable so subsequent deployments reuse the same named volumes.

## Prepare configuration

Generate a URL-safe database password with `openssl rand -hex 32`. Generate each of the two application keys independently with `openssl rand -base64 32`. Save all three in a password manager, then configure them in the Dokploy service's environment editor:

```dotenv
POSTGRES_PASSWORD=<hex database password>
STATUSMON_CONFIG_KEY=<first base64 key>
STATUSMON_CONFIG_KEY_ID=production-v1
STATUSMON_API_KEY=<second base64 key>
STATUSMON_REGION=production
STATUSMON_PUBLIC_URL=https://status.example.com
STATUSMON_SMTP_ADDRESS=smtp.example.com:587
STATUSMON_SMTP_FROM=status@example.com
STATUSMON_SMTP_USERNAME=<SMTP username>
STATUSMON_SMTP_PASSWORD=<SMTP password>
```

Compose derives `DATABASE_URL` and `NATS_URL` using its internal service names. The database password must be URL-safe; the hex generator above meets this requirement. Do not change the password environment variable alone after a PostgreSQL volume has been initialized: it does not change the existing database user's password.

SMTP must support STARTTLS (typically port 587). Implicit TLS on port 465 is not supported. No Mailpit service is included, and plaintext SMTP is disabled. Identity emails are sent only when someone requests verification or password recovery; they do not use your incident notification channels.

Do not regenerate configuration keys during redeployment. Stored channel credentials depend on the original key and key ID. Keep keys and database backups in separately protected storage.

## Create the Dokploy service

1. Create a project/environment and add a **Compose** service.
2. Connect `https://github.com/Seeridia/vendor-status-monitoring` and select `main` (or a reviewed deployment commit).
3. Set the Compose path to `deploy/dokploy.compose.yaml`. Use the Git checkout as the source; copying only the YAML into a raw editor does not provide the Docker build context.
4. Enter the environment variables above. Do not put secrets into the repository or build arguments.
5. Under domain routing, select service **api**, container port **8080**, and your hostname. Enable HTTPS and create the required DNS record to the Dokploy server. Set `STATUSMON_PUBLIC_URL` to that same HTTPS origin, without `/ui`.
6. Deploy. PostgreSQL and NATS must become healthy; `migrate` must exit with code 0; API and worker must remain running. A successfully exited migration container is expected, not a crashed service.

The file expects Dokploy's external network named `dokploy-network`. If your installation uses a different proxy network, change that network declaration. The backend network must retain outbound access because the worker contacts public status pages and notification destinations.

The API listens on plain HTTP inside the container; Dokploy terminates HTTPS. Do not enable `-allow-http-oidc` for the public deployment. Avoid proxy buffering or short timeouts for `/v1/tenants/*/events/stream`; do not cache `/auth` or authenticated API responses.

## Initialize the workspace

Use the **api container terminal** in Dokploy after deployment. These commands run inside the container, where `DATABASE_URL` is already configured:

```sh
statusmon-admin tenant-create -slug acme -name 'Acme'
```

Save the returned workspace `id`, then replace the example below with that UUID and your real Owner email:

```sh
statusmon-admin owner-invite -tenant-id '<workspace UUID>' -email 'owner@example.com'
```

The result contains `invitation_token`. Treat the terminal output as a secret. Do not include it in deployment logs, screenshots, or support requests. Open:

```text
https://status.example.com/ui/?tenant=acme#/account-flow?mode=invite&token=<invitation_token>
```

Request the verification email, follow its link, and set a password. Log in at `https://status.example.com/ui/?tenant=acme`. From Settings, invite members and create service accounts as needed. No default account, password, or demonstration data is created.

Add a public status-page URL from Settings to start monitoring. Create a notification channel and rule. Test only a destination you control: a channel test makes a real outbound request.

## Verify and maintain

- `https://status.example.com/healthz` should return success.
- `/readyz` should return success after the database and NATS live bridge connect.
- Check the source's last successful collection and next scheduled poll in the console. Initial polling establishes a baseline; old incidents do not all generate new notifications.
- Verify a real identity email and a notification to your own destination before inviting the team.
- Back up the PostgreSQL volume/database, NATS JetStream data, and application keys. A volume is persistent storage, not a backup. Monitor disk space and arrange off-server backups.
- Dokploy/Compose restarts exited services; a failed health check alone does not automatically restart a running container. Investigate unhealthy API status and worker logs.

### Schema upgrades

`statusmon-migrate` embeds the up migrations and records each filename and SHA-256 checksum in `statusmon_schema_migrations`. A database advisory lock serializes concurrent migration jobs. Each migration and its ledger entry commit in the same transaction. Rerunning an unchanged deployment skips applied migrations.

It rejects edited applied migrations, gaps, a database with newer migrations, and an existing untracked schema. It does **not** automatically adopt a database initialized using the old `make migrate-up` command. For an existing installation, first rehearse an audited migration/import process on a restored copy; do not delete tables or fabricate the ledger to bypass this check.

For upgrades, back up first and arrange a maintenance window. Stop API and worker before deploying schema changes that are incompatible with running code. Compose startup dependencies do not stop old containers before a new migration begins. Ensure the migration job is recreated from the new build on each deployment; inspect its logs and image version. A manual Compose workflow from the server checkout is:

```sh
# Run with the same Compose project name and environment used by Dokploy.
docker compose -f deploy/dokploy.compose.yaml stop api worker
docker compose -f deploy/dokploy.compose.yaml build
docker compose -f deploy/dokploy.compose.yaml run --rm migrate
docker compose -f deploy/dokploy.compose.yaml up -d --force-recreate api worker
```

Do not run these commands with an unrelated project name: that can create different volumes. If migration fails, leave application services stopped and resolve the issue before continuing. No automatic down migration is provided. Restoring an older application can be incompatible with the current schema; restore a tested matching backup when required.

## 中文部署步骤

1. 在 Dokploy 新建 **Compose 服务**，关联本仓库，填写路径 `deploy/dokploy.compose.yaml`。该文件需要完整 Git 构建上下文，不能只复制 YAML。
2. 在环境变量中配置上面的数据库密码、两把独立密钥、公开域名和真实 SMTP。数据库密码使用 `openssl rand -hex 32`，两把密钥各执行一次 `openssl rand -base64 32`。不要提交这些值。
3. 域名选择 **api / 8080**，开启 HTTPS；`STATUSMON_PUBLIC_URL` 与域名保持一致，不包含 `/ui`。数据库、NATS 不需要开放端口。
4. 部署后检查 PostgreSQL/NATS 健康、迁移容器退出码为 0、API/worker 持续运行。
5. 在 API 容器终端使用上面的 `tenant-create` 创建工作区，再执行 `owner-invite` 邀请首位 Owner。邀请令牌属于秘密。通过真实邮件完成验证后登录，添加状态页、渠道与通知规则。
6. 设置数据库、NATS 数据和密钥的异地备份。升级前备份，涉及不兼容迁移时先停 API/worker；启动依赖不能代替停机窗口。

新增迁移程序有版本、校验值和并发锁保护，重复部署不会重跑已成功的迁移。**它拒绝直接接管旧版无迁移记录的数据库**；当前本地数据若要迁到 Dokploy，需要单独演练导入，不能直接导入后绕过校验。不要执行 `down -v` 或删除持久卷来排查启动问题。

更多账号与邮件说明见 [团队账号](operations/team-accounts.md)，备份步骤见 [运行维护](operations/maintenance.md)。

## Validation scope

The migration lifecycle is exercised against a disposable PostgreSQL database, including concurrent startup, retries, rollback, checksum changes, and refusal of untracked schemas. Compose configuration and Linux Go builds are also checked. A full image build must still succeed on your builder; local verification was blocked by Docker Hub authentication endpoint timeouts. Domain routing, real SMTP delivery, and deployment on your Dokploy instance require environment-specific verification.
