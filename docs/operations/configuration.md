# 配置与账号

[返回操作文档](README.md)

## 环境变量

Go 程序读取进程环境，不自动读取 `.env`。在启动它的每个终端中执行：

```bash
set -a
. ./.env
set +a
```

| 配置 | 用途 / 默认行为 |
| --- | --- |
| `DATABASE_URL` | PostgreSQL 连接串；API、worker 和 admin CLI 使用 |
| `NATS_URL` | NATS 连接串；本地通常为 `nats://127.0.0.1:54222` |
| `STATUSHUB_CONFIG_KEY` | base64 编码的 32 字节 AES 配置加密密钥，API/worker 必须一致 |
| `STATUSHUB_CONFIG_KEY_ID` | 配置密钥版本标识，本地建议显式设为 `local-v1` |
| `STATUSHUB_API_KEY` | 独立的 base64 32 字节密钥，用于会话及游标；不是服务账号登录令牌 |
| `STATUSHUB_API_ADDRESS` | API 监听地址，默认 `127.0.0.1:8080` |
| `STATUSHUB_PUBLIC_URL` | 唯一公开访问地址及邮件链接来源，本地 `http://127.0.0.1:8080` |
| `STATUSHUB_REGION` | 区域身份，默认 `local`，影响来源 ownership |
| `STATUSHUB_HTML_RECIPES_FILE` | 可选受控 HTML recipe JSON 文件；API 和 worker 需要相容配置 |
| `AWS_REGION` | AWS SDK 使用的区域，本地示例 `us-east-1` |
| `POSTGRES_PORT/DB/USER/PASSWORD` | Compose 数据库初始化和端口配置 |
| `NATS_CLIENT_PORT` / `NATS_MONITOR_PORT` | Compose 映射端口，默认 54222 / 58222 |

Compose 中改变数据库口令不会自动修改已有数据卷里用户的口令；连接串也不会随端口环境变量自动改写。不要用删除数据卷来解决配置不一致。

worker 的 `-metrics-address` 默认 `127.0.0.1:9464`。`-collector-interval` 等参数表示扫描任务队列的频率，不是所有厂商固定轮询周期；实际轮询由资源能力、动态 cadence 和下一次调度时间决定。

## 默认采集策略

采集按资源分别调度，不是每次都请求全部接口：

| 资源 | 事故活跃期间 | 最近变化后 | 稳定期间 |
| --- | --- | --- | --- |
| 事故 / 未结束事故 | 60–90 秒 | 2–3 分钟 | 4–5 分钟 |
| 状态 | 90–120 秒 | 2–3 分钟 | 4–5 分钟 |
| 组件 / 完整摘要 | 2–3 分钟 | 3–4 分钟 | 4–5 分钟 |
| 计划维护 | 5–10 分钟 | 5–10 分钟 | 10–15 分钟 |

一般变化后保持 hot 10 分钟，随后 warm 使用 3–4 分钟间隔，30 分钟无变化转为 stable。事故仍未关闭但连续 30 分钟没有内容变化，也会降至稳定频率；后续采集发现新变化再加速。稳定时新事件可能等待约 5 分钟才被发现，再叠加上游发布、缓存和处理时间，不承诺秒级发现。

失败采用带下限的指数退避：首次 30–60 秒，第二次 1–2 分钟，第三次 2–4 分钟，逐渐增长至 7.5–15 分钟。使用数据库保存的连续失败次数，重启 worker 不会清零；上游 Retry-After 可以进一步延长等待。

这些是代码默认范围，缓存 TTL 和 Retry-After 是更严格的下限。同一来源有多个独立资源，相邻两次调度可以短于单个接口间隔；任务队列扫描间隔也不等于外部 HTTP 请求间隔。当前没有租户级频率编辑入口。修改调度默认值后需构建并重启 worker，已有资源在下一次采集后采用新间隔。

## 账号权限

| 能力 | viewer | operator | admin | owner |
| --- | --- | --- | --- | --- |
| 查看可见厂商、事件、规则、渠道、投递 | 是 | 是 | 是 | 是 |
| 添加来源、管理规则/渠道、测试渠道、重试投递 | 否 | 是 | 是 | 是 |
| 审计导出、成员/连接器管理权限 | 否 | 否 | 是 | 是 |
| 身份配置、区域灾备切换权限 | 否 | 否 | 否 | 是 |

这是服务端权限分组，不表示所有能力都已有网页按钮。平台 CLI 直接使用数据库权限，不会因为当前浏览器账号是 viewer 而自动受到同样限制；CLI 仅交给可信运维使用。

创建账号的参数为 `service-account-create -tenant-id ... -name ... -role ...`，网页操作和令牌管理见[团队账号](team-accounts.md)。日常操作通常使用 operator，纯查看使用 viewer；无需为了编辑通知规则发放 owner。

创建结果中的 `token` 才是服务账号登录凭据，格式以 `sa.` 开头。租户 slug、服务账号显示名称、`STATUSHUB_API_KEY` 都不是密码。现在支持仅邀请加入的邮箱密码成员、邮件验证/密码找回，以及网页服务账号停用和轮换，详见 [团队账号管理](team-accounts.md)。没有开放自助注册；丢失服务令牌应轮换，不应重置配置加密密钥。

## 团队登录

登录、初始化和邀请见 [团队账号](team-accounts.md)。`STATUSHUB_TRUSTED_PROXIES` 为逗号分隔的可信代理 CIDR；只从这些代理解析客户端转发地址。

## API 调用约定

在线合同入口：`http://127.0.0.1:8080/openapi.yaml`；源码合同见 [api/openapi.yaml](../../api/openapi.yaml)。租户路径可使用 slug 或 UUID。

- 服务账号使用 `Authorization: Bearer <token>`。
- cookie 会话的写入还需有效 CSRF token；不要通过关闭 CSRF 来绕过错误。
- 创建、更新、测试、重试等写入通常要求 `Idempotency-Key`。一次逻辑操作使用一个 key；不确定是否成功时，以相同 key 重试相同正文。
- 改变正文必须换新 key，否则可能得到 409。先查询状态，不要以不断换 key 的方式重试未知结果。
- URL 探测 `POST /v1/tenants/{tenant}/sources:probe` 使用 `{"url":"https://status.openai.com/"}`；探测本身不创建来源。创建来源也可只提供 URL，服务端识别身份，已有可见来源会复用。
- SSE 使用租户绑定的签名游标。浏览器会重连；代理应关闭 SSE 缓冲。

普通查看优先使用网页；批量操作按对应运行手册核对权限、幂等键及作用范围。不要把真实凭据放在 URL 中。
