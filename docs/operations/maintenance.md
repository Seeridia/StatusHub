# 运行与维护

[返回操作文档](README.md)

## 日常检查

在仓库目录载入配置后执行：

```bash
set -a
. ./.env
set +a
make infra-status
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:9464/metrics -o /dev/null
```

随后在网页检查：数据源成功采集时间是否推进、失败原因是否持续、浏览器是否实时连接、投递是否出现积压或死信。一个健康接口不能覆盖全部业务链路。

API 和 worker 由命令行启动时，日志在对应终端。Compose 日志只覆盖数据库和 NATS：

```bash
docker compose -f deploy/compose.yaml logs --tail=100 postgres nats
```

必要时只读查询来源状态：

```bash
docker compose -f deploy/compose.yaml exec -T postgres \
  psql -U "${POSTGRES_USER:-statusmon}" -d "${POSTGRES_DB:-statusmon}" \
  -c 'SELECT canonical_url, enabled, health_state, last_attempt_at, last_success_at, failure_streak, next_poll_at FROM sources ORDER BY last_attempt_at DESC NULLS LAST;'
```

记录完整 URL 时先确认它不包含私有 token 等敏感查询参数。

## 停止与重启

本地开发可在 API 和 worker 的终端分别 Ctrl-C。关闭 API 会中断网页请求；关闭 worker 会停止采集和正常投递。

暂停基础设施：

```bash
docker compose -f deploy/compose.yaml stop
```

重新启动后按[启动步骤](getting-started.md#5-启动服务)运行两个 Go 进程。

`make infra-down` 删除容器但通常保留命名数据卷；`docker compose down -v` 会删除数据卷，不能用于正常重启。正常启动不需要重新执行 bootstrap 或迁移。

## 版本升级

1. 确认目标版本变更，备份数据库和原密钥，保存当前版本标识、配置和二进制。
2. 在独立测试数据库验证新增迁移和目标版本；不要把 `make migrate-down` 当作自动升级的一部分。
3. 根据迁移要求安排维护窗口，停止可能写入不兼容 schema 的 API/worker。
4. 核对已经执行的迁移，仅按数字顺序运行尚未执行的 `.up.sql`。
5. 如前端变化，重新安装锁定依赖并构建网页，再编译 Go 程序。
6. 启动服务，检查 readiness、来源采集、事件详情、登录和投递，再恢复正常流量。

当前没有自动迁移历史表。维护人员需要保存已执行文件名、版本、时间和结果。不要根据“程序能启动”推断所有迁移都已完成。查找迁移文件：

```bash
ls migrations/*.up.sql
```

下例仅演示执行一份**已确认缺失**的迁移，不是每次升级固定运行：

```bash
docker compose -f deploy/compose.yaml exec -T postgres \
  psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-statusmon}" -d "${POSTGRES_DB:-statusmon}" \
  < migrations/000011_m5_control_plane.up.sql
```

若该文件对应的表已经存在，应先确认历史状态，不能直接重跑。

构建方式：

```bash
make ui-install
make ui-build
mkdir -p bin
go build -o bin/statusmon-api ./cmd/statusmon-api
go build -o bin/statusmond ./cmd/statusmond
go build -o bin/statusmon-admin ./cmd/statusmon-admin
```

API 的网页由编译时 embed 决定，正确顺序是“网页构建 → Go 构建 → 替换并重启”。服务启动参数与 `go run` 方式一致。数据库回退必须评估 down migration 的数据影响；仅回滚二进制不一定兼容新 schema。

## 数据库备份

以下命令为本地 Compose 示例。备份含配置、事件、账号哈希及其他租户数据，应以受控权限存储：

```bash
umask 077
mkdir -p tmp/backups
BACKUP_FILE="tmp/backups/statusmon-$(date +%Y%m%d-%H%M%S).dump"
docker compose -f deploy/compose.yaml exec -T postgres \
  pg_dump -U "${POSTGRES_USER:-statusmon}" -d "${POSTGRES_DB:-statusmon}" -Fc \
  > "$BACKUP_FILE"
test -s "$BACKUP_FILE"
```

`test -s` 只验证文件非空，不能代替恢复演练。数据库备份必须配套保存原配置加密密钥/key ID；只有数据库而没有原密钥，无法解密已有渠道。原密钥应在独立受控位置保管，不要直接提交 `.env` 或把它放进公开备份目录。

`tmp/` 被 Git 忽略，但不是长期备份存储。将已验证备份复制到受控的独立存储，并记录保留周期和恢复目标。

## 在独立数据库恢复演练

确认 `statusmon_restore_check` 不存在，且不是正在使用的业务数据库。将 `BACKUP_FILE` 设为上一步实际路径：

```bash
docker compose -f deploy/compose.yaml exec -T postgres \
  createdb -U "${POSTGRES_USER:-statusmon}" statusmon_restore_check

docker compose -f deploy/compose.yaml exec -T postgres \
  pg_restore --exit-on-error --no-owner --no-privileges \
  -U "${POSTGRES_USER:-statusmon}" -d statusmon_restore_check \
  < "$BACKUP_FILE"

docker compose -f deploy/compose.yaml exec -T postgres \
  psql -U "${POSTGRES_USER:-statusmon}" -d statusmon_restore_check \
  -c 'SELECT count(*) FROM sources; SELECT count(*) FROM incidents; SELECT count(*) FROM endpoints;'
```

首次命令若提示数据库已存在，不要直接删除或覆盖，先核对它的用途。不要启动连接恢复数据库的 notifier 来做数据验证，除非已隔离外部通知目的地；恢复的任务可能触发真实投递。

PostgreSQL 备份不包含 NATS JetStream 的 stream、consumer 或 ACK 状态。需要恢复完整运行环境时，应按 NATS 的 snapshot/restore 方案单独备份 JetStream，并在隔离环境演练重复消息和恢复追赶。不要直接删除 NATS 数据卷后假定所有历史消息都能自动重建。

## 正式部署前的必要工作

本仓库提供本地基础设施编排，不是开箱即用的高可用生产部署：

- 配置 HTTPS、正确 public URL、可信 OIDC 客户端和数据库凭据。
- 为 API/worker 配置进程管理、日志收集、启动环境和重启策略。
- 配置持久化、备份与恢复演练；API/worker 的加密密钥必须一致并可恢复。
- 反向代理为 SSE 关闭缓冲，允许长连接；不要缓存认证 API。
- 限制数据库、NATS 和 metrics 的网络暴露。
- 配置来源新鲜度、投递延迟、schema drift、DLQ 与容量监控，阈值参考 [通知](../notifications.md) 和实际基线。
- 当前静态 key backend 的限制、KMS/Vault 和在线轮换的未完成边界见 [管理 API](../api-guide.md)。

## 开发验证

```bash
make ui-check
make verify
```

这组命令会执行构建、测试和基础设施相关检查，不属于日常服务启动命令。真实公网 canary 会访问厂商，结果依赖网络与上游状态：

```bash
make canary-top5
make canary-ecosystem
```

大型容量测试有资源开销，不要在承载真实通知的环境随意执行。
