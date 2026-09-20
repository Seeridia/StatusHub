import { useEffect, useMemo, useState } from "react";
import { Link, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { Alert, Breadcrumb, Button, Card, Input, Layout, Menu, Space, Table, Tag } from "tdesign-react";
import { AddIcon, AppIcon, DashboardIcon, HistoryIcon, LogoutIcon, RefreshIcon, SearchIcon, UserIcon } from "tdesign-icons-react";
import { Dialog } from "../overlays";
import { BrandMark, LanguageSelect } from "../App";
import { accountRequest, clearSession, platformRequest, type AccountSession } from "../lib/api";
import { tr } from "../lib/i18n";

type Summary = { users: number; enabled_users: number; workspaces: number; platform_sources: number };
type PlatformUser = { id: string; email: string; name: string; enabled: boolean; platform_admin: boolean; workspace_count: number; active_sessions: number; created_at: string; last_session_at?: string };
type PlatformWorkspace = { id: string; slug: string; name: string; admin_email: string; members: number; sources: number; created_at: string };
type PlatformAuditEvent = { id: number; occurred_at: string; actor_email: string; action: string; resource_type: string; resource_id: string; metadata: Record<string, unknown> };

function date(value?: string) {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : tr("暂无");
}

function Overview() {
  const [data, setData] = useState<Summary | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { platformRequest<Summary>("/summary").then(setData).catch((e) => setError((e as Error).message)); }, []);
  const cards = [[tr("用户总数"), data?.users], [tr("启用用户"), data?.enabled_users], [tr("工作区"), data?.workspaces], [tr("平台数据源"), data?.platform_sources]];
  return <>
    <div className="admin-page-heading"><div><h1>{tr("平台总览")}</h1><p>{tr("查看 StatusHub 实例的账号、工作区与共享资源规模。")}</p></div></div>
    {error && <Alert theme="error" message={error} />}
    <div className="admin-stat-grid">{cards.map(([title, value]) => <Card key={String(title)} className="admin-stat-card"><span>{title}</span><strong>{value ?? "—"}</strong></Card>)}</div>
    <Card className="admin-guide-card" title={tr("管理边界")}><p>{tr("平台管理员管理整个实例；工作区管理员只管理自己的成员与业务配置。两种权限相互独立。")}</p></Card>
  </>;
}

