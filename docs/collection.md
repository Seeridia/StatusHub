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

通知协议层包含 Generic Webhook（CloudEvents 1.0 + HMAC-SHA256）、Slack Incoming Webhook、飞书交互式卡片和 SMTP 邮件；已接入 subscription matcher、fanout 与 delivery worker，详见 [通知](notifications.md)。

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

该命令执行前端国际化、TypeScript 与生产构建，并检查 Go 格式、`go vet` 和全仓编译。仓库当前不提交自动化测试文件。迁移、PostgreSQL lease fencing、JetStream 重投递和通知投递语义需要在隔离环境按发布清单人工验收，不能把构建成功当成运行链路已经验证。

## 故障语义

- 首次 snapshot 只建 baseline，不发送存量 incident。
- 旧 watermark 不覆盖较新 aggregate；同一 observation replay 不增加 revision。
- 只有完整且权威的 snapshot 连续两次缺失，才推断 incident resolved。
- checkpoint、canonical event、outbox 受同一 source lease token 保护并原子提交。
- publish 成功但 outbox 标记失败时不主动释放 lease；租约过期后使用同一 Msg-Id 重发。
- JetStream 是 at-least-once；最终幂等来自 canonical event 与 delivery 的数据库唯一约束。
- Webhook/Slack/飞书的 2xx 和 SMTP 接受只记为提供方接受，不宣称最终送达或已读。
- 通知默认使用 DNS pinning/SSRF 防护并拒绝重定向；429 优先遵守 `Retry-After`。
