# Deploy with Dokploy

[通用部署与镜像发布](deployment.md) · [README](../README.md)

Dokploy uses the same root `compose.yaml` as a normal Docker server. There is no platform-specific production file and no source build on the server. GitHub Actions builds and publishes the application image to GHCR first.

## 中文部署步骤

1. 在 GitHub Actions 运行 **Publish container**，或推送版本标签触发发布。等待构建与静态检查、镜像构建成功，在任务摘要中复制镜像 digest。首次发布检查 GHCR 包可见性；私有镜像需配置拉取凭据。
2. 在 Dokploy 新建 **Compose 服务**，关联本仓库，文件路径填写根目录 `compose.yaml`。
3. 在环境变量编辑器中按根目录 `.env.example` 填写 `STATUSHUB_IMAGE`、数据库密码、两把独立密钥、公开 HTTPS 域名和 SMTP。镜像优先填写上一步的 digest，不要填写一个尚未发布的版本。
4. 在域名配置中选择 **api** 服务、容器端口 **8080**，启用 HTTPS，并设置 DNS。`STATUSHUB_PUBLIC_URL` 使用同一 HTTPS 域名，不带 `/ui`。
5. 确认 Dokploy 的反向代理可以通过其代理网络连接 **api**。部分版本会自动接入；未自动接入时，检查平台生成的最终 Compose 和本页的排查步骤。不要把数据库和 NATS 加入代理网络。
6. 部署，检查 PostgreSQL/NATS 健康、迁移任务退出码为 0、API/worker 持续运行。迁移容器成功退出是正常行为。
7. 在 API 容器终端生成一次性初始化链接，详见[初始化步骤](deployment.md#initialize-the-workspace)。打开链接创建首位 Admin 和工作区，完成后自动登录；后续邀请与密码恢复需要真实邮件服务。

## Git Compose 与 Dokploy 域名配置

仓库将各服务的网络和环境变量显式展开，不使用 YAML `<<` 继承。这样平台解析后追加 API 的代理网络时，可以看到并保留原有 `backend` 网络。仓库不包含固定域名、Traefik 标签或 Dokploy 专属网络。

域名应由 Dokploy 的 Domains 页面管理，服务名选择 `api`，域名规则需启用；保存后执行 Deploy，不能只 Restart。更新此 Compose 不需要发布新镜像，也不需要更换 `STATUSHUB_IMAGE`、工作区或数据卷。

如果 API 直连健康，但公网仍然 404，检查最后一次部署是否成功、是否使用了最新 Git 提交、Domains 记录是否启用且绑定 `api`，以及最终生成的 Compose 中是否包含平台注入的标签和代理网络。显式展开配置并不能替代这些平台配置。不要通过删除数据库卷处理域名问题。

通用 Compose 默认还将 API 映射到宿主机 `127.0.0.1:8080`，此端口不向公网开放。如果已有应用占用该端口，在 Dokploy 环境变量中设置一个空闲的 `STATUSHUB_HTTP_PORT`；域名配置的**容器端口仍是 8080**。宿主机 loopback 不能直接被其他容器当作服务地址，容器代理应走共享网络。

## 更新与回退

发布新镜像后，先备份，再修改 `STATUSHUB_IMAGE` 并重新部署。不要重新生成密钥。涉及不兼容迁移时先停止 API/worker，确认新迁移成功后再启动；Compose 启动依赖不会自动停止旧进程。详细步骤见[数据库升级](deployment.md#schema-upgrades)。

当前迁移程序拒绝直接接管用旧版 `make migrate-up` 初始化、没有迁移记录的数据库。本地数据导入需要单独演练，不要通过删除表、伪造迁移记录或删除持久卷绕过检查。

不要仅靠将镜像改回旧版执行数据库回退。旧程序可能不兼容新 schema；需要恢复经过验证、与应用版本匹配的备份。

## 部署后检查

- 检查 `/healthz` 和 `/readyz`。
- 验证真实身份邮件和发送到自己渠道的测试通知。
- 查看来源最近成功采集、失败分类与下一次采集时间。
- 确保代理支持 SSE 长连接，不缓存认证接口。
- 安排 PostgreSQL、NATS 和密钥的异地备份，监控磁盘空间。

## Deploy automatically after a manual publish

Publishing from `main` also updates `ghcr.io/<owner>/statushub:latest`. Ordinary code pushes still do not publish images. Version tags and other branches publish immutable tags but do not trigger production deployment. Container publishing is serialized to prevent overlapping updates to `latest`.

1. In Dokploy, set `STATUSHUB_IMAGE=ghcr.io/<owner>/statushub:latest`. The root Compose applies `pull_policy: always` to api, worker and migrate. Keep Git push auto-deployment disabled; the workflow triggers deployment only after the image is available.
2. Create a Dokploy API key for an identity with access to this Compose service and deployment creation/read permissions. Store it as the GitHub Actions secret `DOKPLOY_API_KEY`; never commit or paste it into issue discussions.
3. Set GitHub Actions repository variables `DOKPLOY_URL` (HTTPS origin), `DOKPLOY_COMPOSE_ID`, and `STATUSHUB_PUBLIC_URL` (HTTPS origin). Set `DOKPLOY_DEPLOY_ENABLED=true` only after configuring the key and image tag.
4. Run **Publish container** manually on `main`. Its deploy job calls `/api/compose.deploy`, waits for the deployment identified by this workflow run, and checks `/healthz` and `/readyz`. API acceptance alone is not reported as deployment success. Check Dokploy logs if the job fails; an ambiguous trigger response is not automatically retried.

手动在 main 运行 Publish，镜像发布成功后才触发部署；不启用普通代码 push 的自动发布。完成密钥、变量和 Dokploy 镜像标签配置后，再将 `DOKPLOY_DEPLOY_ENABLED` 设为 `true`。部署任务失败不会自动回滚数据库。要暂停自动部署，将该变量改为 `false`；回滚应用时使用已验证的 SHA/digest，并先确认数据库兼容性。