function Users({ self }: { self: string }) {
  const [rows, setRows] = useState<PlatformUser[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<{ user: PlatformUser; action: "enabled" | "sessions" | "platform" } | null>(null);
  async function load() {
    setLoading(true); setError("");
    try { const result = await platformRequest<{ data: PlatformUser[] }>(`/users?query=${encodeURIComponent(query)}`); setRows(result.data || []); }
    catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);
  async function apply() {
    if (!confirm) return;
    setLoading(true); setError("");
    try {
      if (confirm.action === "enabled") await platformRequest(`/users/${confirm.user.id}`, { method: "PATCH", body: JSON.stringify({ enabled: !confirm.user.enabled }) });
      else if (confirm.action === "sessions") await platformRequest(`/users/${confirm.user.id}/revoke-sessions`, { method: "POST", body: "{}" });
      else if (confirm.user.platform_admin) await platformRequest(`/platform-admins/${confirm.user.id}`, { method: "DELETE", body: "{}" });
      else await platformRequest("/platform-admins", { method: "POST", body: JSON.stringify({ user_id: confirm.user.id }) });
      setConfirm(null); await load();
    } catch (e) { setError((e as Error).message); setLoading(false); }
  }
  const actions = (row: PlatformUser, mobile = false) => <Space breakLine={mobile}>
    <Button variant={mobile ? "outline" : "text"} disabled={!row.enabled || (row.id === self && row.platform_admin)} onClick={() => setConfirm({ user: row, action: "platform" })}>{row.platform_admin ? tr("撤销平台权限") : tr("授予平台管理员")}</Button>
    <Button variant="text" disabled={row.id === self && row.enabled} onClick={() => setConfirm({ user: row, action: "enabled" })}>{row.enabled ? tr("停用") : tr("恢复")}</Button>
    <Button variant="text" disabled={row.active_sessions === 0} onClick={() => setConfirm({ user: row, action: "sessions" })}>{tr("撤销会话")}</Button>
  </Space>;
  const dialogTitle = confirm?.action === "sessions" ? tr("撤销全部会话") : confirm?.action === "platform" ? (confirm.user.platform_admin ? tr("撤销平台权限") : tr("授予平台管理员")) : confirm?.user.enabled ? tr("停用用户") : tr("恢复用户");
  return <>
    <div className="admin-page-heading"><div><h1>{tr("用户管理")}</h1><p>{tr("管理实例账号、平台权限和浏览器会话。工作区角色仍由各工作区管理员维护。")}</p></div><Button icon={<RefreshIcon />} variant="outline" loading={loading} onClick={() => void load()}>{tr("刷新")}</Button></div>
    {error && <Alert theme="error" message={error} />}
    <Card>
      <div className="admin-filter"><Input value={query} onChange={(v) => setQuery(String(v))} prefixIcon={<SearchIcon />} placeholder={tr("搜索邮箱或名称")} onEnter={() => void load()} /><Button theme="primary" onClick={() => void load()}>{tr("搜索")}</Button></div>
      <Table className="admin-desktop-table" rowKey="id" tableLayout="fixed" data={rows} loading={loading} empty={tr("暂无用户")} columns={[
        { colKey: "email", title: tr("用户"), minWidth: 230, cell: ({ row }) => <div><strong>{row.name || row.email}</strong>{row.name && <span className="cell-subtitle">{row.email}</span>}</div> },
        { colKey: "status", title: tr("状态"), width: 110, cell: ({ row }) => <Tag theme={row.enabled ? "success" : "default"}>{row.enabled ? tr("已启用") : tr("已停用")}</Tag> },
        { colKey: "scope", title: tr("权限与范围"), minWidth: 180, cell: ({ row }) => <Space>{row.platform_admin && <Tag theme="primary">{tr("平台管理员")}</Tag>}<span>{tr("{{count}} 个工作区", { count: row.workspace_count })}</span></Space> },
        { colKey: "sessions", title: tr("活跃会话"), width: 100, cell: ({ row }) => row.active_sessions },
        { colKey: "last", title: tr("最近活动"), minWidth: 160, cell: ({ row }) => date(row.last_session_at) },
        { colKey: "actions", title: tr("操作"), fixed: "right", width: 360, cell: ({ row }) => actions(row) },
      ]} />
      <div className="admin-mobile-list">{rows.length === 0 && <p className="muted">{tr("暂无用户")}</p>}{rows.map((row) => <section className="admin-mobile-item" key={row.id}><div className="admin-mobile-title"><div><strong>{row.name || row.email}</strong>{row.name && <span className="cell-subtitle">{row.email}</span>}</div><Tag theme={row.enabled ? "success" : "default"}>{row.enabled ? tr("已启用") : tr("已停用")}</Tag></div><div className="admin-mobile-meta">{row.platform_admin && <Tag theme="primary">{tr("平台管理员")}</Tag>}<span>{tr("{{count}} 个工作区", { count: row.workspace_count })}</span><span>{tr("{{count}} 个活跃会话", { count: row.active_sessions })}</span></div>{actions(row, true)}</section>)}</div>
    </Card>
    <Dialog visible={!!confirm} header={dialogTitle} confirmBtn={tr("确认")} cancelBtn={tr("取消")} onClose={() => setConfirm(null)} onCancel={() => setConfirm(null)} onConfirm={() => void apply()}>
      <p>{confirm?.action === "sessions" ? tr("该用户需要重新登录所有浏览器。") : confirm?.action === "platform" ? (confirm.user.platform_admin ? tr("该用户将失去整个实例的管理权限，工作区角色不受影响。") : tr("该用户将可以管理整个实例的用户、工作区和平台审计记录。")) : confirm?.user.enabled ? tr("该用户将无法登录，现有会话也会立即失效。") : tr("该用户将可以重新登录并访问仍然有效的工作区。")}</p><strong>{confirm?.user.email}</strong>
    </Dialog>
  </>;
}

function Workspaces() {
  const [rows, setRows] = useState<PlatformWorkspace[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: "", slug: "", admin_email: "" });
  async function load() {
    setLoading(true); setError("");
    try { const result = await platformRequest<{ data: PlatformWorkspace[] }>(`/workspaces?query=${encodeURIComponent(query)}`); setRows(result.data || []); }
    catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);
  async function create() {
    setLoading(true); setError("");
    try { await platformRequest("/workspaces", { method: "POST", body: JSON.stringify(form) }); setCreating(false); setForm({ name: "", slug: "", admin_email: "" }); await load(); }
    catch (e) { setError((e as Error).message); setLoading(false); }
  }
  return <>
    <div className="admin-page-heading"><div><h1>{tr("工作区管理")}</h1><p>{tr("查看工作区规模，或将已有用户指定为新工作区管理员。")}</p></div><Space><Button icon={<RefreshIcon />} variant="outline" loading={loading} onClick={() => void load()}>{tr("刷新")}</Button><Button icon={<AddIcon />} theme="primary" onClick={() => setCreating(true)}>{tr("创建工作区")}</Button></Space></div>
    {error && <Alert theme="error" message={error} />}
    <Card><div className="admin-filter"><Input value={query} onChange={(v) => setQuery(String(v))} prefixIcon={<SearchIcon />} placeholder={tr("搜索工作区、标识或管理员")} onEnter={() => void load()} /><Button theme="primary" onClick={() => void load()}>{tr("搜索")}</Button></div>
      <Table className="admin-desktop-table" rowKey="id" tableLayout="fixed" data={rows} loading={loading} empty={tr("暂无工作区")} columns={[
        { colKey: "name", title: tr("工作区"), minWidth: 220, cell: ({ row }) => <div><strong>{row.name}</strong><span className="cell-subtitle">{row.slug}</span></div> },
        { colKey: "admin", title: tr("管理员"), minWidth: 220, cell: ({ row }) => row.admin_email },
        { colKey: "members", title: tr("成员"), width: 100, cell: ({ row }) => row.members },
        { colKey: "sources", title: tr("数据源"), width: 100, cell: ({ row }) => row.sources },
        { colKey: "created", title: tr("创建时间"), minWidth: 170, cell: ({ row }) => date(row.created_at) },
        { colKey: "action", title: tr("操作"), fixed: "right", width: 120, cell: ({ row }) => <Link to={`/ui/?tenant=${encodeURIComponent(row.slug)}#/overview`} reloadDocument>{tr("进入工作区")}</Link> },
      ]} />
      <div className="admin-mobile-list">{rows.length === 0 && <p className="muted">{tr("暂无工作区")}</p>}{rows.map((row) => <section className="admin-mobile-item" key={row.id}><div className="admin-mobile-title"><div><strong>{row.name}</strong><span className="cell-subtitle">{row.slug}</span></div><a href={`/ui/?tenant=${encodeURIComponent(row.slug)}#/overview`}>{tr("进入工作区")}</a></div><div className="admin-mobile-meta"><span>{tr("管理员")}: {row.admin_email}</span><span>{tr("{{count}} 名成员", { count: row.members })}</span><span>{tr("{{count}} 个数据源", { count: row.sources })}</span></div></section>)}</div>
    </Card>
    <Dialog visible={creating} header={tr("创建工作区")} confirmBtn={tr("创建")} cancelBtn={tr("取消")} confirmLoading={loading} onClose={() => setCreating(false)} onCancel={() => setCreating(false)} onConfirm={() => void create()}>
      <div className="admin-dialog-form"><label>{tr("工作区名称")}<Input value={form.name} maxlength={128} onChange={(v) => setForm({ ...form, name: String(v) })} placeholder={tr("例如：基础设施团队")} /></label><label>{tr("工作区标识")}<Input value={form.slug} maxlength={63} onChange={(v) => setForm({ ...form, slug: String(v).toLowerCase() })} placeholder="infrastructure" /></label><label>{tr("管理员邮箱")}<Input value={form.admin_email} onChange={(v) => setForm({ ...form, admin_email: String(v) })} placeholder="admin@example.com" /></label><p className="field-hint">{tr("管理员必须是当前实例中已启用的用户；此操作不会创建账号或密码。")}</p></div>
    </Dialog>
  </>;
}

