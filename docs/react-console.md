# React + TDesign 控制台

默认 `/ui/` 使用 React 19 + TypeScript + TDesign React 1.18，参考 TDesign Starter 的浅色工作区、侧栏导航、统计卡片和表单布局。原有手写控制台保留在 `/ui/legacy.html`，便于迁移期间对照。

日常使用见 [网页操作手册](operations/console-guide.md)，首次安装和登录见 [安装与启动](operations/getting-started.md)。

## 开发与构建

要求 Node 22.12+、npm，以及项目原有 Go 环境。

```bash
make ui-install
make ui-dev
# http://127.0.0.1:5173/ui/
```

开发服务器把 `/auth` 和 `/v1` 代理至 `127.0.0.1:8080`。正常模式支持邮箱密码、OIDC 和服务账号登录。OIDC 回调仍指向 API 配置的 public URL；回到开发页面后恢复同主机会话即可。

```bash
make ui-build
make run-api
# http://127.0.0.1:8080/ui/
```

Vite 输出至 `internal/controlplane/assets/console`，由 Go embed 打包，不需要独立 Node 生产进程。路由使用 HashRouter，避免子路径刷新依赖代理 rewrite；筛选与事件详情 ID 保存在 hash 内的 query 参数中。

生产资源带内容 hash；JS/CSS 预压缩为 gzip，服务端根据 Accept-Encoding 提供压缩版本。HTML 不缓存，hash 资源使用 immutable 缓存。路由按需加载，React runtime 单独缓存。

## 设计与功能

- 界面默认使用英文，可在登录页或登录后的顶部栏切换 English／简体中文；选择保存在当前浏览器中。
- 默认跟随系统主题，支持浅色／深色并记住选择。
- 总览：全部可见厂商、活跃事件、受影响厂商、数据过期；异常提示、厂商卡片／表格和最近事件。
- 厂商：名称搜索、状态筛选、详情抽屉，以及关联事件入口。
- 事件：服务端厂商／阶段筛选、游标加载、事件时间线、厂商时间与系统采集时间分开呈现。
- 通知规则：新建与编辑、厂商多选、影响阈值、事件类型、多渠道、启停状态、右侧摘要。规则内可新增渠道并保留草稿。
- 通知渠道：Slack／Webhook 创建，异步测试状态持续查询至最终成功或失败。
- 投递：状态筛选、尝试时间线、失败原因与 dead-letter 重试确认。
- 设置：输入 URL 自动探测身份、适配器和资源能力；展示检测结果后确认接入，已有来源复用；采集健康、租户私有适配器发布／门禁／回滚、管理员审计记录。
- 响应式：手机侧栏、单列表单、卡片布局和固定保存区；宽表在容器内横向滚动。

## 语义与边界

厂商服务状态与采集新鲜度独立显示。后端 `collection` 合同按资源最后成功检查点的 `next_poll_at + 2 分钟` 计算期限，失败重试不延长期限；厂商汇总取可见且启用来源的最差状态，停用来源不参与。旧检查点缺少资源成功时间时显示等待检查点，后续成功采集自然补齐。前端不再使用固定 5 分钟阈值，使用后端截止时间更新逾期显示。

总览展示全部可见厂商，暂未添加独立的“我的关注”持久化接口。没有伪造可用率、历史趋势或成功率图表。服务／区域选择、官方事件链接及组件影响范围尚缺完整 API，未在页面中提供不可用控件。

最低影响级别也作用于恢复事件；选择重大阈值时，无影响的恢复更新可能被过滤。界面对此给出说明。编辑现有规则会保留关键词和静默时段配置；涉及组件、标签或不能表示为厂商×事件组合的 scope 会禁用编辑，避免扩大范围。

渠道修改／凭据轮换 API 已存在，但控制台只提供创建与测试；渠道禁用、轮换及更复杂配置可继续使用 API。不要把“渠道接受”解释为终端用户已经阅读。

SSE 保留服务端签名 cursor，正常 EOF 和异常断开都会重连，采用退避与短时间合并刷新。列表保留缓存内容；不在每个事件到达时重建整页。写入网络结果不确定时，重试相同请求会复用 Idempotency-Key。UI 根据服务端身份控制操作权限，服务端继续进行最终权限校验。

## 安全与兼容

新增租户限定的 `GET /v1/tenants/{tenant}/session` 与 `POST /v1/tenants/{tenant}/logout`，支持 service account 获取真实角色与退出。`TenantContext` 现在明确输出小写 JSON 字段，与控制台会话合同一致；依赖旧版大写字段的外部客户端需要同步更新。session 响应使用 no-store。

脚本与样式表保持同源 CSP。仅 React HTML 增加 `style-src-attr 'unsafe-inline'`，供组件定位和尺寸样式使用；没有开放内联脚本、eval 或外部域名。图标样式使用本地静态 CSS，抽屉／弹窗通过外部 CSS class 锁定滚动，避免组件动态 style 标签被策略阻止。

