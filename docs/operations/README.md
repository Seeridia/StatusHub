# 操作文档

这组手册面向当前 React + TDesign 控制台和 Go 后端。所有命令默认在仓库根目录执行，示例使用本地 Docker Compose；服务器部署不能直接照搬本地 HTTP 和默认数据库口令。

## 从哪里开始

| 任务 | 文档 |
| --- | --- |
| 第一次安装、已有环境重启、创建登录账号 | [安装与启动](getting-started.md) |
| 查看状态、添加状态页、设置渠道和通知规则 | [网页操作手册](console-guide.md) |
| 配置项、角色权限、OIDC、API 使用约定 | [配置与账号](configuration.md) |
| 邀请成员、邮箱密码、服务账号轮换与撤销 | [团队账号管理](team-accounts.md) |
| 日常检查、升级、备份恢复、停机 | [运行与维护](maintenance.md) |
| 登录失败、没有数据、探测失败、通知未到达 | [故障排查](troubleshooting.md) |

推荐首次使用路径：安装 → 启动 API 和 worker → 邀请首位 Owner 并验证邮箱 → 登录 → 检查官方来源 → 配置渠道 → 测试渠道 → 创建通知规则 → 查看投递记录。

## 系统由哪些部分组成

| 组件 | 用途 | 本地入口 |
| --- | --- | --- |
| PostgreSQL | 状态快照、事件、配置、任务和审计 | `127.0.0.1:55432` |
| Mailpit | 本地身份邮件沙箱 | `http://127.0.0.1:58025` |
| NATS JetStream | 事件总线和实时唤醒 | `127.0.0.1:54222` |
| `statusmon-api` | 登录、REST API、网页、SSE、渠道测试 worker | `http://127.0.0.1:8080/ui/` |
| `statusmond` | 持续采集、事件处理、订阅匹配、实际通知投递 | 指标：`http://127.0.0.1:9464/metrics` |
| Vite（开发可选） | 前端热更新，API 代理到 8080 | `http://127.0.0.1:5173/ui/` |

只启动网页 API 不会持续采集或执行正常通知投递；渠道测试由 API 内的独立 worker 执行，因此“测试成功”也不能代替对 `statusmond` 的检查。

## 深入资料

- [控制台开发与样式规范](../react-console.md)
- [通知投递与可观测性](../notifications.md)
- [状态页适配、通知渠道与死信重放](../adapters-and-channels.md)
- [私有代理、多区域与审计](../advanced-operations.md)
- [管理 API、OIDC、SSE 与幂等写入](../api-guide.md)
- [OpenAPI](../../api/openapi.yaml)