function AuditEvents() {
  const [rows, setRows] = useState<PlatformAuditEvent[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  async function load() {
    setLoading(true); setError("");
    try { const result = await platformRequest<{ data: PlatformAuditEvent[] }>(`/audit-events?query=${encodeURIComponent(query)}`); setRows(result.data || []); }
    catch (e) { setError((e as Error).message); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);
  const details = (row: PlatformAuditEvent) => Object.keys(row.metadata || {}).length ? JSON.stringify(row.metadata) : "—";
  return <>
    <div className="admin-page-heading"><div><h1>{tr("平台审计")}</h1><p>{tr("查看平台管理员对用户、权限和工作区执行的写操作。")}</p></div><Button icon={<RefreshIcon />} variant="outline" loading={loading} onClick={() => void load()}>{tr("刷新")}</Button></div>
    {error && <Alert theme="error" message={error} />}
    <Card><div className="admin-filter"><Input value={query} onChange={(v) => setQuery(String(v))} prefixIcon={<SearchIcon />} placeholder={tr("搜索操作者、动作或资源")} onEnter={() => void load()} /><Button theme="primary" onClick={() => void load()}>{tr("搜索")}</Button></div>
      <Table className="admin-desktop-table" rowKey="id" tableLayout="fixed" data={rows} loading={loading} empty={tr("暂无平台审计记录")} columns={[
        { colKey: "time", title: tr("时间"), minWidth: 170, cell: ({ row }) => date(row.occurred_at) },
        { colKey: "actor", title: tr("操作者"), minWidth: 200, cell: ({ row }) => row.actor_email || tr("系统或命令行") },
        { colKey: "action", title: tr("动作"), minWidth: 190, cell: ({ row }) => <code>{row.action}</code> },
        { colKey: "resource", title: tr("资源"), minWidth: 220, cell: ({ row }) => <div>{row.resource_type}<span className="cell-subtitle admin-resource-id">{row.resource_id}</span></div> },
        { colKey: "details", title: tr("详情"), minWidth: 260, ellipsis: true, cell: ({ row }) => details(row) },
      ]} />
      <div className="admin-mobile-list">{rows.length === 0 && <p className="muted">{tr("暂无平台审计记录")}</p>}{rows.map((row) => <section className="admin-mobile-item" key={row.id}><div className="admin-mobile-title"><strong>{row.action}</strong><span>{date(row.occurred_at)}</span></div><div className="admin-mobile-meta"><span>{row.actor_email || tr("系统或命令行")}</span><span>{row.resource_type}: {row.resource_id}</span></div><code className="admin-audit-meta">{details(row)}</code></section>)}</div>
    </Card>
  </>;
}

export default function PlatformAdmin({ snapshot, onLogout }: { snapshot: AccountSession; onLogout: () => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const nav = useMemo(() => [
    { path: "/overview", label: tr("平台总览"), icon: <DashboardIcon /> },
    { path: "/users", label: tr("用户管理"), icon: <UserIcon /> },
    { path: "/workspaces", label: tr("工作区管理"), icon: <AppIcon /> },
    { path: "/audit", label: tr("平台审计"), icon: <HistoryIcon /> },
  ], []);
  const active = nav.find((item) => location.pathname.startsWith(item.path)) || nav[0];
  async function logout() { await accountRequest("logout").catch(() => {}); clearSession(); onLogout(); }
  return <Layout className="admin-shell">
    <aside className="admin-sidebar"><a className="brand" href="/admin/#/overview"><BrandMark /></a><div className="admin-product-label">{tr("实例管理")}</div><Menu value={active.path} onChange={(v) => navigate(String(v))}>{nav.map((item) => <Menu.MenuItem key={item.path} value={item.path} icon={item.icon}>{item.label}</Menu.MenuItem>)}</Menu></aside>
    <Layout className="admin-main"><header className="topbar"><Breadcrumb><Breadcrumb.BreadcrumbItem>{tr("实例管理")}</Breadcrumb.BreadcrumbItem><Breadcrumb.BreadcrumbItem>{active.label}</Breadcrumb.BreadcrumbItem></Breadcrumb><Space><Button variant="text" href="/ui/">{tr("返回工作台")}</Button><LanguageSelect /><Button shape="square" variant="text" icon={<LogoutIcon />} aria-label={tr("退出登录")} onClick={() => void logout()} /></Space></header>
      <main className="admin-content"><Routes><Route path="/overview" element={<Overview />} /><Route path="/users" element={<Users self={snapshot.user.id} />} /><Route path="/workspaces" element={<Workspaces />} /><Route path="/audit" element={<AuditEvents />} /><Route path="/" element={<Navigate to="/overview" replace />} /><Route path="*" element={<Navigate to="/overview" replace />} /></Routes></main>
    </Layout>
  </Layout>;
}
