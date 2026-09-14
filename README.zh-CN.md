# StatusHub

**在一个工作区，了解团队所依赖服务的状态。**

[English](README.md) · [安装与启动](docs/operations/getting-started.md) · [操作文档](docs/operations/README.md) · [API](api/openapi.yaml) · [参与贡献](CONTRIBUTING.md)

StatusHub 是可自托管的服务状态监控与通知平台。输入公开状态页 URL，查看自动检测到的适配器，再为团队配置通知规则。React + TDesign 控制台集中展示服务状态、事件时间线、采集健康与通知投递记录，默认英文，支持简体中文及明暗主题。

## 核心能力

- **通过 URL 接入**：复用通用状态页适配器，减少逐服务开发。
- **查看事件**：统一事件与组件影响，区分服务更新时间和系统采集时间。
- **解释采集状态**：展示最近成功、下次采集、失败原因和退避；采集失败不等于服务故障。
- **配置通知**：网页创建 Slack 和签名 Webhook 渠道，按订阅规则投递，查看重试与死信记录。
- **管理团队**：邀请注册、邮箱密码、四级角色，以及服务账号创建、停用和令牌轮换。

## 适配范围

支持 Atlassian Statuspage、incident.io Widget API、Instatus、Better Stack、Status.io、Cachet、Gatus、cState、受控 HTML recipe 和 AWS public Health。部分引擎需要专用 API URL 或页面所有者配置；AWS public Health 适配器为实验性实现，提供 RSS 回退。

通用引擎可面向数千个服务扩展，不表示已经逐一验证数千个站点。实际兼容性以 URL 检测结果和上游接口为准；品牌展示不代表合作关系或适配保证。

采用自适应轮询：稳定事故资源默认 4–5 分钟，活跃期间 60–90 秒，并遵守上游缓存与退避要求。不是秒级可用性探针。详见[适配器说明](docs/adapters-and-channels.md)和[默认采集策略](docs/operations/configuration.md#默认采集策略)。

## 开始使用

需要 Go 1.25+、Node.js 22.12+、npm、Python 3、Docker Compose v2。

```bash
git clone https://github.com/Seeridia/StatusHub.git
cd StatusHub
```

按[安装与启动](docs/operations/getting-started.md)生成密钥，启动 PostgreSQL、NATS、Mailpit，初始化空数据库，然后运行 API 与 worker。该指南包含首位 Owner 的邀请、邮件验证和登录流程。

控制台地址：`http://127.0.0.1:8080/ui/?tenant=local`。没有默认密码，也不开放自主注册。已有实例请遵循[升级说明](docs/operations/maintenance.md#版本升级)，不要重复执行初始化。

## 文档导航

| 文档                                                                                                 | 内容                               |
| ---------------------------------------------------------------------------------------------------- | ---------------------------------- |
| [网页操作](docs/operations/console-guide.md)                                                         | 状态页接入、事件、规则、渠道与投递 |
| [团队账号](docs/operations/team-accounts.md)                                                         | 邀请、角色、密码恢复与服务账号     |
| [配置](docs/operations/configuration.md)                                                             | 环境变量、采集频率和接口约定       |
| [运行维护](docs/operations/maintenance.md) / [故障排查](docs/operations/troubleshooting.md)          | 升级、备份、恢复和排查             |
| [管理 API](docs/api-guide.md)                                                                        | 登录集成、幂等写入、SSE            |
| [采集](docs/collection.md) / [通知](docs/notifications.md) / [高级运维](docs/advanced-operations.md) | 数据一致性、投递语义及 CLI 操作    |
| [前端开发](docs/react-console.md)                                                                    | TDesign、构建与国际化              |

## 开发与部署

生产部署使用根目录 `compose.yaml` 拉取 GHCR 镜像，由 GitHub Actions 测试、构建并发布。详见[通用容器部署](docs/deployment.md)和 [Dokploy 部署指南](docs/dokploy.md#中文部署步骤)。

```bash
make ui-install
make ui-dev
make ui-check
make verify
```

`make verify` 包含数据库和 NATS 集成测试，请使用可丢弃的开发数据库。网页资源嵌入 Go API；生产网页变更需要重新构建并重启 API。

生产部署需要公开 HTTPS 地址、独立 SMTP、持久存储和密钥保管。Compose 用于本地开发；当前静态 AES 密钥后端不提供 KMS/Vault 或在线多密钥轮换。贡献约定见 [CONTRIBUTING.md](CONTRIBUTING.md)，漏洞报告见 [SECURITY.md](SECURITY.md)。

## 许可证与品牌

项目源码许可证尚未确定，本 README 不授予开源许可；添加许可证前，分发和再利用需取得著作权人许可。第三方素材保留各自条款，详见[品牌素材来源](docs/brand-assets.md)。服务名称和 Logo 归各自权利人所有。
