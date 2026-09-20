import i18n, { type TOptions } from "i18next";
import { initReactI18next } from "react-i18next";

export type AppLanguage = "en" | "zh";
export type LanguagePreference = AppLanguage | "system";

/** Choose the first supported language in the browser's preference order. */
export function resolveLanguage(
  preference: string | null,
  browserLanguages: readonly string[],
): AppLanguage {
  if (preference === "zh" || preference === "en") return preference;
  if (!preference) return "en";
  for (const locale of browserLanguages) {
    const language = locale.trim().toLowerCase().split(/[-_]/)[0];
    if (language === "zh" || language === "en") return language;
  }
  return "en";
}

const english: Record<string, string> = {
  实例管理: "Platform administration",
  平台总览: "Platform overview",
  用户管理: "Users",
  工作区管理: "Workspaces",
  平台审计: "Platform audit",
  用户总数: "Total users",
  启用用户: "Enabled users",
  工作区: "Workspaces",
  平台数据源: "Platform sources",
  管理边界: "Administration boundary",
  "查看 StatusHub 实例的账号、工作区与共享资源规模。":
    "Review accounts, workspaces, and shared resources across this StatusHub instance.",
  "平台管理员管理整个实例；工作区管理员只管理自己的成员与业务配置。两种权限相互独立。":
    "Platform administrators manage the instance. Workspace administrators manage only their own members and business configuration. These permissions are independent.",
  "管理实例账号状态并撤销浏览器会话。工作区角色仍由各工作区管理员维护。":
    "Manage instance account status and revoke browser sessions. Workspace roles remain managed by each workspace administrator.",
  "管理实例账号、平台权限和浏览器会话。工作区角色仍由各工作区管理员维护。":
    "Manage instance accounts, platform access, and browser sessions. Workspace roles remain managed by each workspace administrator.",
  搜索邮箱或名称: "Search email or name",
  搜索: "Search",
  暂无用户: "No users",
  用户: "User",
  权限与范围: "Access and scope",
  平台管理员: "Platform administrator",
  "{{count}} 个工作区": "{{count}} workspaces",
  "{{count}} 个活跃会话": "{{count}} active sessions",
  "{{count}} 名成员": "{{count}} members",
  "{{count}} 个数据源": "{{count}} sources",
  活跃会话: "Active sessions",
  最近活动: "Last activity",
  停用: "Disable",
  撤销会话: "Revoke sessions",
  授予平台管理员: "Grant platform administrator",
  撤销平台权限: "Revoke platform access",
  撤销全部会话: "Revoke all sessions",
  停用用户: "Disable user",
  恢复用户: "Restore user",
  "该用户需要重新登录所有浏览器。": "The user must sign in again on every browser.",
  "该用户将无法登录，现有会话也会立即失效。":
    "The user will be unable to sign in and all current sessions will be revoked immediately.",
  "该用户将可以重新登录并访问仍然有效的工作区。":
    "The user can sign in again and access their enabled workspaces.",
  "该用户将失去整个实例的管理权限，工作区角色不受影响。":
    "The user will lose instance-wide administration access. Workspace roles are unchanged.",
  "该用户将可以管理整个实例的用户、工作区和平台审计记录。":
    "The user will be able to manage instance users, workspaces, and platform audit records.",
  "查看每个工作区的管理员、成员数量和已关联数据源。":
    "Review each workspace administrator, member count, and attached sources.",
  "查看工作区规模，或将已有用户指定为新工作区管理员。":
    "Review workspace scale or assign an existing user to administer a new workspace.",
  创建工作区: "Create workspace",
  创建: "Create",
  管理员邮箱: "Administrator email",
  "例如：基础设施团队": "For example: Infrastructure team",
  "管理员必须是当前实例中已启用的用户；此操作不会创建账号或密码。":
    "The administrator must be an enabled user in this instance. This action does not create an account or password.",
  "查看平台管理员对用户、权限和工作区执行的写操作。":
    "Review changes platform administrators made to users, access, and workspaces.",
  "搜索操作者、动作或资源": "Search actor, action, or resource",
  暂无平台审计记录: "No platform audit events",
  时间: "Time",
  动作: "Action",
  资源: "Resource",
  系统或命令行: "System or CLI",
  "搜索工作区、标识或管理员": "Search workspace, slug, or administrator",
  暂无工作区: "No workspaces",
  创建时间: "Created",
  进入工作区: "Open workspace",
  返回工作台: "Back to console",
  暂无: "None",
  无法访问平台管理后台: "Platform administration unavailable",
  "当前账号不是平台管理员。请由可信运维使用 CLI 授予平台权限。":
    "This account is not a platform administrator. Ask a trusted operator to grant platform access with the CLI.",
  "当前账号没有平台管理权限。": "This account does not have platform administration access.",
  "必须保留至少一名有效的平台管理员。": "At least one active platform administrator is required.",
  "平台管理员不能停用自己的账号。": "Platform administrators cannot disable their own account.",
  "平台管理员不能撤销自己的平台权限。": "Platform administrators cannot revoke their own platform access.",
  "请先转移该用户负责的工作区管理员，再停用账号。":
    "Transfer the workspaces administered by this user before disabling the account.",
  "正在加载平台管理后台…": "Loading platform administration…",
  编辑通知渠道: "Edit notification channel",
  留空以保留现有凭据: "Leave blank to keep the current credential",
  "通知渠道已归档，历史投递记录仍然保留。":
    "The notification channel was archived. Delivery history is preserved.",
  "通知渠道已恢复为停用状态，请检查凭据后再启用。":
    "The notification channel was restored as disabled. Check its credentials before enabling it.",
  使用中: "Active",
  已归档: "Archived",
  恢复: "Restore",
  归档: "Archive",
  更多: "More",
  "已更新「{{value0}}」。": "Updated “{{value0}}”.",
  包含已归档服务: "Include archived services",
  "选择当前工作区需要监控的服务。共享采集由 StatusHub 维护，你可以独立启停或移除工作区关联。":
    "Choose the services monitored in this workspace. StatusHub maintains shared collection while you control workspace access independently.",
  监控中: "Monitoring",
  托管服务目录: "Managed service catalog",
  "这里列出 StatusHub 已经持续采集的共享服务。添加后只会关联到当前工作区，不会创建重复采集器。":
    "This catalog lists shared services already collected by StatusHub. Adding one only links it to this workspace and does not create another collector.",
  没有更多托管服务: "No additional managed services",
  "仍可输入任意状态页地址进行实时检测。":
    "You can still enter any status-page URL for live detection.",
  已添加: "Added",
  添加: "Add",
  来源: "Source type",
  "StatusHub 托管": "Managed by StatusHub",
  工作区自建: "Workspace managed",
  编辑名称: "Edit name",
  暂停监控: "Pause monitoring",
  启用监控: "Enable monitoring",
  替换状态页: "Replace status page",
  "新地址检测成功后才会切换。旧数据源会归档，事件历史不会删除。":
    "The source changes only after the new address passes detection. The old source is archived and its incident history is preserved.",
  "已找到 StatusHub 托管采集器，确认后会添加到当前工作区。":
    "A StatusHub-managed collector was found. Confirm to add it to this workspace.",
  确认替换: "Confirm replacement",
  添加到工作区: "Add to workspace",
  编辑数据源名称: "Edit source name",
  归档数据源: "Archive source",
  "该服务会从当前工作区移除并停止后续通知，历史事件与投递记录仍会保留。相关通知规则可能被自动暂停。":
    "This service will be removed from the workspace and stop generating notifications. Incident and delivery history is preserved. Related rules may be paused.",
  管理数据源: "Manage source",
  只读用户: "Viewer",
  "工作区始终只有一名管理员。管理员可以管理操作员和只读用户；如需更换管理员，请使用转移管理员。":
    "Each workspace has exactly one administrator. The administrator manages operators and viewers; use administrator transfer to appoint a replacement.",
  新管理员: "New administrator",
  转移管理员: "Transfer administrator",
  source_archived: "Source archived",
  channel_archived: "Notification channel archived",
  数据源已归档: "Source archived",
  通知渠道已归档: "Notification channel archived",
  复制: "Copy",
  查看官方状态页: "View official status page",
  "SMTP 邮件": "SMTP email",
  "SMTP 服务器及端口": "SMTP server and port",
  连接加密: "Connection encryption",
  "SMTP 用户名": "SMTP username",
  "SMTP 密码或授权码": "SMTP password or app password",
  发件邮箱: "Sender email",
  收件邮箱: "Recipient email",
  "请填写 SMTP 服务器及端口，例如 smtp.example.com:587。":
    "Enter an SMTP server and port, such as smtp.example.com:587.",
  "请输入有效的邮箱地址。": "Enter a valid email address.",
  "请填写 SMTP 密码或授权码。": "Enter the SMTP password or app password.",
  "使用加密 SMTP 连接发送 HTML 邮件及纯文本副本。每个渠道配置一个收件邮箱，可使用团队邮件组。与账号邀请邮件配置独立。":
    "Send HTML email with a plain-text alternative over encrypted SMTP. Configure one recipient or a team mailing list per channel. This is separate from account invitation email.",
  切换工作区: "Switch workspace",
  "正在加载账号…": "Loading account\u2026",
  "此链接属于旧版登录系统，请重新申请邀请或密码重置。":
    "This link belongs to the old login system. Request a new invitation or password reset.",
  "邮箱或密码不正确。": "Email or password is incorrect.",
  "服务公开地址与当前页面不一致，请联系管理员。":
    "The service public URL does not match this page. Contact your administrator.",
  "会话已过期，请重新登录。": "Your session has expired. Sign in again.",
  "你没有此工作区的访问权限。": "You do not have access to this workspace.",
  "邀请已过期、撤销或接受，请联系管理员重新邀请。":
    "This invitation has expired, been revoked or already accepted. Ask your administrator for a new invitation.",
  "初始化链接已失效，或实例已经初始化。":
    "The setup link has expired or this instance is already initialized.",
  "密码必须包含 12–128 个字符。":
    "Password must contain 12\u2013128 characters.",
  "请先使用受邀邮箱登录，再接受邀请。":
    "Sign in with the invited email before accepting.",
  "当前登录邮箱与邀请邮箱不一致。":
    "Your signed-in email does not match the invitation.",
  "成员关系已存在，请联系管理员调整权限。":
    "Membership already exists. Contact your administrator to change access.",
  "链接已过期或已使用，请重新申请。":
    "This link has expired or already been used. Request a new one.",
  "页面凭据已过期，请刷新后重试。": "Refresh this page and try again.",
  "邮件服务未配置或不可用，请联系管理员。":
    "Email service is unavailable or not configured. Contact your administrator.",
  "认证服务暂时不可用，请稍后重试。":
    "Authentication is temporarily unavailable. Try again later.",
  请求失败: "Request failed",
  登录: "Sign in",
  "初始化 StatusHub": "Set up StatusHub",
  工作区名称: "Workspace name",
  创建管理员与工作区: "Create administrator and workspace",
  找回密码: "Forgot password",
  重置密码: "Reset password",
  "密码已重置，请使用新密码登录。":
    "Password reset complete. Sign in with your new password.",
  加入工作区: "Join workspace",
  接受邀请: "Accept invitation",
  选择工作区: "Choose workspace",
  "暂无可访问工作区，请联系管理员邀请。":
    "No workspaces available. Ask an administrator to invite you.",
  返回工作区: "Back to workspaces",
  验证邮箱: "Verify email",
  "邮箱已验证。": "Email verified.",
  "操作已完成，但令牌无法再次显示，请重新轮换。":
    "The operation completed but the token can no longer be retrieved. Rotate it again.",
  等待发送: "Queued",
  已提交邮件服务器: "Submitted to SMTP",
  发送失败: "Delivery failed",
  重新发送: "Resend",
  "令牌仅在本次结果中显示，请安全保存。":
    "The token is only shown in this result. Store it securely.",
  跟随浏览器: "Browser language",
  "登录 StatusHub": "Sign in to StatusHub",
  "登录工作区，掌握服务状态与重要通知。":
    "Monitor service status and important updates in your workspace.",
  搜索已加载的规则: "Search loaded rules",
  "当前仅显示已加载的规则，可在下方继续加载。":
    "Showing loaded rules. Load more below to include additional rules.",
  没有匹配的规则: "No matching rules",
  "尝试其他名称，或清空搜索条件。": "Try another name or clear the search.",
  采集频率说明: "Collection frequency",
  删除: "Delete",
  删除配置: "Delete configuration",
  确认删除: "Confirm deletion",
  "确认删除“{{name}}”？配置将从列表移除，历史事件和投递记录保留。已开始执行的任务可能仍会完成。":
    "Delete “{{name}}”? This removes the configuration from the list and preserves historical events and deliveries. Tasks already in progress may still finish.",
  "该渠道将从通知规则中移除；失去全部接收渠道的规则会自动停用。":
    "This channel will be removed from notification rules. Rules with no remaining channels will be disabled.",
  飞书: "Feishu / Lark",
  "请填写飞书机器人的签名密钥。": "Enter the bot signing secret.",
  "使用飞书群自定义机器人的 Webhook 地址，并开启签名校验。签名密钥填写机器人安全设置中的密钥。保存后可发送测试通知。":
    "Use a Feishu group custom bot webhook with signature verification enabled. Enter the secret from the bot security settings. You can send a test notification after saving.",
  服务名称: "Service name",
  搜索或输入服务名称: "Search or enter a service name",
  "选择服务可自动填写状态页；未收录的服务请自行填写地址。接入前仍会检测是否支持。":
    "Choose a service to fill its status page. For an unlisted service, enter the URL yourself. Compatibility is checked before connecting.",
  "未找到服务，请在下方填写状态页地址。":
    "No matching service. Enter its status page URL below.",
  保存: "Save",
  确认: "Confirm",
  关闭: "Close",
  服务账号: "Service accounts",
  邀请: "Invitations",
  成员: "Members",
  个人账号: "My account",
  忘记密码: "Forgot password",
  邮箱密码登录: "Sign in with email",
  返回登录: "Back to sign in",
  发送重置邮件: "Send reset email",
  设置密码: "Set password",
  登录并接受邀请: "Sign in and accept invitation",
  发送验证邮件: "Send verification email",
  密码: "Password",
  "新成员先验证邮箱；已有邮箱密码账号可输入密码接受邀请。":
    "New members must verify their email. Existing password accounts can sign in below to accept.",
  账号与邀请: "Account and invitation",
  "操作完成，请登录工作区。": "Completed. Sign in to your workspace.",
  "若该邮箱符合条件，系统将发送邮件，请检查收件箱。":
    "If this email is eligible, a message will be sent. Check your inbox.",
  "操作未完成，请检查凭据、邀请有效期和工作区。":
    "Unable to complete the action. Check your credentials, invitation validity and workspace.",
  "请求过于频繁，请稍后重试。": "Too many attempts. Try again later.",
  "操作完成后需要重新登录。":
    "You will need to sign in again after this operation.",
  退出全部会话: "Sign out all sessions",
  修改密码: "Change password",
  "新密码（12–128 个字符）": "New password (12–128 characters)",
  原密码: "Current password",
  "服务账号请通过服务账号管理轮换令牌。":
    "Use service account management to rotate this account’s token.",
  "操作已完成，但凭据已无法再次显示。请重新轮换或生成邀请。":
    "The operation completed, but the credential can no longer be displayed. Rotate again or generate a new invitation.",
  凭据或邀请链接: "Credential or invitation link",
  "此内容仅在本次结果中显示，请安全保存。邀请链接需要由你分享给受邀者。":
    "Save this result securely. Share invitation links directly with the intended recipient. Existing tokens cannot be retrieved later.",
  请立即保存: "Save this now",
  "权限变更和停用会影响后续访问；轮换令牌会立即使旧令牌失效。请确认目标与角色。":
    "Role changes and disabling affect subsequent access. Rotation immediately invalidates the previous token. Check the target and role before confirming.",
  确认账号操作: "Confirm account action",
  账号配置: "Account settings",
  轮换令牌: "Rotate token",
  撤销邀请: "Revoke invitation",
  邮箱密码: "Email and password",
  最近使用: "Last used",
  登录方式: "Sign-in method",
  有效期至: "Expires at",
  等待接受: "Pending",
  已过期: "Expired",
  已撤销: "Revoked",
  已接受: "Accepted",
  名称: "Name",
  创建服务账号: "Create service account",
  创建邀请: "Create invitation",
  邮箱: "Email",
  角色: "Role",
  查看者: "Viewer",
  最近失败原因: "Latest failure reason",
  "上游限流，请等待退避后重试":
    "Upstream rate limit; wait for the scheduled retry",
  "上游拒绝访问，请检查来源权限":
    "Upstream access denied; check source permissions",
  "上游服务错误，请等待恢复": "Upstream server error; wait for recovery",
  "上游 HTTP 错误，请检查状态页地址":
    "Upstream HTTP error; check the status-page URL",
  "请求超时，请检查网络和上游可用性":
    "Request timed out; check network and upstream availability",
  "网络连接异常，请检查 DNS、证书和连通性":
    "Network error; check DNS, certificates and connectivity",
  "响应解析异常，请检查适配器兼容性":
    "Invalid response; check adapter compatibility",
  "来源或操作不受支持，请重新探测适配器":
    "Unsupported source or operation; probe the adapter again",
  "采集被取消，请检查 worker 运行状态":
    "Collection cancelled; check worker health",
  "未分类错误，请查看采集日志": "Unclassified error; consult collector logs",
  按计划采集: "On schedule",
  采集已逾期: "Collection overdue",
  等待资源检查点: "Awaiting resource checkpoint",
  没有启用的数据源: "No enabled sources",
  等待首次成功采集: "Awaiting first successful collection",
  部分资源超过新鲜度期限: "Some resources passed their freshness deadline",
  "失败后退避，具体原因请查采集日志":
    "Backing off after a failed attempt; consult collector logs for the cause",
  采集健康异常: "Collection health is degraded",
  事件活跃期: "Active incident",
  近期变化期: "Recent change",
  观察期: "Warm",
  稳定期: "Stable",
  失败退避: "Failure backoff",
  常规调度: "Regular cadence",
  上游缓存策略: "Upstream cache policy",
  上游要求延后: "Upstream retry delay",
  新鲜度期限: "Freshness deadline",
  采集计划: "Collection schedule",
  下次采集: "Next collection",
  调度阶段: "Scheduling phase",
  最近尝试: "Last attempt",
  连续失败次数: "Consecutive failures",
  资源计划时间: "Resource scheduled time",
  调度依据: "Scheduling basis",
  "采集按资源独立调度。稳定期通常为 4–5 分钟，事件活跃期为 60–90 秒；维护资源为 5–15 分钟，上游缓存和退避可能延长等待。发现新事件还需叠加上游发布与缓存时间。查看采集计划了解实际安排。":
    "Resources are scheduled independently: typically 4–5 minutes when stable, 60–90 seconds for active incidents, and 5–15 minutes for maintenance. Upstream cache policies and backoff can extend the wait. Detection also depends on upstream publishing and caching. Open the collection schedule for actual timings.",
  "新鲜度期限是资源成功采集后计划的下次时间，加上 2 分钟执行宽限。失败重试不会延长此期限。旧检查点会在各资源下次成功采集后补齐；尚无记录不代表服务故障。":
    "The freshness deadline is the next time scheduled after a successful resource check plus a 2-minute execution allowance. Failed retries do not extend it. Older checkpoints gain this information after each resource succeeds again; missing records do not indicate a vendor outage.",
  "资源计划时间属于最近成功检查点；失败退避时实际重试不会早于来源的下次采集时间。":
    "Resource times reflect the last successful checkpoint. During failure backoff, retries will not start before the source's next collection time.",
  "Acme 工作区": "Acme workspace",
  "HTTPS 地址": "HTTPS URL",
  "StatusHub · 服务公开状态与采集健康分开呈现":
    "StatusHub · Vendor status and collection health, clearly separated",
  "Webhook 需要签名标识与签名密钥。":
    "A webhook requires a signing key ID and secret.",
  不一致率: "Mismatch rate",
  严重: "Critical",
  严重中断: "Major outage",
  个: "",
  个数据源: " sources",
  个活跃事件: " active incidents",
  事件: "Incident",
  事件中心: "Incidents",
  事件创建: "Incident created",
  事件恢复: "Incident resolved",
  事件时间线: "Incident timeline",
  事件更新: "Incident updated",
  事件流已断开: "The event stream disconnected",
  事件流消息过大: "The event stream message is too large",
  事件类型: "Event type",
  事件记录: "Incident history",
  事件详情: "Incident details",
  "从云基础设施到 AI 与团队工具，统一追踪状态、事件和通知。":
    "Track status, incidents, and notifications across cloud infrastructure, AI, and team tools.",
  你的配置将如何工作: "How your configuration will work",
  使用服务账号登录: "Sign in with a service account",
  "例如 local": "For example, local",
  "例如 生产告警": "For example, Production alerts",
  "例如 生产环境 · AI 服务告警": "For example, Production · AI service alerts",
  保存为停用状态: "Save as disabled",
  保存修改: "Save changes",
  保存后开始匹配新事件: "Start matching new events after saving",
  保存渠道: "Save channel",
  "候选 p95": "Candidate p95",
  候选适配器名称: "Candidate adapter name",
  "候选适配器在后台采样。晋级前必须通过全部质量门禁。":
    "The candidate adapter samples in the background and must pass every quality gate before promotion.",
  候选适配器版本: "Candidate adapter version",
  健康状态: "Health",
  全部: "All",
  全部服务: "All vendors",
  "全部取消表示不限事件类型。": "Clear all to include every event type.",
  全部可见服务: "All visible vendors",
  全部投递状态: "All delivery states",
  全部状态: "All states",
  全部阶段: "All phases",
  关注服务: "Vendors to monitor",
  关注这些服务: "Monitor these vendors",
  "关闭后保留配置，暂停匹配新的事件。":
    "Turn this off to keep the configuration while pausing new event matches.",
  关闭导航: "Close navigation",
  内部事件中心: "Internal incident center",
  凭据: "Credentials",
  "凭据加密保存，保存后不会再次显示。保存不会发送通知，可在渠道列表发起测试。":
    "Credentials are encrypted and cannot be shown again. Saving does not send a notification; run a test from the channel list.",
  切换导航: "Toggle navigation",
  列表: "List",
  刚刚: "Just now",
  创建你的第一条通知规则: "Create your first notification rule",
  创建规则: "Create rule",
  创建通知规则: "Create notification rule",
  刷新: "Refresh",
  刷新统计: "Refresh statistics",
  刷新页面: "Reload page",
  加入重试队列: "Queue retry",
  加载更多: "Load more",
  "区域网络连接已恢复（演示事件）":
    "Regional network connectivity restored (demo incident)",
  卡片: "Cards",
  服务: "Vendor",
  "服务事件更新会出现在这里。": "Vendor incident updates will appear here.",
  服务尚未结束的事件: "Unresolved vendor incidents",
  服务更新时间: "Vendor updated",
  厂商状态: "Vendor status",
  服务状态工作台: "Vendor status console",
  服务状态筛选: "Filter vendor status",
  服务详情: "Vendor details",
  历史记录: "History",
  原始事件数据: "Raw incident data",
  发生这些变化: "When these changes occur",
  发送中: "Sending",
  发送测试: "Send test",
  取消: "Cancel",
  受影响: "Impacted",
  受影响服务: "Impacted vendors",
  只接收与你的依赖有关的变化:
    "Receive only changes related to your dependencies",
  只读成员: "Viewer",
  "可采集：": "Collects:",
  同一规则可以发送到多个接收渠道: "A rule can send to multiple channels",
  启动影子运行: "Start shadow run",
  启用状态: "Enabled",
  启用规则: "Enable rule",
  停用规则: "Disable rule",
  响应没有事件流: "The response has no event stream",
  回滚: "Roll back",
  "基于通用状态页适配器，连接广泛的服务生态":
    "Connect a broad service ecosystem with universal status-page adapters",
  基本信息: "Basic information",
  基础设施重大事件: "Major infrastructure incidents",
  填写变更依据或关联事件: "Describe the reason or linked incident",
  处理阶段: "Phase",
  外观主题: "Theme",
  外部依赖状态监控: "External dependency monitoring",
  失败: "Failed",
  完成: "Done",
  实时连接正常: "Live connection active",
  实时连接认证失败: "Live connection authentication failed",
  审计记录: "Audit log",
  "将服务、事件条件和接收渠道组合成清晰的通知规则。":
    "Combine vendors, event conditions, and channels into clear notification rules.",
  尚无成功采集: "No successful collection yet",
  尚未启动适配器发布: "No adapter rollout started",
  尚未开始投递: "Delivery has not started",
  尚未选择渠道: "No channel selected",
  尝试次数: "Attempts",
  尝试记录: "Attempts",
  工作区标识: "Workspace",
  "工作区由管理员创建，使用已分配的服务账号令牌登录。":
    "Workspaces are created by an administrator. Sign in with your assigned service-account token.",
  工作台: "Console",
  "工作进程处理后会显示尝试记录。":
    "Attempts will appear after the worker processes the delivery.",
  已停止重试: "Retries stopped",
  已停用: "Disabled",
  "已加密 · v": "Encrypted · v",
  已启用: "Enabled",
  已回滚: "Rolled back",
  已完成: "Completed",
  已定位: "Identified",
  已恢复: "Resolved",
  "已显示当前事件状态，后续更新将自动同步。":
    "The current incident state is shown. Future updates will sync automatically.",
  已晋级: "Promoted",
  已送达: "Delivered",
  "已重新加入投递队列。": "The delivery has been queued again.",
  平台管理: "Platform managed",
  序号: "Sequence",
  开始建立你的监控视图: "Build your monitoring view",
  "异常优先展示 · 最近成功采集时间":
    "Issues first · Last successful collection",
  当前工作区可见的服务: "Vendors visible to this workspace",
  "当前没有活跃事件，部分采集数据需要检查":
    "No active incidents; some collection data needs attention",
  当前监控范围内没有活跃事件: "No active incidents in the current scope",
  "当前角色无权执行此操作。": "Your current role cannot perform this action.",
  影响级别: "Impact",
  影子运行: "Shadow",
  待处理: "Pending",
  性能降级: "Degraded performance",
  总览: "Overview",
  成功: "Succeeded",
  所有事件类型: "All event types",
  所有影响级别: "All impact levels",
  执行中: "Running",
  找不到这个页面: "Page not found",
  技术详情: "Technical details",
  投递状态: "Delivery state",
  投递状态筛选: "Filter delivery state",
  投递记录: "Deliveries",
  投递详情: "Delivery details",
  "掌握外部依赖的最新状态，让重要变化一目了然。":
    "See the latest state of every external dependency and spot important changes at a glance.",
  排队中: "Queued",
  接收渠道: "Channel",
  搜索服务: "Search vendors",
  搜索服务名称: "Search vendor name",
  操作: "Actions",
  操作原因: "Reason",
  操作员: "Operator",
  操作时间: "Time",
  操作者: "Actor",
  "数千服务的状态，": "The status of thousands of services,",
  数据加载失败: "Failed to load data",
  数据源: "Sources",
  数据过期: "Stale data",
  "数据过期 · ": "Stale · ",
  无影响: "None",
  "无法加载服务或渠道选项，请重试后保存。":
    "Could not load vendor or channel options. Retry before saving.",
  时间未知: "Unknown time",
  暂无事件: "No incidents",
  暂无匹配事件: "No matching incidents",
  暂无审计记录: "No audit events",
  暂无投递记录: "No deliveries",
  暂无接收端响应: "No receiver response",
  暂无数据源: "No sources",
  暂无更新正文: "No update text",
  暂无监控服务: "No monitored vendors",
  暂无记录: "No records",
  更新时间: "Updated",
  最低影响: "Minimum impact",
  "最低影响条件同样用于恢复事件。恢复更新若被标记为无影响，可能不会发送；需要完整恢复通知时请选择所有影响级别。":
    "The minimum-impact condition also applies to recovery events. A recovery marked with no impact may be filtered; choose all impact levels to receive every recovery.",
  最低影响级别: "Minimum impact",
  最低样本数: "Minimum samples",
  "最大不一致率（0–1）": "Maximum mismatch rate (0–1)",
  "最大错误率（0–1）": "Maximum error rate (0–1)",
  最近事件: "Recent incidents",
  "最近决策：": "Latest decision:",
  最近成功采集: "Last successful collection",
  "有新的状态变化时会显示在这里。": "New status changes will appear here.",
  服务状态: "Service status",
  服务状态变化: "Service status changed",
  服务生态展示: "Service ecosystem",
  "服务生态展示 · 实际接入以状态页检测结果为准":
    "Service ecosystem shown · Actual compatibility depends on status-page detection",
  服务组件: "Components",
  服务账号令牌: "Service-account token",
  未知: "Unknown",
  未结束事件: "Unresolved incidents",
  查看: "View",
  查看事件: "View incidents",
  查看关联事件: "View related incidents",
  查看发布: "View rollout",
  检查数据源: "Check sources",
  "检查通知是否成功，了解每一次尝试和失败原因。":
    "Verify notification delivery and inspect every attempt and failure.",
  "检查采集健康，管理租户私有状态页。":
    "Check collection health and manage workspace sources.",
  "检测成功，可以接入。":
    "Detection succeeded. This status page can be connected.",
  检测状态页: "Detect status page",
  "正在加载工作台…": "Loading console…",
  正在连接: "Connecting",
  "正在连接工作区…": "Connecting to workspace…",
  正在重连: "Reconnecting",
  正常: "Healthy",
  正常且数据新鲜: "Healthy with fresh data",
  "此操作会重新向原渠道投递通知。若接收端此前已收到但未确认，可能收到重复通知。":
    "This sends the notification to the original channel again. If the receiver got it without acknowledging it, a duplicate may be delivered.",
  "此操作未包含在设计演示中。":
    "This action is unavailable in the design demo.",
  "此规则包含精细服务范围或非组合条件，本编辑器暂不支持修改，以免扩大通知范围。请通过 API 管理。":
    "This rule contains a granular service scope or non-combinable conditions. Edit it through the API to avoid widening its notification scope.",
  "此规则已有的关键词和静默时段配置会保留。":
    "Existing keyword and quiet-hour settings will be preserved.",
  "汇聚于此。": "unified here.",
  没有匹配的服务: "No matching vendors",
  活跃事件: "Active incidents",
  浅色模式: "Light",
  "测试任务已入队，正在等待通知工作进程处理。":
    "The test is queued and waiting for the notification worker.",
  深色模式: "Dark",
  "添加 Slack 或 Webhook，开始接收服务状态变化。":
    "Add Slack or a webhook to start receiving vendor status changes.",
  添加数据源: "Add source",
  添加新渠道: "Add new channel",
  添加渠道: "Add channel",
  添加第一个渠道: "Add your first channel",
  添加通知渠道: "Add notification channel",
  "渠道信息暂时不可用。": "Channel information is temporarily unavailable.",
  渠道名称: "Channel name",
  渠道已接受: "Accepted by channel",
  渠道类型: "Channel type",
  状态: "Status",
  "状态来自服务官方页面，实际业务可用性请结合自身监控判断。":
    "Status comes from official vendor pages. Use your own monitoring to assess actual availability.",
  状态概览: "Status summary",
  状态页: "Status page",
  状态页地址: "Status-page URL",
  生产告警: "Production alerts",
  "生产环境 · AI 服务": "Production · AI services",
  "留空表示当前工作区可见的全部服务，也包含后续新增服务。":
    "Leave blank to include every vendor visible to this workspace, including vendors added later.",
  登录工作区: "Sign in to workspace",
  "登录已过期或凭据无效，请重新登录。":
    "Your session expired or credentials are invalid. Sign in again.",
  监控服务: "Monitored vendors",
  监控总览: "Monitoring overview",
  监控范围与条件: "Scope and conditions",
  确认接入: "Connect source",
  示例接收端暂时不可用: "Demo receiver temporarily unavailable",
  示例接收端返回服务不可用: "Demo receiver returned service unavailable",
  "示例更新：已定位到受影响的服务，正在进行恢复。":
    "Demo update: The affected service has been identified and recovery is underway.",
  "示例更新：正在调查部分请求延迟升高的问题。":
    "Demo update: Investigating elevated latency for some requests.",
  "示例更新：缓解措施已生效，正在持续观察请求延迟。此内容为设计演示，不代表服务真实状态。":
    "Demo update: Mitigation is effective and request latency is being monitored. This is demo content, not a real vendor status.",
  站点名称: "Site name",
  等待重试: "Waiting to retry",
  筛选事件阶段: "Filter incident phase",
  筛选服务: "Filter vendor",
  签名密钥: "Signing secret",
  签名密钥标识: "Signing key ID",
  管理员: "Administrator",
  "管理员可启动候选适配器的影子运行。":
    "Administrators can start a shadow run for a candidate adapter.",
  "管理数据接入和高级运行配置，查看工作区审计记录。":
    "Manage data sources and advanced operations, and review workspace audit events.",
  管理通知渠道: "Manage notification channels",
  类型: "Type",
  "粘贴服务状态页地址，我们会自动检查是否支持接入。":
    "Paste a vendor status-page URL and we will detect whether it can be connected.",
  系统发现时间: "Discovered by system",
  系统更新时间: "System updated",
  "系统采集：": "Collected by system:",
  给规则一个容易辨认的名称: "Give the rule an easy-to-recognize name",
  "统一监控，及时响应": "Unified monitoring, timely response",
  维护中: "Maintenance",
  编辑: "Edit",
  编辑通知规则: "Edit notification rule",
  观察中: "Monitoring",
  "规则匹配到事件后，通知投递过程会显示在这里。":
    "Notification deliveries will appear here after a rule matches an event.",
  规则名称: "Rule name",
  规则预览: "Rule preview",
  计划维护: "Scheduled maintenance",
  让重要变化及时到达: "Deliver important changes on time",
  设置: "Settings",
  设计演示: "Design demo",
  "设计演示 · 当前为示例数据，操作仅在本次预览内生效，不发送真实通知。":
    "Design demo · This is sample data. Actions affect only this preview and send no real notifications.",
  识别服务: "Detected vendor",
  "试试其他名称或状态筛选。": "Try another name or status filter.",
  "该状态页已接入，可直接查看，无需重复添加。":
    "This status page is already connected and can be viewed directly.",
  详情: "Details",
  "请为通知渠道命名。": "Name the notification channel.",
  "请先检测状态页地址。": "Detect the status-page URL first.",
  "请刷新重试。如果问题持续，请联系管理员。":
    "Reload the page. If the problem continues, contact your administrator.",
  "请填写候选适配器名称和版本。":
    "Enter the candidate adapter name and version.",
  "请填写操作原因。": "Enter a reason for this action.",
  "请填写状态页地址。": "Enter a status-page URL.",
  "请由管理员接入服务数据源。":
    "Ask an administrator to connect a vendor source.",
  "请至少选择一个通知渠道。": "Select at least one notification channel.",
  "请输入工作区标识。": "Enter a workspace.",
  "请输入有效的 HTTPS 地址，且不要在地址中包含用户名和密码。":
    "Enter a valid HTTPS URL without a username or password.",
  "请输入服务账号令牌。": "Enter a service-account token.",
  "请输入规则名称。": "Enter a rule name.",
  请选择通知渠道: "Select notification channels",
  "请重新登录，以继续接收状态更新。":
    "Sign in again to continue receiving status updates.",
  "调整筛选条件，或等待服务发布新的事件。":
    "Adjust the filters or wait for the vendor to publish a new incident.",
  调查中: "Investigating",
  资源标识: "Resource ID",
  资源类型: "Resource type",
  跟随系统: "System",
  跳到主要内容: "Skip to main content",
  跳到工作区登录: "Skip to workspace sign-in",
  轻微: "Minor",
  运行正常: "Operational",
  返回总览: "Back to overview",
  返回规则列表: "Back to rules",
  "还没有渠道？可直接在这里添加，已填写的规则会保留。":
    "No channels yet? Add one here; your rule draft will be preserved.",
  还没有通知渠道: "No notification channels yet",
  "连接 Slack 或 Webhook，为关键依赖配置通知规则。":
    "Connect Slack or a webhook and create notification rules for critical dependencies.",
  "连接团队常用工具，确保重要事件有明确的接收目的地。":
    "Connect the tools your team uses so important incidents have a clear destination.",
  "连接团队的接收渠道，再通过通知规则关联服务。":
    "Connect a team channel, then associate vendors through notification rules.",
  连接失败: "Connection failed",
  连接工作区: "Connect workspace",
  "追踪服务事件的完整进展，保留每一次官方更新。":
    "Track each vendor incident from start to finish and retain every official update.",
  "追踪工作区配置变更及操作者。":
    "Track workspace configuration changes and their actors.",
  退出登录: "Sign out",
  适配器: "Adapter",
  适配器发布: "Adapter rollout",
  "适配器：": "Adapter:",
  "选择服务与渠道，让重要的状态变化及时到达。":
    "Choose vendors and channels so important status changes arrive on time.",
  "选择服务，留空表示全部": "Select vendors; leave blank for all",
  "选择要关注的变化，以及它们应该到达的地方。":
    "Choose the changes to monitor and where they should be delivered.",
  "通用 Webhook": "Generic webhook",
  通知发送至: "Send notifications to",
  通知渠道: "Notification channels",
  通知规则: "Notification rules",
  "通知规则已保存。": "Notification rule saved.",
  "通过通用状态页适配器，面向数千个服务扩展。":
    "Scale to thousands of services through universal status-page adapters.",
  通过门禁并晋级: "Promote after passing gates",
  "部分 API 请求延迟升高（演示事件）":
    "Elevated latency for some API requests (demo incident)",
  部分中断: "Partial outage",
  "部分模型请求错误率升高（演示事件）":
    "Elevated error rate for some model requests (demo incident)",
  采样数: "Samples",
  "采样比例（0–1）": "Sample rate (0–1)",
  采集健康: "Collection health",
  采集恢复: "Collection recovered",
  采集新鲜度: "Collection freshness",
  采集降级: "Collection degraded",
  重大: "Major",
  重新投递通知: "Retry notification",
  重置筛选: "Reset filters",
  重试: "Retry",
  重试查询: "Retry query",
  错误率: "Error rate",
  "降级、中断或维护中": "Degraded, interrupted, or under maintenance",
  "集中查看外部依赖，区分服务状态与采集健康。":
    "See external dependencies in one place while distinguishing service status from collection health.",
  需要检查采集状态: "Collection needs attention",
  需要重新登录: "Sign-in required",
  页面暂时无法显示: "This page is temporarily unavailable",
  "（已停用）": " (disabled)",
  "（还有更多）": " (more available)",
  界面语言: "Language",
  简体中文: "简体中文",

  "渠道测试：{{value0}}": "Channel test: {{value0}}",
  "接收端返回 HTTP {{value0}}": "Receiver returned HTTP {{value0}}",
  "查询测试结果失败：{{value0}}": "Failed to retrieve test result: {{value0}}",
  "已添加「{{value0}}」，可以发送测试验证连接。":
    "Added “{{value0}}”. Send a test to verify the connection.",
  "最低 {{value0}}": "Minimum {{value0}}",
  "上限 {{value0}}%": "Maximum {{value0}}%",
  "主版本 {{value0}} ms": "Primary {{value0}} ms",
  "暂时无法接入该地址。{{value0}}":
    "This URL cannot be connected right now. {{value0}}",
  "请输入可公开访问的 HTTPS 状态页地址。":
    "Enter a publicly accessible HTTPS status page URL.",
  "无法解析该状态页的域名。": "The status page hostname could not be resolved.",
  "无法连接到该状态页。": "The status page could not be reached.",
  "该状态页的 HTTPS 证书或连接无效。":
    "The status page has an invalid HTTPS certificate or connection.",
  "状态页响应超时，请稍后重试。":
    "The status page timed out. Try again later.",
  "状态页返回的内容过大，无法安全检测。":
    "The status page response is too large to inspect safely.",
  "没有在该地址检测到受支持的状态页接口。":
    "No supported status-page API was detected at this address.",
  "暂时无法检测该状态页。":
    "The status page cannot be inspected right now.",
  "{{value0}} 个活跃事件，需要持续关注":
    "{{value0}} active incidents need attention",
  "{{value0}} 家服务的数据已过期，当前状态可能不是最新结果。":
    "Data for {{value0}} vendors is stale, so the current status may be outdated.",
  "查看 {{value0}}": "View {{value0}}",
  "{{value0}}及以上": "{{value0}} and above",
  "{{value0}} 个渠道": "{{value0}} channels",
  "最近成功采集：{{value0}}。超过 5 分钟或采集健康异常时标记过期。":
    "Last successful collection: {{value0}}. Data is marked stale after 5 minutes or when collection health is abnormal.",
  "请求失败（HTTP {{value0}}）": "Request failed (HTTP {{value0}})",
  "{{value0}} 分钟前": "{{value0}} minutes ago",
  "{{value0}} 小时前": "{{value0}} hours ago",
  "{{value0}} 天前": "{{value0}} days ago",
  "{{value0}} · {{value1}}{{value2}}": "{{value0}} · {{value1}}{{value2}}",
  "已加载 {{value0}} 个启用渠道{{value1}}。通过通知规则决定监控范围和接收方式。":
    "Loaded {{value0}} enabled channels{{value1}}. Use notification rules to define what to monitor and where to receive updates.",
  activeIncidentCount_one: "{{count}} active incident",
  activeIncidentCount_other: "{{count}} active incidents",
  activeIncidentAttention_one: "{{count}} active incident needs attention",
  activeIncidentAttention_other: "{{count}} active incidents need attention",
  staleVendorNotice_one:
    "Data for {{count}} vendor is stale, so the current status may be outdated.",
  staleVendorNotice_other:
    "Data for {{count}} vendors is stale, so the current status may be outdated.",
  sourceCount_one: "{{count}} source",
  sourceCount_other: "{{count}} sources",
  channelCount_one: "{{count}} channel",
  channelCount_other: "{{count}} channels",
  loadedEnabledChannels_one:
    "Loaded {{count}} enabled channel{{extra}}. Use notification rules to define what to monitor and where to receive updates.",
  loadedEnabledChannels_other:
    "Loaded {{count}} enabled channels{{extra}}. Use notification rules to define what to monitor and where to receive updates.",
  minutesAgo_one: "{{count}} minute ago",
  minutesAgo_other: "{{count}} minutes ago",
  hoursAgo_one: "{{count}} hour ago",
  hoursAgo_other: "{{count}} hours ago",
  daysAgo_one: "{{count}} day ago",
  daysAgo_other: "{{count}} days ago",
};

