# 采集与事件一致性


## 数据路径

```text
source lease (PostgreSQL, SKIP LOCKED)
  → Statuspage summary / unresolved 并行拉取
  → baseline + watermark reconciliation
  → checkpoint + canonical event + outbox 原子提交（lease token fencing）
  → outbox lease
  → JetStream publish（稳定 Msg-Id）
  → 显式 ACK / redelivery
  → delivery UNIQUE 约束吸收重复消费
```

通知协议层包含 Generic Webhook（CloudEvents 1.0 + HMAC-SHA256）和 Slack Incoming Webhook；已接入 subscription matcher、fanout 与 delivery worker，详见 [通知](notifications.md)。

## 启动

首次配置、数据库初始化和常驻 worker 启动见 [安装与启动](operations/getting-started.md)。

## 独立 contract canary

一次检查：

```bash
make canary
```

持续低频检查（示例运行 24 小时，每 5 分钟一次）：

```bash
go run ./cmd/statushub \
  -url https://www.githubstatus.com \
  -operation canary \
  -iterations 0 \
  -interval 5m \
  -timeout 24h
```

Canary 只验证真实端点、schema 与规范化，不生成用户通知。

## 验证门禁

```bash
make verify
```

该命令执行：

1. `gofmt` 检查与 `go vet`；
2. 全仓 `go test -race`；
3. 所有 migration 按正序 up、逆序 down；
4. PostgreSQL source/outbox lease fencing 与并发 `SKIP LOCKED`；
5. JetStream publish dedupe、显式 ACK 与 redelivery；
6. publish 后标记前崩溃、delivery commit 后 ACK 前崩溃的跨组件测试。

## 故障语义

- 首次 snapshot 只建 baseline，不发送存量 incident。
- 旧 watermark 不覆盖较新 aggregate；同一 observation replay 不增加 revision。
- 只有完整且权威的 snapshot 连续两次缺失，才推断 incident resolved。
- checkpoint、canonical event、outbox 受同一 source lease token 保护并原子提交。
- publish 成功但 outbox 标记失败时不主动释放 lease；租约过期后使用同一 Msg-Id 重发。
- JetStream 是 at-least-once；最终幂等来自 canonical event 与 delivery 的数据库唯一约束。
- Webhook/Slack 的 2xx 只记为 `provider_accepted`，不宣称最终送达。
- 通知默认使用 DNS pinning/SSRF 防护并拒绝重定向；429 优先遵守 `Retry-After`。