## 演示与验证

开发模式可打开 `http://127.0.0.1:5173/ui/?demo=1#/overview`。页面明确标注演示数据，数据只存在于内存，测试按钮不会访问第三方渠道。生产构建排除演示适配器，`?demo=1` 不会绕过生产认证。

```bash
make ui-check
make verify
```

前端回归覆盖规则 scope 的交集与保守编辑、采集过期判断、幂等重试、分页选项读取和 SSE 分块／续传解析。Go 测试覆盖静态资源、gzip 协商、CSP、租户身份和跨租户拒绝。

可选生产页面浏览器夹具，使用真实 Go HTTP handler 与内存记录，不连接数据库或 notifier：

```bash
STATUSHUB_UI_BROWSER_FIXTURE=1 go test ./internal/controlplane \
  -run '^TestConsoleBrowserFixture$' -v -timeout=16m
# http://127.0.0.1:5174/ui/，工作区 acme，测试令牌 valid
```

夹具默认跳过，显式启动后最长运行 15 分钟，也可 Ctrl-C 停止。真实 IdP 与通知渠道需在独立环境验证。

参考：
- https://tdesign.tencent.com/starter/react/dashboard/base
- https://tdesign.tencent.com/starter/docs/react/get-started

## TDesign 样式维护约定

样式来源以安装版本 `tdesign-react/es/style/index.css` 的 token 定义为准；不要复制独立颜色表或为每页创建一套字体/间距。

| 项目 | 使用约定 |
| --- | --- |
| 正文 | `--td-font-body-medium`，14px / 22px |
| 辅助文字 | `--td-font-size-body-small`，12px |
| 页面标题 | `--td-font-headline-small`，24px / 32px |
| 区块标题 | `--td-font-title-medium`，16px / 24px |
| 面板圆角 | `--td-radius-medium`；登录面板 `--td-radius-extraLarge` |
| 间距 | `--td-size-*`、`--td-comp-padding*`；桌面页面 24px，手机 16px |
| 颜色 | `--td-text-color-*`、`--td-bg-color-*`、品牌和语义状态色 |
| 主题 | 官方 `theme-mode`，同步 `color-scheme` |

面包屑使用原生 Breadcrumb；按钮、表格、表单、Tag、Drawer、Dialog、Timeline 沿用组件默认交互。布局断点和侧栏尺寸属于应用布局，不需要强行替换成组件 token。

侧栏只由外层控制展开/折叠宽度，Menu 使用百分比宽度，避免写死的菜单宽度盖过父容器边框。时间线的完整日期放在内容区域正常文档流中，不放进狭窄固定标签栏。长 URL、标题、ID 及错误正文须能换行或使用表格自带省略提示；宽表在内部滚动，不能导致整个页面横向溢出。

变更后至少检查：登录、7 个主页面、规则编辑、渠道/来源抽屉、事件/投递详情，以及浅色/深色和 390px 手机宽度。没有真实业务数据时可用隔离演示验证视觉状态，但不要将演示测试描述为真实通知验证。

## 国际化维护

控制台使用 `i18next` 和 `react-i18next`，TDesign 的全局组件语言与当前界面语言同步。浏览器首次打开时使用英文；用户选择保存在 `localStorage` 的 `statushub-language` 中，后续访问保持该语言。页面的 `<html lang>`、标题、日期格式以及 TDesign 分页、选择器和空状态文案会一并切换。

源代码以中文原文作为稳定翻译键，界面文案使用 `tr("中文原文")`，带变量的文案使用 i18next 插值，例如 `tr("查看 {{value0}}", { value0: name })`。英文资源集中在 `web/src/lib/i18n.ts`。新增或修改界面文案时必须补上英文翻译，不要拼接需要调序的句子。

执行以下命令检查所有非测试前端源码。检查会拒绝缺少英文资源的 `tr()` 文案，以及未经过国际化处理的中文字符串：

```bash
cd web
npm run check:i18n
```

`npm run build` 已包含这项检查。切换语言会重新载入页面，使导航、选项等模块级静态文案也使用新语言；当前会话和工作区不会因此退出。

## 登录首页与服务生态

登录首页展示通用适配器的扩展能力及 60 个生态品牌的本地 SVG Logo。品牌墙属于产品介绍，不从租户查询接口生成，也不表示这些厂商已经全部接入或逐一通过适配验证；实际接入以状态页检测结果为准。“面向数千个服务扩展”描述通用引擎适配能力，不是实时监控数量。

品牌标识和远程图标映射位于 `web/src/lib/brands.ts`，来源及回退规则见 [品牌素材来源](brand-assets.md)。厂商 Logo 按 Devicon、Simple Icons、内置通用图标的顺序加载；图片保留明确尺寸并懒加载。目录外的用户输入名称也会尝试生成安全的远程图标地址。修改映射后需重新构建网页并重启 API。
