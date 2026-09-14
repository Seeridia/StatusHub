# 高级运维

本文命令供持有数据库权限的可信平台运维使用，不受浏览器 RBAC 约束。日常成员管理见 [团队账号](operations/team-accounts.md)。

## AWS Account Health connector

部署 [`deploy/aws-account-health.yaml`](../deploy/aws-account-health.yaml)，将栈输出的 SNS HTTPS subscription endpoint 指向：

```text
POST /v1/connectors/aws-health/{connector_id}
```

然后注册 connector：

```bash
go run ./cmd/statushub-admin aws-connector-create \
  -database-url "$DATABASE_URL" \
  -tenant-id "$TENANT_ID" \
  -account-id 123456789012 \
  -sns-topic-arn arn:aws:sns:us-east-1:123456789012:statushub-health \
  -regions us-east-1,us-west-2 \
  -services ec2,rds
```

入口验证 SNS RSA signature v1/v2、AWS 证书主机、Topic ARN、AWS account、region 与 service allowlist。SubscriptionConfirmation 只在签名和 Topic 绑定都通过后自动执行。重复和同语义事件幂等；乱序事件进入 canonical 审计时间线但不能覆盖较新的 connector entity state。CloudFormation 栈包含 EventBridge、SNS、重试策略和 SQS DLQ。

## Private Delivery Agent

创建 agent 并绑定 `channel='private_agent'` 的 endpoint：

```bash
go run ./cmd/statushub-admin private-agent-create \
  -database-url "$DATABASE_URL" -tenant-id "$TENANT_ID" -name dc-primary

go run ./cmd/statushub-admin private-agent-bind \
  -database-url "$DATABASE_URL" -agent-id "$AGENT_ID" -endpoint-id "$ENDPOINT_ID"
```

token 只返回一次，数据库只保存 SHA-256。agent 只向 SaaS 发起 HTTPS 长轮询：

```bash
STATUSHUB_AGENT_ID="$AGENT_ID" STATUSHUB_AGENT_TOKEN="$AGENT_TOKEN" \
go run ./cmd/statushub-agent \
  -server-url https://statushub.example.com
```

delivery 的 lease、attempt、重试和 DLQ 仍在服务端。completion 同时校验 delivery lease token 和 agent/endpoint 绑定，错误 agent 无法完成其他 agent 的工作。只有 agent 进程可以访问租户内网；SaaS 的 SSRF 策略不会因此放宽。

## 账号与权限

参见 [团队账号](operations/team-accounts.md) 和 [管理 API](api-guide.md)。

## 不可变审计导出

审计事件以租户为单位持有 `sequence + previous_hash + event_hash`。追加事务锁定 `audit_heads`，并发写入仍保持连续；`audit_events` 的 UPDATE/DELETE 由数据库 trigger 拒绝。哈希对 canonical JSON 和全部安全相关字段做长度分隔，避免拼接歧义。

```bash
go run ./cmd/statushub-admin audit-export \
  -database-url "$DATABASE_URL" -tenant-id "$TENANT_ID" \
  -actor-type user -actor-id "$AUDITOR_ID" > audit.ndjson

go run ./cmd/statushub-admin audit-verify -file audit.ndjson
```

完整离线校验必须从 sequence 1 导出；`-after-sequence` 只用于增量归档，验证增量时应携带上一段最终 hash 并由归档系统衔接。数据库 trigger 防止普通应用角色修改记录；hash chain 还能检测越权 DBA 或备份层面的删除/篡改。生产应将 NDJSON 定期写入启用 Object Lock/WORM 的对象存储。

## 多区域 ownership 与灾备

每个 daemon 设置稳定区域名：

```bash
STATUSHUB_REGION=cn-east \
go run ./cmd/statushubd -database-url "$DATABASE_URL" -worker-id cn-east-collector-01
```

daemon 每 10 秒写 region heartbeat，并只为“尚未有 owner”的 source 初始化 home/active region。另一区域上线不会自动夺权。采集 lease 返回 ownership epoch，checkpoint、canonical event 与 outbox 的提交同时校验：

```text
lease_token matches
AND lease_until is fresh
AND active_region matches
AND ownership_epoch matches
```

灾备切换必须读取当前 epoch，并确认目标 region heartbeat 新鲜：

```bash
go run ./cmd/statushub-admin source-region-change \
  -database-url "$DATABASE_URL" -source-id "$SOURCE_ID" \
  -target-region cn-west -expected-epoch 7 \
  -heartbeat-max-age 30s -actor-id "$OWNER_ID" \
  -reason 'INC-2048 regional failover'
```

租户私有 source 必须同时传 `-tenant-id`；全局公开 source 必须省略它，二者不可混用。切换事务递增 epoch、清除旧 lease、将 source 立即置为 due。恢复 home region 使用同一命令和最新 epoch。不要在数据库复制仍可能双主写入时绕过此控制面；推荐 source ownership 与 source/event ledger 位于同一强一致 PostgreSQL writer。

## Shadow adapter rollout

`internal/adapter/shadow` 的 primary fetch 位于响应路径；候选 adapter 运行在有界异步队列。控制面缓存 miss 只触发有界后台刷新，队列满时宁可跳过样本，也不拖慢生产采集。比较 projection 排除 observation/transport/adapter version，排序实体后计算 canonical SHA-256。

```bash
go run ./cmd/statushub-admin adapter-rollout-create \
  -database-url "$DATABASE_URL" -source-id "$SOURCE_ID" \
  -candidate-name atlassian-statuspage -candidate-version statuspage-v2/1 \
  -sample-rate 0.1 -minimum-samples 500 \
  -maximum-mismatch-rate 0 -maximum-error-rate 0.01 \
  -actor-id "$OWNER_ID"

go run ./cmd/statushub-admin adapter-rollout-status \
  -database-url "$DATABASE_URL" -rollout-id "$ROLLOUT_ID"

go run ./cmd/statushub-admin adapter-rollout-promote \
  -database-url "$DATABASE_URL" -rollout-id "$ROLLOUT_ID" \
  -actor-id "$OWNER_ID" -reason '500 samples, zero semantic mismatch'

go run ./cmd/statushub-admin adapter-rollout-rollback \
  -database-url "$DATABASE_URL" -rollout-id "$ROLLOUT_ID" \
  -actor-id "$OWNER_ID" -reason 'post-promotion regression'
```

daemon 已将当前二进制内注册的 Statuspage、AWS public health 和全部 ecosystem adapter 接入 shadow registry；未来同时编译旧/新实现时，用 `name@version` 增加候选即可。promote 在同一事务中重新计算样本数、semantic mismatch rate 和 candidate error rate；未达到 rollout 自带门禁会拒绝。promote 后迟到的 shadow result 被丢弃。rollback 会恢复创建 rollout 时锁定的 baseline adapter 名称与版本。租户 source 的所有命令都必须传正确 `-tenant-id`。
