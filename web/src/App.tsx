import { setLanguage, tr, type LanguagePreference } from "./lib/i18n";
import {
  LoginPage,
  SetupPage,
  InvitationPage,
  ForgotPage,
  ResetPage,
  VerifyEmailPage,
  WorkspacePicker,
  AccountPage,
} from "./pages/Auth";
import { FormField } from "./components";
import { lazy, Suspense, useEffect, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Breadcrumb,
  Button,
  Dropdown,
  Form,
  Input,
  Layout,
  Menu,
  Skeleton,
  Tag,
  Tooltip,
} from "tdesign-react";
import {
  TranslateIcon,
  ModeLightIcon,
  AppIcon,
  ChevronRightIcon,
  CloudIcon,
  DashboardIcon,
  HistoryIcon,
  LinkIcon,
  LogoutIcon,
  MenuFoldIcon,
  NotificationIcon,
  SettingIcon,
  ServerIcon,
  UserIcon,
} from "tdesign-icons-react";
import {
  APIError,
  api,
  clearSession,
  currentTenant,
  demo,
  accountSession,
  accountRequest,
  configure,
  enterWorkspace,
  request,
  type AccountSession,
} from "./lib/api";
import { SessionContext, useLive } from "./lib/hooks";
import { label } from "./lib/model";
import type { Session } from "./lib/types";
const Overview = lazy(() => import("./pages/Overview"));
const Incidents = lazy(() => import("./pages/Incidents"));
const Rules = lazy(() => import("./pages/Rules"));
const Channels = lazy(() => import("./pages/Channels"));
const Operations = lazy(() => import("./pages/Operations"));

export function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <span
      className={`brand-mark ${compact ? "is-compact" : ""}`}
      translate="no"
    >
      <img
        className="brand-mark__logo"
        src={`${import.meta.env.BASE_URL}brand/statushub.svg`}
        width={32}
        height={27}
        alt=""
      />
      {!compact && (
        <span className="brand-mark__name" aria-label="StatusHub">
          <span className="brand-mark__status">Status</span>
          <span className="brand-mark__hub">Hub</span>
        </span>
      )}
    </span>
  );
}

export function LanguageSelect() {
  return (
    <Dropdown
      trigger="click"
      options={[
        { value: "system", content: tr("跟随浏览器") },
        { value: "zh", content: tr("简体中文") },
        { value: "en", content: "English" },
      ]}
      onClick={(item) => setLanguage(String(item.value) as LanguagePreference)}
    >
      <Button
        variant="text"
        theme="default"
        shape="square"
        aria-label={tr("界面语言")}
        title={tr("界面语言")}
        icon={<TranslateIcon />}
      />
    </Dropdown>
  );
}

