# 网页操作手册

[返回操作文档](README.md) · [登录账号准备](getting-started.md#6-创建工作区和账号)

## 登录与导航

打开 `/ui/`，使用邮箱密码登录，然后选择工作区。侧栏显示当前工作区及角色。看不到“添加”“创建”或“重试”按钮时，先检查是否登录了只读账号，不要把界面隐藏理解为功能故障。

界面首次打开时默认使用英文。登录页和登录后的顶部栏都可以切换 English／简体中文，选择会保存在当前浏览器中，刷新和再次访问后仍然有效。语言切换会重新载入当前页面，不会退出会话或改变工作区数据。

顶部可切换浅色、深色或跟随系统。左上角按钮用于折叠侧栏；手机端用于打开导航。退出登录会清除当前浏览器会话，不会删除工作区、停止采集或撤销服务账号令牌。

## 总览与服务状态

总览展示当前工作区可见服务，而不是用户个人收藏列表：

| 字段         | 应如何理解                                                                              |
| ------------ | --------------------------------------------------------------------------------------- |
| 监控服务     | 当前可见服务数量                                                                        |
| 活跃事件     | 按后端事件阶段汇总的未结束事件，包含上游长期未关闭的记录                                |
| 受影响服务   | 组件或事件反映降级、中断、维护的服务                                                    |
| 数据过期     | 后端判定采集健康异常，或资源超过计划期限及执行宽限；等待首次采集/旧检查点缺信息另行显示 |
| 实时连接正常 | 浏览器与事件流连接正常，不代表所有服务业务正常                                          |

使用服务名称搜索、状态筛选和卡片/列表切换。点击服务详情或活跃事件入口，查看更细的状态和关联事件。

“运行正常”与“采集正常”是不同维度：前者来自服务状态，后者表示采集链路工作情况。HTTP 200 不等于服务没有故障。资源新鲜度期限为上次成功检查点排定的下次时间，加 2 分钟执行宽限；失败重试不会延长期限。304 也会更新成功检查点，但不证明上游缓存以外没有新变化。

进入 **数据源 → 采集计划**，点击对应状态，查看调度阶段、最近尝试、最近成功、来源下次采集、连续失败次数和每个资源的计划/期限。资源计划来自最近成功检查点；退避时实际重试不会早于来源的下次采集时间。页面显示持久化的最近失败分类和排查提示；旧记录缺少分类时不会猜测原因。可点击刷新重新查询。

升级前的资源检查点会在各资源下次成功采集后自动补齐，期间显示“等待资源检查点”；无成功记录不代表服务故障。稳定期通常 4–5 分钟、活跃期 60–90 秒，维护资源 5–15 分钟；缓存/退避可能延长实际等待。

页面反映官方来源的公开信息，不是从你的网络主动探测该服务所有业务。来源刚刷新，也可能返回很久以前尚未关闭的事件。

## 添加一个状态页

进入 **数据源 → 添加数据源**：

1. 在“服务名称”中搜索服务名、别名或状态页域名，例如 OpenAI 或 Claude。选择候选项后自动填写状态页地址，也可用方向键和 Enter 选择。未收录的服务直接输入名称，再手动填写完整 HTTPS 地址。地址始终可以编辑；修改服务名称会清空之前的地址和检测结果。
2. 点击“检测状态页”，等待探测完成。
3. 查看识别服务/站点名称、规范化地址和可采集内容；需要时展开“技术详情”查看适配器。
4. 未接入的来源点击“确认接入”；已有来源会提示无需重复添加，点击“完成”。

不需要填写适配器名。[服务地址目录](service-catalog.md)提供快捷补全，实际支持情况以实时检测为准。已知官方别名会归一化；已有来源优先复用。无法匹配已知服务但能够适配的站点，使用输入的服务名称；未填写时使用主机名和路径。名称仅作为显示文本，不会据此合并服务或覆盖已有名称。同一托管平台的不同路径不会仅因域名相同被合并。

添加成功后返回数据源列表，等待 worker 首次采集，核对“采集健康”和“最近成功采集”。探测成功只说明当前支持的适配器能发现资源，不代表已经完成所有历史数据采集。

### 探测失败怎么办

先确认地址可公开访问、使用 HTTPS 且不包含账号密码。网络超时、需要登录、目标属于受限制网络、接口变化或尚无适配器都可能导致失败。不要通过反复选择错误服务来解决探测失败，也不要为了接入关闭 SSRF 防护。按[探测故障排查](troubleshooting.md#状态页检测失败)收集脱敏日志。

当前自动适配使用已有适配器和受控配置，不会自动生成任意站点的可靠解析器。HTML recipe 配置由平台维护，见 [适配器与渠道](../adapters-and-channels.md)。

### 共享来源与私有来源

bootstrap 导入的公开来源由平台维护；当前工作区通过页面新增的来源属于租户。用户无需选择或发布适配器，系统在接入时自动检测。数据源列表显示采集健康和计划；排障需要时，可在采集计划的“技术详情”中查看适配器名称和版本。

## 事件中心

使用服务、处理阶段筛选，点击事件标题打开详情。列表较长时点击“加载更多”；重置筛选恢复默认列表。

详情包含：服务、影响程度、当前阶段、服务更新时间、系统观察时间、时间线及原始记录。服务更新时间来自上游，系统时间是采集/观察记录，不应把最近一次观察时间当作事故开始时间。

时间线中的外语正文来自上游原文；如 Gatus 的 `Synthetic check failed` 是对应适配器根据探测结果生成的事件描述，不是网页报错。没有正文时会显示空状态，不补造官方说明。

首次采集会建立基线，已有事件可以显示在网页中，但不会为了补历史数据而批量发送旧事故通知。

## 配置通知渠道

进入 **通知渠道 → 添加渠道**，填写名称和类型。目前网页创建表单提供两种：

| 类型                   | 所需信息                                            |
| ---------------------- | --------------------------------------------------- |
| Slack Incoming Webhook | Slack 工作区生成的 Incoming Webhook HTTPS 地址      |
| Generic Webhook        | 接收端 HTTPS 地址、签名标识、与接收端约定的签名密钥 |

Generic Webhook 的签名密钥用于接收端校验通知，与服务端 `.env` 的配置加密密钥不是同一把密钥。签名约定见 [通知](../notifications.md)。

保存时会加密保存配置，之后不回显凭据；保存本身不会发送测试通知。

### 测试渠道

1. 在列表找到目标渠道，确认它指向预期接收方。
2. 点击“测试”。**这个操作会发送真实测试消息。**
3. 等待异步测试结束，查看成功/失败结果、HTTP 状态或错误摘要。
4. 到接收端核对消息内容。

测试成功说明此次接收端接受了请求，不证明通知规则一定会匹配，也不证明接收人已经阅读。测试失败不要立即重新创建多个相同渠道；先排查地址、凭据和网络。

后端还有其他渠道 driver，但当前网页没有为所有渠道提供创建表单。渠道修改、启停和凭据轮换也并非全部开放在网页中；高级操作参阅 [适配器与渠道](../adapters-and-channels.md)、[管理 API](../api-guide.md) 和 API 合同。

## 创建通知规则

进入 **通知规则 → 创建规则**：

1. 填写易识别的名称，决定是否启用。
2. 选择关注服务；留空表示所有可见服务，也包含以后新增的服务。
3. 设置最低影响级别。
4. 勾选事件类型；全部取消表示不限事件类型。
5. 选择一个或多个已配置渠道；也可在规则内“添加新渠道”，规则草稿会保留。
6. 检查右侧预览，点击“创建规则”。

推荐初次验证使用单一服务、所有影响级别、一个测试渠道，确认匹配行为后再扩大范围。

服务范围、事件类型和影响条件共同约束匹配。最低影响级别也作用于恢复事件：若恢复记录影响为“无影响”，较高阈值可能过滤这条恢复通知。需要接收恢复消息时，应检查这一设置。

新规则从后续事件开始匹配，不自动补发网页中的全部历史事件。仅创建规则不会保证立即出现投递记录。不要用导入或修改虚假事故来验证生产通知。

### 编辑和暂停

在规则列表打开编辑，修改后保存。启用开关用于暂停后续匹配，规则配置仍保留。已经生成或正在投递的任务，不应认为会随规则暂停被自动取消。

通过 API 建立的复杂组件/标签 scope，若无法在当前表单中无损表达，页面会限制编辑；关键词、静默时段等已有配置会按实现保留。请使用对应 API 管理复杂规则，避免把它简化成更宽范围的规则。

## 查看投递记录

进入 **投递记录**，按状态筛选并打开详情，查看渠道、规则、事件类型、尝试次数、HTTP 结果和错误摘要。

- 没有记录：先确认 worker 在运行，并且创建规则后确实发生了匹配的新事件。
- “渠道已接受”：接收服务接受了请求，不能等同于终端送达或已阅读。
- 等待重试：按调度退避重试，先检查失败原因，不要不断手动重复发送。
- 死信/可重试失败：修复原因后，在有权限时点击重试并确认。重试可能向原接收端再次发送消息。

只读用户不能发起重试。更大范围的 DLQ preview/replay 使用 [适配器与渠道](../adapters-and-channels.md)，先预览影响范围，再执行。

## 管理员设置

### 排查采集失败

在「数据源 → 采集计划」查看最近失败原因。该项只在连续失败次数大于零时显示；成功采集后自动清除，不代表服务自身发生故障。

| 分类                    | 操作建议                                                 |
| ----------------------- | -------------------------------------------------------- |
| 上游限流                | 等待计划重试，避免反复发起请求；结合下次采集时间检查退避 |
| 访问拒绝 / HTTP 错误    | 检查来源权限、状态页地址和公开端点是否仍可访问           |
| 上游服务错误            | 等待上游恢复，观察新鲜度期限                             |
| 超时 / 网络错误         | 检查 worker 所在网络的 DNS、TLS 证书、连通性和上游可用性 |
| 响应解析 / 不支持的来源 | 重新探测，核对适配器版本和上游结构变化                   |
| 取消 / 未分类           | 检查 worker 运行状态及采集日志                           |

历史失败不回填原因；无法可靠分类时显示「未分类错误」。多个资源同时失败时按固定优先级显示一个代表原因（限流、访问拒绝、服务错误、其他 HTTP 错误、超时、网络、解析、不支持、取消），完整详情查看日志。接口只返回分类代码，不回显原始错误或上游响应。


admin/owner 可查看审计记录。适配器升级、影子验证和回滚由平台维护者通过可信运维工具处理，不作为工作区用户的操作步骤，见 [高级运维](../advanced-operations.md)。

## 飞书群机器人通知 / Feishu notifications

在飞书群中添加自定义机器人，开启签名校验，复制 Webhook 地址和签名密钥。在 StatusHub 的“通知渠道 → 添加渠道”选择“飞书”，填写名称、HTTPS Webhook 地址和签名密钥。地址通常为 `https://open.feishu.cn/open-apis/bot/v2/hook/...`。飞书不需要通用 Webhook 的“签名密钥标识”。

保存后在渠道列表发送测试通知，确认飞书群收到消息，再在通知规则中选择该渠道。保存操作本身不发送消息。签名密钥加密存储，不会在普通响应中回显。若还配置了关键词或 IP 白名单，测试和正式消息都必须满足对应规则；签名校验失败时检查密钥和服务器时钟。

Choose **Feishu / Lark** under Notification channels, enter the custom bot webhook URL and its signing secret, then save and send a test notification. Enable signature verification in the bot security settings. Attach the channel to a notification rule to receive event notifications. The signing-key ID used by Generic Webhook is not required for Feishu.


## Delete configuration / 删除配置

The **Delete** action is available in notification channels, notification rules, and workspace data sources. A confirmation dialog describes the impact. Deletion hides the configuration and stops future collection or notification work; historical events, delivery records, and audit records remain available. Work already in progress may finish. There is no restore action; create a new configuration if needed.

Deleting a channel also removes it from linked rules. Rules with no remaining channels are disabled; other rules keep their remaining channels. Workspace users cannot delete platform shared data sources. Deletion uses the same role permissions as editing the corresponding resource.

在通知渠道、通知规则和工作区数据源列表中点击**删除**，确认后配置会从列表移除，停止后续采集或通知。历史事件、投递记录和审计记录保留；已开始执行的任务仍可能完成。目前不提供恢复入口，需要时可重新创建配置。

删除渠道会同时解除通知规则中的引用；没有剩余渠道的规则会自动停用，其他规则保留其剩余渠道。平台共享数据源由平台维护，工作区不能删除。删除权限与对应配置的编辑权限一致。

### 飞书消息内容 / Feishu message content

飞书通知使用富文本（post），分开显示标题、事件类型、组件状态变化、事件阶段、影响程度、最新非空进展、UTC 时间和原始事件链接。缺失字段不显示；组件变更会显示 `operational → degraded_performance` 等状态变化，不再出现空破折号。进展按上游更新时间选择，时间缺失时使用创建时间。

消息按 JSON 编码后的字节数限制在 20 KiB 内（渠道设置更小时使用更小限制）。超长正文截断；超出限制或服务器返回 HTTP 413 时降级为精简文本。上游文字作为文本节点发送，不解释为 @ 提及或消息结构。仅允许 HTTP/HTTPS 原始事件链接。无需修改已有飞书渠道的 Webhook 和签名密钥。

Feishu notifications use rich-text posts with a title, event type, component status transition, incident phase, impact, latest non-empty update, UTC timestamp and an original-event link. Missing fields are omitted. Payloads are bounded to 20 KiB of encoded JSON or the lower channel limit, with compact text fallback for oversized messages or HTTP 413. Upstream text is rendered as text nodes; links must use HTTP or HTTPS. Existing webhook and signing-secret settings remain valid.

Reference: [Feishu custom bot guide](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot?lang=zh-CN).

## SMTP 邮件通知 / SMTP email notifications

在「通知渠道 → 添加渠道」选择 **SMTP 邮件**。填写服务器及端口（如 `smtp.example.com:587`）、加密方式（587/2525 使用 STARTTLS，465 通常使用 TLS）、用户名、密码或邮箱服务商提供的授权码、发件邮箱和收件邮箱。发件人须获得 SMTP 服务商授权。允许无需身份认证的加密中继，此时用户名与密码留空。每个渠道配置一个收件邮箱，可使用团队邮件组；不同收件人可创建多个渠道。

保存后点击「发送测试」，再关联通知规则。HTML 邮件包含服务名称、状态变化、事件阶段、影响程度、最新进展、UTC 时间和原始链接，同时附带纯文本版本。上游内容进行 HTML 转义，链接仅允许 HTTP/HTTPS。SMTP 接受表示服务商接收，不保证进入收件箱；检查垃圾箱及 SPF、DKIM、DMARC 配置。临时错误按现有投递策略重试，SMTP 5xx 为永久失败，错误记录不包含密码和服务器返回的原文。

配置独立于账号邀请/密码恢复 SMTP，完整配置加密存储。要求 TLS 1.2 及以上并验证服务器证书，不支持明文连接。网页渠道仅能连接公网 SMTP 地址，沿用出站地址过滤和 DNS 固定策略；不允许访问本机、内网或云元数据地址。

Choose **SMTP email** in Notification channels. Enter the SMTP server and port, TLS (usually 465) or STARTTLS (587/2525), username, password/app password, authorized sender and one recipient (a mailing list is supported). Anonymous encrypted relays may omit credentials. Save, send a test, then attach notification rules. Emails include HTML and plain-text alternatives. SMTP acceptance does not guarantee inbox delivery; configure SPF/DKIM/DMARC and check spam folders. Temporary errors are retried; SMTP 5xx failures are permanent. This encrypted channel configuration is separate from identity email settings. Only public SMTP destinations with verified TLS are supported.
