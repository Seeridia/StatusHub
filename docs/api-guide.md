# 管理 API

采集、事件、订阅和通知通过以下方式管理：租户隔离的管理 API 与浏览器控制台。控制面由 `statushub-api` 提供；采集和投递 worker 仍由 `statushubd` 承担。

## 能力边界

- REST API：vendor 汇总状态、source、incident timeline、subscription、endpoint、delivery/attempt、audit export。
- 实时事件：PostgreSQL catch-up + NATS live signal 的 SSE；断线后用租户绑定、HMAC 签名的 `Last-Event-ID` 续传。
- 身份：人员邮箱密码会话，以及独立的 API 服务账号 Token。
- 浏览器安全：可撤销的服务端会话、HttpOnly、SameSite、HTTPS Secure Cookie、CSRF 和同源 CSP；请求重新校验成员状态和角色。
- 写入：所有创建、更新、禁用、测试、重试和 rollout decision 都要求 `Idempotency-Key`；普通结果持久化 24 小时；服务账号 Token 的加密重试结果仅保留秘密 10 分钟，详见 [团队账号](operations/team-accounts.md)。
- 凭据：endpoint config 使用 AES-256-GCM envelope，AAD 绑定 endpoint ID 与 secret version；API 永不返回明文或密文。
- endpoint test：先持久化 job，再由独立 lease worker 异步执行，调用方可查询结果。
- adapter rollout：租户私有 source 可在 UI/API 启动 shadow、观察样本/错误/不一致率与 p95，再按门禁 promote 或 rollback。

当前静态 AES key backend 只用于本地和受控单实例环境。当前不提供 KMS/Vault、多 key 解密与在线轮换；不要把 `STATUSHUB_CONFIG_KEY` 提交到仓库或写入数据库。

## 本地启动

首次安装与已有环境重启统一按 [安装与启动](operations/getting-started.md) 执行。本手册保留管理 API 和高级运维细节。

- `make migrate-up` 没有迁移版本跟踪，不能在已初始化数据库上反复运行。
- `.env` 中两把密钥只在首次初始化生成，之后保留原值；API 与 worker 使用同一配置 key/key ID。
- 同时运行 `statushub-api` 和 `statushubd` 才具备网页、持续采集及正常通知投递。API 内另有渠道测试 worker。
- 本地 HTTP 显式使用 `-allow-local-http`；正式部署使用 HTTPS。
- `/healthz` 是存活检查，`/readyz` 联合检查 PostgreSQL 与 NATS live bridge；合同入口 `/openapi.yaml`，网页 `/ui/`。

`STATUSHUB_CONFIG_KEY` 加密渠道配置，`STATUSHUB_API_KEY` 派生会话和游标签名。更换前者会导致已有渠道无法解密；更换后者会使旧会话/游标失效。密钥保存与恢复见 [运行与维护](operations/maintenance.md)。

## 初始化工作区与账号

按 [首次启动](operations/getting-started.md#6-创建工作区和账号) 创建工作区及首位人员 Admin，随后从网页邀请成员和创建服务账号。完整权限与令牌管理见 [团队账号](operations/team-accounts.md)。

## 人员会话

`POST /auth/login` 只接收邮箱和密码。`GET /auth/session` 返回用户、可访问工作区、CSRF 和邮件配置状态。浏览器会话属于用户，业务请求按路径工作区校验当前成员权限。`POST /auth/logout` 撤销当前会话，`/auth/logout-all` 撤销全部会话。

初始化使用 `/auth/setup`。邮件邀请使用 `/auth/invitations/preview` 和 `/auth/invitations/accept`。密码接口为 `/auth/password/forgot`、`reset`、`change`。所有写请求检查来源，登录后的写请求携带 `X-CSRF-Token`。机器 Bearer Token 不能兑换浏览器会话。详细参数见 [OpenAPI](../api/openapi.yaml)。

## 幂等写入

每次逻辑写入生成一个新的 key；网络超时后重试同一请求时复用原 key：

```bash
IDEMPOTENCY_KEY="$(uuidgen)"
curl -fsS -X POST \
  -H "Authorization: Bearer $STATUSHUB_SERVICE_ACCOUNT_TOKEN" \
  -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ops","channel":"slack","config":{"url":"https://hooks.slack.com/services/..."}}' \
  'http://127.0.0.1:8080/v1/tenants/acme/endpoints'
```

同租户、同 key、同 method/path/body 返回原状态码与响应，并带 `Idempotency-Replayed: true`。复用 key 但改变 path 或 body 返回 409。进行中的请求返回 `idempotency_in_progress`；30 秒 lease 后可由相同请求接管，记录 24 小时后才可被新请求复用。

## Endpoint 安全与测试

UI/API 允许新建 `generic_webhook` 和 `slack`；其他渠道 driver 仍可由既有 bootstrap/admin 流程使用。URL 在 driver 层校验，实际发送统一经过禁止跳转、DNS/IP 审查、超时和 body 上限的安全 transport。

创建/轮换时 config 先校验再加密。Generic webhook 的 `signing_key_id` 是发给接收方的签名 key 标识，与数据库 envelope 的 `key_id` 不同。轮换 endpoint 必须提交当前 `expected_secret_version`，服务端使用 CAS 递增版本。

`POST /endpoints/{id}/test` 只入队，不在 API 请求内访问第三方。轮询 `GET /endpoint-tests/{test}`，最终状态为 `succeeded` 或 `failed`，并保留 HTTP status、provider message ID 或脱敏错误摘要。

## SSE 恢复语义

```bash
curl -N \
  -H "Authorization: Bearer $STATUSHUB_SERVICE_ACCOUNT_TOKEN" \
  -H "Last-Event-ID: $SIGNED_CURSOR" \
  'http://127.0.0.1:8080/v1/tenants/acme/events/stream'
```

无 cursor 时最多回放过去 24 小时；每次连接最多分 25 页追赶，每页 200 条。NATS 只承担低延迟唤醒，事件正文和租户可见性始终从 PostgreSQL 读取，因此重连与 NATS 重投不会越租户或重复推进 cursor。代理层必须关闭响应缓冲；服务端已发送 `X-Accel-Buffering: no` 和 15 秒 heartbeat。普通 API 使用 15 秒 request context deadline，SSE 显式排除。

## Shadow rollout

只有租户私有 source 显示“查看发布”操作；共享公开 source 由平台运维通过 平台 CLI 命令管理。创建 rollout 后 candidate 在有界后台路径采样，primary 采集不等待 candidate。

晋级需同时满足：`total >= minimum_samples`、`mismatch_rate <= maximum_mismatch_rate`、`error_rate <= maximum_error_rate`。数据库在 decision 事务中重新计算统计，UI 的绿色状态不能绕过服务端门禁。rollback 可停止 shadow，也可在 promote 后恢复创建时锁定的 baseline。
