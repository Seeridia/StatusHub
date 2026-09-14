import { setLanguage, tr, type LanguagePreference } from "./lib/i18n";
import AccountFlow, { accountRequest } from "./pages/AccountFlow";
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
  UserIcon,
} from "tdesign-icons-react";
import {
  APIError,
  api,
  clearSession,
  currentTenant,
  demo,
  login,
  restore,
} from "./lib/api";
import { SessionContext, useLive } from "./lib/hooks";
import { label } from "./lib/model";
import type { Session } from "./lib/types";
const Overview = lazy(() => import("./pages/Overview"));
const Incidents = lazy(() => import("./pages/Incidents"));
const Rules = lazy(() => import("./pages/Rules"));
const Channels = lazy(() => import("./pages/Channels"));
const Operations = lazy(() => import("./pages/Operations"));

function BrandMark({ compact = false }: { compact?: boolean }) {
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

function LanguageSelect() {
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
function Login({ onLogin }: { onLogin: (session: Session) => void }) {
  useEffect(() => {
    document.documentElement.setAttribute("theme-mode", "light");
  }, []);
  const [workspace, setWorkspace] = useState(currentTenant());
  const [secret, setSecret] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function connect() {
    if (!workspace.trim()) {
      setError(tr("\u8BF7\u8F93\u5165\u5DE5\u4F5C\u533A\u6807\u8BC6\u3002"));
      return;
    }
    if (advanced && !secret.trim()) {
      setError(
        tr("\u8BF7\u8F93\u5165\u670D\u52A1\u8D26\u53F7\u4EE4\u724C\u3002"),
      );
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (advanced) {
        onLogin(await login(workspace.trim(), secret.trim()));
      } else {
        await accountRequest("login", {
          tenant: workspace.trim(),
          email,
          password,
        });
        clearSession();
        onLogin(await login(workspace.trim()));
      }
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="signin-page">
      <img
        className="signin-art"
        src={`${import.meta.env.BASE_URL}landing/login-background.jpg`}
        alt=""
        width={3168}
        height={1344}
        fetchPriority="high"
      />
      <a
        className="skip-link"
        href="#login-workspace"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("login-workspace")?.focus();
        }}
      >
        {tr("\u8DF3\u5230\u5DE5\u4F5C\u533A\u767B\u5F55")}
      </a>
      <header className="signin-header">
        <div className="signin-brand">
          <BrandMark />
        </div>
        <div className="signin-header-actions">
          <span className="signin-caption">
            {tr("\u5916\u90E8\u4F9D\u8D56\u72B6\u6001\u76D1\u63A7")}
          </span>
        </div>
      </header>
      <main className="signin-main">
        <section className="signin-hero" aria-labelledby="signin-title">
          <h1 id="signin-title">{tr("登录 StatusHub")}</h1>
          <p className="signin-intro">
            {tr("登录工作区，掌握服务状态与重要通知。")}
          </p>
          <section
            className="signin-login"
            id="login-workspace"
            tabIndex={-1}
            aria-label={tr("\u767B\u5F55\u5DE5\u4F5C\u533A")}
          >
            <Form
              className={advanced ? "signin-form is-token" : "signin-form"}
              labelAlign="top"
              layout="vertical"
              onSubmit={() => void connect()}
            >
              <div className="signin-fields">
                <FormField
                  label={tr("\u5DE5\u4F5C\u533A\u6807\u8BC6")}
                  name="workspace"
                >
                  <Input
                    aria-label={tr("\u5DE5\u4F5C\u533A\u6807\u8BC6")}
                    value={workspace}
                    onChange={setWorkspace}
                    placeholder={tr("\u4F8B\u5982 local")}
                    size="large"
                  />
                </FormField>
                {!advanced && (
                  <>
                    <FormField label={tr("邮箱")} name="email">
                      <Input
                        aria-label={tr("邮箱")}
                        ref={(input) =>
                          input?.inputElement?.setAttribute(
                            "aria-label",
                            tr("邮箱"),
                          )
                        }
                        value={email}
                        onChange={setEmail}
                        autocomplete="email"
                        size="large"
                      />
                    </FormField>
                    <FormField label={tr("密码")} name="password">
                      <Input
                        aria-label={tr("密码")}
                        ref={(input) =>
                          input?.inputElement?.setAttribute(
                            "aria-label",
                            tr("密码"),
                          )
                        }
                        type="password"
                        value={password}
                        onChange={setPassword}
                        autocomplete="current-password"
                        size="large"
                      />
                    </FormField>
                  </>
                )}
                {advanced && (
                  <FormField
                    label={tr("\u670D\u52A1\u8D26\u53F7\u4EE4\u724C")}
                    name="token"
                  >
                    <Input
                      aria-label={tr("\u670D\u52A1\u8D26\u53F7\u4EE4\u724C")}
                      type="password"
                      value={secret}
                      onChange={setSecret}
                      placeholder="sa.…"
                      size="large"
                      autocomplete="off"
                    />
                  </FormField>
                )}
              </div>
              <Button
                className="signin-submit"
                size="large"
                type="submit"
                loading={busy}
              >
                {advanced
                  ? tr("\u8FDE\u63A5\u5DE5\u4F5C\u533A")
                  : tr("邮箱密码登录")}
                <ChevronRightIcon aria-hidden="true" />
              </Button>
              {error && (
                <div className="signin-error" role="alert">
                  <Alert theme="error" message={error} />
                </div>
              )}
            </Form>
            <div className="signin-options">
              <Button
                variant="text"
                onClick={() => {
                  location.href = `/auth/${encodeURIComponent(workspace.trim())}/login`;
                }}
              >
                {tr("通过 SSO 登录")}
              </Button>
              <Button
                variant="text"
                onClick={() => {
                  location.hash = "/account-flow?mode=forgot";
                }}
              >
                {tr("忘记密码")}
              </Button>
              <Button
                variant="text"
                theme="primary"
                className="signin-switch"
                onClick={() => {
                  setAdvanced(!advanced);
                  setError("");
                }}
              >
                {advanced
                  ? tr("邮箱密码登录")
                  : tr("\u4F7F\u7528\u670D\u52A1\u8D26\u53F7\u767B\u5F55")}
                <ChevronRightIcon aria-hidden="true" />
              </Button>
            </div>
            {advanced && (
              <p className="signin-account-note">
                {tr(
                  "\u5DE5\u4F5C\u533A\u7531\u7BA1\u7406\u5458\u521B\u5EFA\uFF0C\u4F7F\u7528\u5DF2\u5206\u914D\u7684\u670D\u52A1\u8D26\u53F7\u4EE4\u724C\u767B\u5F55\u3002",
                )}
              </p>
            )}
          </section>
        </section>
      </main>
    </div>
  );
}
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
      if (!demo) await api("/logout", { method: "POST", body: "{}" });
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
  const accountLocation = useLocation();
  const [session, setSession] = useState<Session | null>(null);
  const [booting, setBooting] = useState(true);
  useEffect(() => {
    let active = true;
    void restore()
      .then((value) => {
        if (active) setSession(value);
      })
      .catch(() => clearSession())
      .finally(() => {
        if (active) setBooting(false);
      });
    return () => {
      active = false;
    };
  }, []);
  if (accountLocation.pathname === "/account-flow") return <AccountFlow />;
  if (booting)
    return (
      <div className="boot">
        {tr("\u6B63\u5728\u8FDE\u63A5\u5DE5\u4F5C\u533A\u2026")}
      </div>
    );
  return session ? (
    <Workspace session={session} onLogout={() => setSession(null)} />
  ) : (
    <Login onLogin={setSession} />
  );
}
