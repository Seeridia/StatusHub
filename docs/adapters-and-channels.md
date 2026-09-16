# 状态页适配、通知渠道与死信重放


## 工作方式

状态页接入和通知使用统一链路：

```text
capability probe → 条件轮询 → engine decoder → canonical reconciliation
                 → transactional outbox → fanout → channel driver → delivery ledger / DLQ
```

稳定态只执行已缓存 capability 对应的 endpoint，不反复枚举引擎。所有公开源仍经过 DNS pinning、逐跳 SSRF 校验、超时、解压后 body 上限和条件请求；所有渠道仍由 delivery lease、attempt ledger、幂等键和统一重试策略控制。

## Engine 适配矩阵

| Engine                 | 探测/读取端点                                | 完整性与消失语义                                                                    | 实现说明                                                                                                                |
| ---------------------- | -------------------------------------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| incident.io Widget API | 页面所有者提供的完整 Widget URL              | 三个数组完整；可权威判断 ongoing incident 与 maintenance 消失                       | URL 不可猜测，必须显式 `-provider incident-io`；owner API 的 `incident-io/sdk-go` 需要凭据，不用于公开聚合              |
| Instatus v3            | `/v3/summary.json`、`/v3/components.json`    | components 完整；summary 仅在相应数组实际出现时才对 incident/maintenance 有消失权威 | 兼容状态值大小写；旧站可通过显式 endpoint/profile 扩展                                                                  |
| Better Stack           | `/index.json`                                | consolidated JSON:API payload 完整                                                  | 关联 `status_report` 与 `status_update`，组件、事件、更新时间一次归一化                                                 |
| Status.io              | `https://api.status.io/1.0/status/{page_id}` | consolidated payload 完整                                                           | 使用 API URL，或同时提供 `-page-id`；不从 HTML 猜 page id                                                               |
| Cachet v2/v3           | v3 `/api/*`，v2 `/api/v1/*`                  | 分页只有一页时 snapshot 完整；多页时标记 partial，禁止错误合成 resolved             | 同一个 decoder 接受 v2 flat object 与 v3 JSON:API attributes；`andygrunwald/cachet` 只适合作为 v2 行为参考              |
| Gatus                  | `/api/v1/endpoints/statuses`                 | endpoint 集合完整                                                                   | 最近最多 3 个结果中至少 2 次失败才产生 synthetic incident；事件和 component tag 明确标识 synthetic，不冒充服务 incident |
| cState                 | `/index.json`                                | systems 与 unresolvedIssues 完整                                                    | 使用静态 JSON 与条件请求；pinned issue 不自动当作未解决故障                                                             |
| HTML recipe            | recipe 指定的同源 path                       | 永远 partial、永远不通过“节点消失”推断恢复                                          | `x/net/html` + `cascadia`，启动时编译 CSS selector；无脚本、无二次请求、精确 host、匹配数量上限                         |

实现位于 `internal/adapter/ecosystem`。纯 decoder 不做网络访问，便于 fixture 回放；`vendorprofile.Adapter` 只负责选择已探测出的 engine。未知站点可以做低频 fallback probe，生产接入建议显式写 `sources.adapter_name`，避免额外探测 RTT。

### HTML recipe 配置

参考 `scripts/html-recipes.example.json`。`scripts/html-recipes.canary.json` 是绑定 GitHub Status 的只读公开 canary recipe，用于持续验证真实网络、HTML 解析和显式 provider 路由；生产 recipe 仍必须由运营方审核。daemon 使用：

```bash
STATUSHUB_HTML_RECIPES_FILE=/etc/statushub/html-recipes.json \
go run ./cmd/statushubd -database-url "$DATABASE_URL"
```

recipe 是受控配置，不接受用户脚本。上线前必须为目标页面保存 HTML fixture；模板变更导致 selector 失配时，采集失败并进入 source backoff，而不是制造“全部恢复”。

## 采集频率