const chinese: Record<string, string> = {
  activeIncidentCount_other: "{{count}} 个活跃事件",
  activeIncidentAttention_other: "{{count}} 个活跃事件，需要持续关注",
  staleVendorNotice_other:
    "{{count}} 家服务的数据已过期，当前状态可能不是最新结果。",
  sourceCount_other: "{{count}} 个数据源",
  channelCount_other: "{{count}} 个渠道",
  loadedEnabledChannels_other:
    "已加载 {{count}} 个启用渠道{{extra}}。通过通知规则决定监控范围和接收方式。",
  minutesAgo_other: "{{count}} 分钟前",
  hoursAgo_other: "{{count}} 小时前",
  daysAgo_other: "{{count}} 天前",
};

const isBrowser = typeof window !== "undefined";
export function languagePreference(): LanguagePreference {
  try {
    const stored = isBrowser
      ? window.localStorage.getItem("statushub-language")
      : null;
    return stored === "zh" || stored === "en" ? stored : "system";
  } catch {
    return "system";
  }
}

function browserLanguages(): readonly string[] {
  if (!isBrowser) return [];
  return window.navigator.languages?.length
    ? window.navigator.languages
    : [window.navigator.language];
}

export const initialLanguage = resolveLanguage(
  languagePreference(),
  browserLanguages(),
);

