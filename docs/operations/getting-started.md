# 安装与启动

[返回操作文档](README.md)

## 1. 环境要求

- Go 1.25+（以仓库 `go.mod` 为准）。
- Docker 与 Docker Compose v2；macOS 可使用 OrbStack 或 Docker Desktop。
- Node.js 22.12+、npm：用于构建或开发 React 控制台，运行已经编译的 Go 二进制不需要 Node。
- Python 3：用于下文首次生成本地配置及读取账号 JSON。
- 可访问服务公开 HTTPS 状态页的网络。

检查工具：

```bash
go version
node --version
npm --version
docker compose version
python3 --version
```

Compose 启动 PostgreSQL、NATS 和本地邮件沙箱 Mailpit，不会自动启动 API 或采集程序。

## 2. 首次初始化配置

**已有 `.env` 时跳过本节，保留原密钥。** 以下命令拒绝覆盖已有文件，根据 `deploy/local.env.example` 创建仅当前用户可读的本地配置：

```bash
python3 - <<'PY'
from pathlib import Path
import base64
import os

path = Path('.env')
text = Path('deploy/local.env.example').read_text()
for key in ('STATUSHUB_CONFIG_KEY', 'STATUSHUB_API_KEY'):
    value = base64.b64encode(os.urandom(32)).decode()
    text = text.replace(key + '=\n', key + '=' + value + '\n')
text += '\nDATABASE_URL=postgres://statushub:statushub_local_only@127.0.0.1:55432/statushub?sslmode=disable\n'
text += 'NATS_URL=nats://127.0.0.1:54222\n'
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as output:
    output.write(text)
print('已创建 .env；请保管其中密钥。')
PY

set -a
. ./.env
set +a
```

修改端口、数据库账号后，同时修改连接 URL。`.env` 不会被 Go 程序自动读取，每个新终端都需要显式载入。此示例仅用于本地环境。

## 3. 首次初始化数据库

```bash
make infra-up
make infra-status
```

确认 PostgreSQL 和 NATS healthy，Mailpit 正在运行。检查数据库是否已经初始化：

```bash
docker compose -f deploy/compose.dev.yaml exec -T postgres \
  psql -U "${POSTGRES_USER:-statushub}" -d "${POSTGRES_DB:-statushub}" -c '\dt'
```

**只有空数据库才执行：**

```bash
make migrate-up
make bootstrap-top5
```

`make migrate-up` 按顺序执行所有 `.up.sql`，没有迁移版本跟踪，不适合每次启动都执行。已有表时请看[升级步骤](maintenance.md#版本升级)。不要使用 `make migrate-down` 修复启动错误，它会回退甚至删除业务表。

`bootstrap-top5` 配置 GitHub、Cloudflare、OpenAI、Anthropic 和 AWS 的公开来源；它不会产生网页演示事件。`bootstrap-ecosystem` 是可选的适配器验证站点集，含 Demo 站点，不建议把它当成必需的正式监控清单。

## 4. 构建网页

```bash
make ui-install
make ui-build
```

构建结果写入 `internal/controlplane/assets/console`，随后由 Go embed 编译进 API。修改网页后需重新构建并重启 API；只刷新浏览器不会更新已经运行的旧 Go 二进制。

## 5. 启动服务

保持两个终端运行。终端 A 启动 API：

```bash
set -a
. ./.env
set +a
export STATUSHUB_SMTP_ADDRESS=127.0.0.1:51025
export STATUSHUB_SMTP_ALLOW_LOCAL_PLAINTEXT=true
go run ./cmd/statushub-api -allow-local-http
```

终端 B 启动采集与投递：

```bash
set -a
. ./.env
set +a
go run ./cmd/statushubd -worker-id local-live
```

API 和 worker 必须使用同一配置加密密钥及 key ID。`-allow-local-http` 只用于此处的本地 HTTP 调试，正式部署使用 HTTPS。

另开终端验证：

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:9464/metrics -o /dev/null
```

`/readyz` 检查 PostgreSQL 和实时总线连接；来源是否已采集仍需在网页检查“最近成功采集”。

## 6. 创建工作区和账号

先载入 `.env`。仅首次创建；同名租户或账号已经存在时不要反复执行。

运行一次性初始化命令：

```bash
go run ./cmd/statushub-admin setup-link
```

打开返回的链接，30 分钟内填写邮箱、密码、工作区名称和标识，即可创建首位 Admin 并自动登录。仅空实例可初始化，重新生成链接使旧链接失效。此步骤不需要 SMTP，也不会标记邮箱已验证。随后可在个人账号中验证邮箱，在设置中发送成员邀请。本地邮件在 Mailpit 中查看。

完整权限、SMTP 与账号恢复见 [团队账号](team-accounts.md)。


## 7. 已有环境重新启动

无需重新生成密钥、迁移、创建租户或导入来源：

1. 启动 Docker/OrbStack。
2. 在仓库目录载入原 `.env`，执行 `make infra-up`。
3. 按第 5 节分别启动 API 和 worker。
4. 打开原工作区，必要时重新登录。

如果已有 API 占用 8080，不要重复启动第二份；先确认进程，再到原终端 Ctrl-C 停止。

## 8. 前端开发和演示

```bash
make ui-dev
```

- 真实数据：`http://127.0.0.1:5173/ui/?tenant=local`，仍需 8080 API、数据库和 worker。
- 设计演示：`http://127.0.0.1:5173/ui/?demo=1#/overview`，仅用于开发预览。
- 对外运行入口使用 8080 内嵌页面；生产构建不会通过 `demo=1` 绕过认证。

`make run-api` 和 `make run-once` 使用 Makefile 的 `LOCAL_DATABASE_URL` / `LOCAL_NATS_URL`，不会自动采用同名以外的 URL 配置。自定义环境优先使用上面的 `go run` 命令。`make run-once` 会运行一次完整 worker 流程，可能执行待投递通知，不是只读探测命令，也不能代替常驻 worker。