资源分别调度；默认间隔、退避和缓存约束见 [默认采集策略](operations/configuration.md#默认采集策略)。只有汇总端点的引擎不会重复下载同一份 JSON。

## 通知渠道

### Endpoint 配置

`encrypted_config` 继续使用 JSON，由外部密钥层负责加解密。以下只是字段形状，真实 secret 不应进入 Git、日志或 DLQ：

| Channel              | 配置字段                                                                          |
| -------------------- | --------------------------------------------------------------------------------- |
| Teams Workflows      | `url`                                                                             |
| Discord webhook      | `url`；driver 强制附加 `wait=true`                                                |
| Telegram             | `secret`=bot token，`to`=chat id；`url` 可省略且固定为 `https://api.telegram.org` |
| 飞书/Lark custom bot | `url`、`secret`                                                                   |
| 钉钉 custom bot      | `url`、`secret`                                                                   |
| 企业微信群机器人     | `url`（其中的 `key` 本身是 secret）                                               |
| Shoutrrr             | `url`=Shoutrrr service URI；仅用于没有原生 driver 的长尾服务                      |

### 协议与成功判定

| Channel         | 请求合同                                 | 接受条件                               | retryable                                               | permanent / disable                               |
| --------------- | ---------------------------------------- | -------------------------------------- | ------------------------------------------------------- | ------------------------------------------------- |
| Generic Webhook | CloudEvents JSON + HMAC                  | HTTP 2xx                               | 408/425/429/5xx、网络错误                               | 其他 4xx；410 disable                             |
| Slack           | Incoming Webhook JSON                    | HTTP 2xx                               | 同上                                                    | 同上                                              |
| Teams           | Workflows message + Adaptive Card        | HTTP 2xx                               | 同上                                                    | 同上；不新增 legacy Office 365 Connector          |
| Discord         | webhook JSON，禁止 mentions，`wait=true` | HTTP 2xx；保存返回 message id          | 429/5xx、网络错误                                       | 其他 4xx；410 disable                             |
| Telegram        | `sendMessage`                            | HTTP 2xx 且 `ok=true`；保存 message_id | HTTP 429/5xx；业务 429，并读取 `parameters.retry_after` | 401/403 等鉴权/请求错误                           |
| 飞书/Lark       | interactive card，秒级 timestamp + HMAC  | HTTP 2xx 且 `code`/`StatusCode` 为 0   | 9499、99991663、HTTP 429/5xx                            | 签名错误 19021 disable；其他业务错误 permanent    |
| 钉钉            | text webhook，毫秒 timestamp/sign query  | HTTP 2xx 且 `errcode=0`                | -1、130101、HTTP 429/5xx                                | 310000/40035 disable；其他业务错误 permanent      |
| 企业微信        | text webhook                             | HTTP 2xx 且 `errcode=0`                | -1、45009、HTTP 429/5xx                                 | 40014/42001/93000 disable；其他业务错误 permanent |
| PagerDuty       | Events v2 + stable dedup key             | HTTP 2xx                               | 429/5xx                                                 | 其他 4xx                                          |
| SES / Twilio    | SDK/API 接受 + provider message id       | provider accepted                      | SDK/network/429/5xx                                     | 明确配置或鉴权错误；最终状态由 callback 更新      |
| Shoutrrr        | 单个 service URI、单次 `Send`            | library 返回 nil                       | library/网络错误，交回本系统 retry                      | URI/本地校验错误；原生渠道 scheme 被拒绝          |

Shoutrrr 不接管队列、重试或多目标 fanout。Slack、Discord、Telegram、SMTP、Teams 与 generic HTTP 等已有原生策略的 scheme 会被桥接层拒绝，防止绕开更严格的业务码解析和安全 transport。

## DLQ 查看与重放

retryable 错误达到 `MaxAttempts` 后，delivery 进入 `dead_letter`，保存有界错误摘要、时间和 replay count：

```bash
go run ./cmd/statushub-admin dlq-list \
  -database-url "$DATABASE_URL" \
  -limit 100

go run ./cmd/statushub-admin dlq-replay \
  -database-url "$DATABASE_URL" \
  -delivery-id 00000000-0000-0000-0000-000000000000
```

replay 是带行锁的原子状态迁移，并重新检查：subscription 启用、endpoint 启用、未 superseded、未过期，以及当前 endpoint secret version 曾用于该 delivery 的 attempt。任一条件不满足都会返回 `ErrReplayNotAllowed`，不会绕过安全变更。