void i18n.use(initReactI18next).init({
  lng: initialLanguage,
  fallbackLng: false,
  supportedLngs: ["en", "zh"],
  keySeparator: false,
  nsSeparator: false,
  returnEmptyString: true,
  resources: {
    en: { translation: english },
    zh: { translation: chinese },
  },
  interpolation: { escapeValue: false },
});

export function tr(key: string, options?: TOptions): string {
  return String(i18n.t(key, options));
}

export function currentLanguage(): AppLanguage {
  const language = i18n.resolvedLanguage || i18n.language || initialLanguage;
  return language.startsWith("zh") ? "zh" : "en";
}

export function formatList(values: string[]): string {
  return new Intl.ListFormat(currentLanguage() === "zh" ? "zh-CN" : "en-US", {
    style: "long",
    type: "conjunction",
  }).format(values);
}

export function setLanguage(preference: LanguagePreference) {
  if (!isBrowser) return;
  try {
    if (preference === "system")
      window.localStorage.setItem("statushub-language", "system");
    else window.localStorage.setItem("statushub-language", preference);
  } catch {
    // Storage can be unavailable in restricted browsing environments.
  }
  const language = resolveLanguage(preference, browserLanguages());
  if (language === currentLanguage()) return;
  void i18n.changeLanguage(language).then(() => window.location.reload());
}

export default i18n;
