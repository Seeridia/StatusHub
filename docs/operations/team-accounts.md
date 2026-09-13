# 团队账号、成员权限与服务账号

StatusMon 支持仅邀请加入的邮箱密码账号和 SSO 成员。没有公开注册或自助创建工作区入口。Viewer/Operator 使用业务页面；Admin 管理 Viewer/Operator；Owner 管理全部工作区角色。不能修改自己的角色或停用自己，必须保留一位有效人员 Owner。服务账号不计入人员 Owner。

## 升级与初始化

已有环境升级时，按 [升级步骤](maintenance.md#版本升级) 应用尚未执行的迁移（包括 `000013_team_identity.up.sql`），再部署新版 API；首次安装已执行全部迁移时无需重复。现有 SSO 成员和服务账号 Token 保留；旧浏览器会话失效，需要重新登录。新增会话在数据库中可撤销，角色与启用状态每次请求重新检查，SSE 至少每 15 秒重新验证。

首位人员 Owner 由可信运维创建邀请：

```bash
# DATABASE_URL 已配置；TENANT_ID 为工作区 UUID。
umask 077
mkdir -p tmp/local
go run ./cmd/statusmon-admin owner-invite \
  -tenant-id "$TENANT_ID" -email 'owner@example.com' > tmp/local/owner-invite.json
```

将结果中的邀请令牌组装为 `https://你的域名/ui/?tenant=工作区标识#/account-flow?mode=invite&token=邀请令牌`，只分享给指定收件人。已有有效人员 Owner 时不能再次初始化；重新初始化会撤销旧的 bootstrap 邀请。不要把邀请文件提交到 Git。

## 邮件配置

身份邮件使用独立 SMTP，不复用事件通知渠道。

| 环境变量 | 用途 |
| --- | --- |
| `STATUSMON_SMTP_ADDRESS` | SMTP 主机与端口，例如 `smtp.example.com:587` |
| `STATUSMON_SMTP_FROM` | 发件邮箱 |
| `STATUSMON_SMTP_USERNAME` | SMTP 用户名，可选 |
| `STATUSMON_SMTP_PASSWORD` | SMTP 密码，可选，保存至 secret manager |
| `STATUSMON_SMTP_ALLOW_LOCAL_PLAINTEXT` | 仅允许 loopback 邮件沙箱明文，生产不启用 |
| `STATUSMON_PUBLIC_URL` | 邮件链接与 SSO 使用的公开 HTTPS 地址 |

SMTP 使用 STARTTLS（TLS 1.2 以上）。不支持隐式 TLS 465 端口。若 SMTP 未配置，身份邮件无法发送，账号仍保持未验证；不能通过邀请链接跳过验证。

本地沙箱：

```bash
docker compose -f deploy/compose.yaml up -d mailpit
export STATUSMON_SMTP_ADDRESS=127.0.0.1:51025
export STATUSMON_SMTP_FROM=statusmon@localhost
export STATUSMON_SMTP_ALLOW_LOCAL_PLAINTEXT=true
```

重启 API 后，打开 `http://127.0.0.1:58025` 查看邮件。验证本地流程时使用 `.test` 收件人，邮件仅保留在沙箱。真实 SMTP、DNS、HTTPS 和投递到真实邮箱需要单独验收。

邮件任务载荷加密保存，每 5 秒领取；失败指数退避，最多 6 次，最长不超过令牌有效期。发送成功、过期或尝试耗尽后清除秘密载荷。任务有租约，多个 API 实例可共同处理；SMTP 接收确认丢失可能重复收到邮件，但链接只能成功使用一次。

## 邀请与成员管理

1. 打开「设置 → 邀请」，输入邮箱和角色，确认创建。
2. 安全保存并分享邀请链接。默认 7 天有效；相同邮箱重新创建会撤销旧邀请。
3. 新的邮箱密码账号选择发送验证邮件，再通过邮件链接设置 12–128 字符密码。已有密码账号输入原密码接受邀请。
4. SSO 成员选择通过 SSO 接受邀请，提供方必须返回已验证、与邀请一致的邮箱。仍需配置正确工作区 issuer/client/回调地址。
5. 在「设置 → 成员」调整角色、停用或恢复。邀请不会覆盖已有成员角色或自动恢复被停用成员。

邮箱只做首尾空格去除和大小写规范化，不做邮箱提供商特有的点号/别名合并。SSO 与本地账号不会因为邮箱相同自动合并。邀请人被停用或降权后，未接受的邀请将无法继续接受。Admin 不能重新生成高权限邀请来绕过权限边界。

## 密码与会话

登录页使用工作区、邮箱和密码。忘记密码会始终返回统一提示；符合条件时发送 30 分钟有效的重置链接。验证链接有效期 24 小时。

「设置 → 个人账号」提供修改本地密码和退出全部浏览器会话。修改密码需要原密码，修改或重置成功后全部会话撤销。SSO 用户的密码由身份提供方管理；服务账号在服务账号页轮换 Token。

登录与验证请求默认按 IP、邮箱/操作分别限制每 15 分钟 20 次，邮件请求同一邮箱每分钟一次。限流数据库共享，不因重启清零。未配置可信代理时使用实际连接 IP，不信任任意 X-Forwarded-For。

## 服务账号

在「设置 → 服务账号」创建、调整角色、停用、恢复和轮换。新 Token 只出现在创建或轮换结果中，不提供查看旧 Token 的接口。轮换立即使旧 Token 失效，停用立即拒绝后续认证。

保存成功结果前不要关闭对话框。网络重试使用同一幂等键，在 10 分钟内返回同一加密保存的秘密结果；之后只返回操作已完成且秘密不可取回，不能再次触发创建或轮换。此时需要发起一次新的轮换。列表、审计和普通响应缓存不保存 Token。

## 故障定位

- 无邮件：检查 SMTP 配置、API 后台日志，以及 `identity_mail_jobs` 的尝试次数、到期和完成时间；不要输出 payload。
- 邀请失效：检查期限、撤销、是否已接受，以及邀请人的当前权限。
- 无法停用 Owner：先安排另一位有效人员 Owner；不要用服务账号替代人员 Owner。
- 登录成功但进不了工作区：检查该身份在目标工作区是否有启用的成员关系。
- 角色修改后界面仍显示旧按钮：刷新获取最新会话信息；服务端已按新权限拒绝操作。

回滚应用前安排维护窗口，旧版应用不识别新成员停用和会话撤销机制。禁止在已有停用成员的系统直接恢复旧版身份认证。数据库 down 迁移会删除新账号凭据、邀请和会话等身份数据，仅用于隔离演练，不用于无备份的生产回退。
