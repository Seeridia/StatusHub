# 平台管理后台

[返回操作文档](README.md)

平台管理后台位于 `/admin/`，用于管理整个 StatusHub 实例。平台管理员与工作区 Admin 是两套独立权限：前者可以查看实例用户和工作区、全局停用账号并撤销浏览器会话；后者只管理一个工作区内的成员和业务配置。

## 授予首位平台管理员

先让目标邮箱完成 StatusHub 账号初始化或邀请注册，再在 API 容器内执行：

```bash
statushub-admin platform-admin-grant -email admin@example.com -actor-id initial-bootstrap
```

打开 `https://your-statushub.example/admin/`，使用同一个邮箱密码登录。命令只接受已存在且已启用的人员账号，不创建密码，也不会授予任何工作区角色。

增加另一名平台管理员后，可以撤销旧授权：

```bash
statushub-admin platform-admin-revoke -email old-admin@example.com -actor-id security-rotation
```

系统拒绝撤销最后一名有效平台管理员。

## 用户操作

- **停用用户**：阻止该人员再次登录，并立即撤销其全部浏览器会话；工作区成员关系、审计记录和历史业务数据保留。若该用户仍是某个工作区的 Admin，须先在对应工作区转移管理员。
- **恢复用户**：允许该人员重新登录；原有且仍启用的工作区成员关系重新生效。
- **撤销会话**：不改变账号或工作区状态，只要求该用户在所有浏览器重新登录。

平台后台不显示密码、会话令牌、服务账号 Token 或通知渠道秘密。平台级写操作记录在只追加的 `platform_audit_events` 中。

## 当前版本范围

v0.2 首批页面包含实例总览、用户管理和工作区总览。共享数据源治理、贡献审核与实例配置诊断将在后续 v0.2 增量中接入同一后台。