const navigation = [
  { path: "/overview", label: tr("\u603B\u89C8"), icon: <DashboardIcon /> },
  {
    path: "/vendors",
    label: tr("\u5382\u5546\u72B6\u6001"),
    icon: <CloudIcon />,
  },
  {
    path: "/incidents",
    label: tr("\u4E8B\u4EF6\u4E2D\u5FC3"),
    icon: <NotificationIcon />,
  },
  { path: "/sources", label: tr("数据源"), icon: <ServerIcon /> },
  { path: "/rules", label: tr("\u901A\u77E5\u89C4\u5219"), icon: <AppIcon /> },
  {
    path: "/channels",
    label: tr("\u901A\u77E5\u6E20\u9053"),
    icon: <LinkIcon />,
  },
  {
    path: "/deliveries",
    label: tr("\u6295\u9012\u8BB0\u5F55"),
    icon: <HistoryIcon />,
  },
  { path: "/settings", label: tr("\u8BBE\u7F6E"), icon: <SettingIcon /> },
];
function Workspace({
  session,
  onLogout,
}: {
  session: Session;
  onLogout: () => void;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [theme, setTheme] = useState(
    localStorage.getItem("statushub-theme") || "system",
  );
  useEffect(() => {
    const media = matchMedia("(max-width: 760px)");
    const resize = () => {
      if (media.matches) setCollapsed(false);
      else setMobileOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileOpen(false);
    };
    resize();
    media.addEventListener("change", resize);
    window.addEventListener("keydown", escape);
    return () => {
      media.removeEventListener("change", resize);
      window.removeEventListener("keydown", escape);
    };
  }, []);
  const [loggingOut, setLoggingOut] = useState(false);
  const [logoutError, setLogoutError] = useState("");
  const live = useLive();
  const active =
    navigation.find((item) => location.pathname.startsWith(item.path)) ||
    navigation[0];
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const apply = () =>
      document.documentElement.setAttribute(
        "theme-mode",
        theme === "system" ? (media.matches ? "dark" : "light") : theme,
      );
    apply();
    localStorage.setItem("statushub-theme", theme);
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [theme]);
  async function logout() {
    setLoggingOut(true);
    try {
      if (!demo) await accountRequest("logout");
      clearSession();
      queryClient.clear();
      onLogout();
    } catch (error) {
      if (error instanceof APIError && error.status === 401) {
        clearSession();
        queryClient.clear();
        onLogout();
      } else setLogoutError((error as Error).message);
    } finally {
      setLoggingOut(false);
    }
  }
  return (
    <SessionContext.Provider value={session}>
      <Layout
        className={`app-shell ${collapsed ? "is-collapsed" : ""} ${mobileOpen ? "mobile-open" : ""}`}
      >
        <a
          className="skip-link"
          href="#workspace-main"
          onClick={(event) => {
            event.preventDefault();
            document.getElementById("workspace-main")?.focus();
          }}
        >
          {tr("\u8DF3\u5230\u4E3B\u8981\u5185\u5BB9")}
        </a>
        <aside className="sidebar">
          <a className="brand" href="#/overview" aria-label="StatusHub">
            <BrandMark compact={collapsed} />
          </a>
          <div className="workspace-chip">
            <UserIcon />
            {!collapsed && (
              <span>
                {session.tenant.name}
                <small>{label(session.identity.role)}</small>
              </span>
            )}
          </div>

          <Menu
            width={["100%", "100%"]}
            value={active.path}
            collapsed={collapsed}
            onChange={(value) => {
              navigate(String(value));
              setMobileOpen(false);
            }}
          >
            {navigation.map((item) => (
              <Menu.MenuItem
                key={item.path}
                value={item.path}
                icon={item.icon}
                href={`#${item.path}`}
              >
                {item.label}
              </Menu.MenuItem>
            ))}
          </Menu>
        </aside>
        {mobileOpen && (
          <button
            aria-label={tr("\u5173\u95ED\u5BFC\u822A")}
            className="mobile-backdrop"
            onClick={() => setMobileOpen(false)}
          />
        )}
        <Layout className="main-layout">
          <header className="topbar">
            <div className="topbar-left">
              <Button
                aria-label={tr("\u5207\u6362\u5BFC\u822A")}
                variant="text"
                shape="square"
                icon={<MenuFoldIcon />}
                onClick={() =>
                  matchMedia("(max-width: 760px)").matches
                    ? setMobileOpen(!mobileOpen)
                    : setCollapsed(!collapsed)
                }
              />
              <Breadcrumb className="breadcrumb">
                <Breadcrumb.BreadcrumbItem>
                  {tr("\u5DE5\u4F5C\u53F0")}
                </Breadcrumb.BreadcrumbItem>
                <Breadcrumb.BreadcrumbItem>
                  {active.label}
                </Breadcrumb.BreadcrumbItem>
              </Breadcrumb>
            </div>
            <div className="topbar-right">
              {live !== "demo" && (
                <Tag
                  variant="light"
                  theme={
                    live === "live"
                      ? "success"
                      : live === "demo"
                        ? "primary"
                        : "warning"
                  }
                >
                  {
                    {
                      live: tr("\u5B9E\u65F6\u8FDE\u63A5\u6B63\u5E38"),
                      demo: tr("\u8BBE\u8BA1\u6F14\u793A"),
                      connecting: tr("\u6B63\u5728\u8FDE\u63A5"),
                      reconnecting: tr("\u6B63\u5728\u91CD\u8FDE"),
                      unauthorized: tr("\u9700\u8981\u91CD\u65B0\u767B\u5F55"),
                    }[live]
                  }
                </Tag>
              )}
              <Tooltip content={tr("切换工作区")}>
                <Button variant="text" shape="square" aria-label={tr("切换工作区")} icon={<AppIcon />} onClick={() => navigate("/workspaces")} />
              </Tooltip>
              <Tooltip content={tr("个人账号")}>
                <Button variant="text" shape="square" aria-label={tr("个人账号")} icon={<UserIcon />} onClick={() => navigate("/account")} />
              </Tooltip>
              <LanguageSelect />
              <Dropdown
                trigger="click"
                options={[
                  { value: "system", content: tr("跟随系统") },
                  { value: "light", content: tr("浅色模式") },
                  { value: "dark", content: tr("深色模式") },
                ]}
                onClick={(item) => setTheme(String(item.value))}
              >
                <Button
                  variant="text"
                  theme="default"
                  shape="square"
                  aria-label={tr("外观主题")}
                  title={`${tr("外观主题")}: ${{ system: tr("跟随系统"), light: tr("浅色模式"), dark: tr("深色模式") }[theme as "system" | "light" | "dark"]}`}
                  icon={<ModeLightIcon />}
                />
              </Dropdown>
              <Tooltip content={tr("\u9000\u51FA\u767B\u5F55")}>
                <Button
                  aria-label={tr("\u9000\u51FA\u767B\u5F55")}
                  variant="text"
                  shape="square"
                  icon={<LogoutIcon />}
                  loading={loggingOut}
                  onClick={() => void logout()}
                />
              </Tooltip>
            </div>
          </header>
          <main id="workspace-main" tabIndex={-1} className="workspace-content">
            {demo && (
              <Alert
                className="demo-banner"
                theme="info"
                message={tr(
                  "\u8BBE\u8BA1\u6F14\u793A \u00B7 \u5F53\u524D\u4E3A\u793A\u4F8B\u6570\u636E\uFF0C\u64CD\u4F5C\u4EC5\u5728\u672C\u6B21\u9884\u89C8\u5185\u751F\u6548\uFF0C\u4E0D\u53D1\u9001\u771F\u5B9E\u901A\u77E5\u3002",
                )}
              />
            )}
            {logoutError && <Alert theme="error" message={logoutError} />}
            {live === "unauthorized" && (
              <Alert
                theme="warning"
                title={tr("\u5B9E\u65F6\u8FDE\u63A5\u8BA4\u8BC1\u5931\u8D25")}
                message={tr(
                  "\u8BF7\u91CD\u65B0\u767B\u5F55\uFF0C\u4EE5\u7EE7\u7EED\u63A5\u6536\u72B6\u6001\u66F4\u65B0\u3002",
                )}
              />
            )}
            <Suspense
              fallback={
                <div className="panel">
                  <Skeleton theme="paragraph" />
                </div>
              }
            >
              <Routes>
                <Route path="/overview" element={<Overview />} />
                <Route path="/vendors" element={<Overview vendorsOnly />} />
                <Route path="/incidents" element={<Incidents />} />
                <Route path="/rules/*" element={<Rules />} />
                <Route path="/channels" element={<Channels />} />
                <Route path="/sources" element={<Operations view="sources" />} />
                <Route
                  path="/deliveries"
                  element={<Operations view="deliveries" />}
                />
                <Route
                  path="/settings/*"
                  element={<Operations view="settings" />}
                />
                <Route path="/" element={<Navigate to="/overview" replace />} />
                <Route
                  path="*"
                  element={
                    <div className="panel">
                      <h1>
                        {tr("\u627E\u4E0D\u5230\u8FD9\u4E2A\u9875\u9762")}
                      </h1>
                      <Button onClick={() => navigate("/overview")}>
                        {tr("\u8FD4\u56DE\u603B\u89C8")}
                      </Button>
                    </div>
                  }
                />
              </Routes>
            </Suspense>
            <footer className="workspace-footer">
              {tr(
                "StatusHub \u00B7 \u5382\u5546\u516C\u5F00\u72B6\u6001\u4E0E\u91C7\u96C6\u5065\u5EB7\u5206\u5F00\u5448\u73B0",
              )}
            </footer>
          </main>
        </Layout>
      </Layout>
    </SessionContext.Provider>
  );
}
export default function App() {
  const loc = useLocation();
  const cache = useQueryClient();
  const [snapshot, setSnapshot] = useState<AccountSession | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  async function reload() {
    try {
      if (demo) {
        const v = await request<Session>("/auth/session");
        setSession(v);
        configure({ tenant: v.tenant.slug });
        return;
      }
      const v = await accountSession();
      setSnapshot(v);
      setError("");
      const selected = currentTenant();
      const w =
        v.workspaces.find((w) => w.slug === selected || w.id === selected) ||
        (!selected && v.workspaces.length === 1 ? v.workspaces[0] : undefined);
      if (w) {
        configure({ tenant: w.slug, csrf: v.csrf_token });
        const u = new URL(window.location.href);
        u.searchParams.set("tenant", w.slug);
        history.replaceState(null, "", u);
        setSession({
          tenant: w,
          identity: {
            actor_id: v.user.id,
            email: v.user.email,
            actor_type: "user",
            role: w.role,
          },
          csrf_token: v.csrf_token,
        });
      } else setSession(null);
    } catch (e) {
      if (e instanceof APIError && e.status === 401) {
        setSnapshot(null);
        setSession(null);
        clearSession();
      } else setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void reload();
    const refresh = () => void reload();
    window.addEventListener("focus", refresh);
    window.addEventListener("statushub-session-expired", refresh);
    return () => {
      window.removeEventListener("focus", refresh);
      window.removeEventListener("statushub-session-expired", refresh);
    };
  }, []);
  const completed = async () => {
    cache.clear();
    await reload();
  };
  if (loading) return <div className="boot">{tr("正在加载账号…")}</div>;
  if (loc.pathname === "/setup") return <SetupPage onComplete={completed} />;
  if (loc.pathname === "/invitation")
    return <InvitationPage snapshot={snapshot} onComplete={completed} />;
  if (loc.pathname === "/forgot-password") return <ForgotPage />;
  if (loc.pathname === "/reset-password") return <ResetPage />;
  if (loc.pathname === "/verify-email")
    return <VerifyEmailPage onComplete={completed} />;
  if (loc.pathname === "/account-flow")
    return (
      <div className="account-flow">
        <Alert
          theme="warning"
          message={tr("此链接属于旧版登录系统，请重新申请邀请或密码重置。")}
        />
        <Button onClick={() => window.location.assign("/ui/")}>
          {tr("返回登录")}
        </Button>
      </div>
    );
  if (error)
    return (
      <div className="account-flow">
        <Alert theme="error" message={error} />
        <Button onClick={() => void reload()}>{tr("重试")}</Button>
      </div>
    );
  if (!snapshot && !demo) return <LoginPage onComplete={completed} />;
  if (snapshot && loc.pathname === "/account")
    return <AccountPage snapshot={snapshot} onComplete={completed} />;
  if (snapshot && (!session || loc.pathname === "/workspaces")) {
    const denied =
      !!currentTenant() &&
      !snapshot.workspaces.some(
        (w) => w.slug === currentTenant() || w.id === currentTenant(),
      );
    return (
      <WorkspacePicker
        snapshot={snapshot}
        denied={denied}
        onComplete={completed}
      />
    );
  }
  return session ? (
    <Workspace
      key={session.tenant.id}
      session={session}
      onLogout={() => {
        setSnapshot(null);
        setSession(null);
      }}
    />
  ) : null;
}
