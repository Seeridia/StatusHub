# 通知投递与可观测性


## 投递链路

规范化事件通过以下链路投递：

```text
canonical event/outbox
  -> JetStream durable consumer
  -> fanout plan + leased shards + checkpoint
  -> subscription coarse scope + exact rule
  -> fair delivery leases
  -> Webhook / Slack / SES / PagerDuty / Twilio
  -> provider callback ledger
```

关键正确性边界：

- `fanout_plans(event_id, rule_version)` 和 delivery 逻辑键共同吸收重复消息；
- shard 只在 lease token 仍有效时提交，分页 checkpoint 与 delivery 同事务；
- scope 的 `NULL` 是通配符；支持 vendor、上游 `component_key`、tag、event kind；
- quiet hours 按 IANA timezone 计算，跨午夜安全；critical 可显式绕过；
- notifier 每轮按 tenant 限额领取，且同 endpoint 前序未终结 delivery 会阻挡后序消息；
- provider `accepted` 与最终 `delivered` 分离；SES/Twilio callback 幂等推进最终状态；
- SNS callback 验签限定 AWS SNS HTTPS 证书域名；Twilio 使用其 HMAC-SHA1 协议验签；
- Generic Webhook、Slack、PagerDuty、Twilio 共用禁止重定向、DNS pinning 和私网阻断的 transport。

## Top 5 profile

| Vendor | 策略 |
|---|---|
| GitHub | 标准 Atlassian Statuspage summary + unresolved |
| Cloudflare | Statuspage，8 MiB 有界 transport；小 incidents 端点与 summary 分 cadence |
| OpenAI | incident.io 的 Statuspage 兼容面；summary 不要求 `incidents` 字段，显式禁用 404 的 unresolved/upcoming，增加 `/incidents.json` |
| Anthropic | `status.anthropic.com` 规范化为 `status.claude.com`，避免重复 source |
| AWS | UTF-16BE `public/currentevents` 实验性 adapter；失败或 schema 不兼容时切换 RSS |

运行全部公开端点 contract canary：

```bash
make canary-top5
```

注册五个共享 source：

```bash
make infra-up
make bootstrap-top5
```

## Endpoint 配置

渠道配置通过 AES-256-GCM envelope 加密保存。以下为加密前字段形状；使用管理接口写入，不要直接将明文写入 `encrypted_config`：

```json
{"url":"https://hooks.example.com/status","secret":"..."}
```

Slack 只需 `url`；PagerDuty 需要 `secret`（Events API v2 routing key，URL 可省略）；SES 需要 `from`、`to`、可选 `configuration_set`；Twilio 需要 `account_sid`、`secret`、`from`、`to`、`callback_url`，API URL 可省略。

SES 使用 AWS SDK for Go v2 `service/sesv2`，`delivery_id` 作为 EmailTag 写入。SNS 回执 URL：

```text
POST /v1/provider-callbacks/ses/{endpoint_id}
```

endpoint 配置还需 `sns_topic_arn`，只接受匹配 topic 且签名有效的 SNS Notification。

Twilio 状态 URL：

```text
POST /v1/provider-callbacks/twilio/{endpoint_id}
```

`callback_url` 必须等于 Twilio 控制台实际配置的公开 URL，否则签名校验会失败。

## Generic Webhook 签名

请求为 CloudEvents 1.0 JSON，包含 `X-Delivery-ID` 和 `X-Event-ID`。`X-Signature-Timestamp` 是请求时间，`X-Signature` 格式为 `v1,kid=<key-id>,t=<timestamp>,sig=<hex>`。

接收方使用共享密钥对 `timestamp + "." + 原始请求正文` 计算 HMAC-SHA256，并以常量时间比较十六进制签名。校验时间戳的允许偏差并按 delivery ID 去重，避免重放；不要先解析或重新序列化 JSON 再验签。签名 key ID 与配置加密 key ID 是不同概念。

## SLO 指标

- `statusmon_source_freshness_seconds`：计划拉取 deadline 到成功观察的延迟（首次拉取使用请求耗时）；
- `statusmon_delivery_eligible_first_attempt_seconds`：delivery eligible 到首次领取；
- `statusmon_source_schema_drift_total`：同 source/resource schema hash 变化；
- `statusmon_fanout_duration_seconds`、`statusmon_fanout_deliveries_total`：fanout 延迟与吞吐；
- 原有 poll、outbox、bus、notification 指标继续保留。

建议首个告警阈值：active source freshness p95 > 30s、eligible first-attempt p95 > 5s、任意 schema drift > 0。Prometheus histogram 的 p95 由 `histogram_quantile(0.95, sum by (le, ...)(rate(..._bucket[5m])))` 计算。
可直接加载的规则位于 [`deploy/prometheus-alerts.yaml`](../deploy/prometheus-alerts.yaml)。
