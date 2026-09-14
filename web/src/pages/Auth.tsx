import { useEffect, useState, type ReactNode } from "react";
import { Alert, Button, Input, Form, Card } from "tdesign-react";
import { useNavigate } from "react-router-dom";
import {
  accountRequest,
  type AccountSession,
  enterWorkspace,
} from "../lib/api";
import { tr } from "../lib/i18n";
import { BrandMark, LanguageSelect } from "../App";

type Done = { onComplete: () => Promise<void> };
function token() {
  return (
    new URLSearchParams(location.hash.split("?")[1] || "").get("token") || ""
  );
}
function Frame({ title, children }: { title: string; children: ReactNode }) {
  useEffect(() => {
    document.documentElement.setAttribute("theme-mode", "light");
  }, []);
  return (
    <div className="signin-page">
      <img
        className="signin-art"
        src={`${import.meta.env.BASE_URL}landing/login-background.jpg`}
        alt=""
      />
      <header className="signin-header">
        <a href="/ui/" aria-label="StatusHub">
          <BrandMark />
        </a>
        <LanguageSelect />
      </header>
      <main className="signin-main">
        <section className="signin-hero">
          <h1>{title}</h1>
          <div className="signin-login">{children}</div>
        </section>
      </main>
    </div>
  );
}
function Feedback({ error, message }: { error: string; message?: string }) {
  return (
    <>
      {error && <Alert theme="error" message={error} />}
      {message && <Alert theme="success" message={message} />}
    </>
  );
}
function Password({
  value,
  onChange,
  label = tr("密码"),
  fresh = false,
}: {
  value: string;
  onChange: (v: string) => void;
  label?: string;
  fresh?: boolean;
}) {
  return (
    <Form.FormItem label={label}>
      <Input
        name={fresh ? "new-password" : "password"}
        type="password"
        value={value}
        onChange={onChange}
        autocomplete={fresh ? "new-password" : "current-password"}
        placeholder={label}
        aria-label={label}
        size="large"
      />
    </Form.FormItem>
  );
}
function Email({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <Form.FormItem label={tr("邮箱")}>
      <Input
        name="email"
        value={value}
        onChange={onChange}
        autocomplete="username"
        placeholder={tr("邮箱")}
        aria-label={tr("邮箱")}
        size="large"
      />
    </Form.FormItem>
  );
}
export function LoginPage({ onComplete }: Done) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("login", { email, password });
      setPassword("");
      await onComplete();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Frame title={tr("登录 StatusHub")}>
      <Form
        className="team-form"
        labelAlign="top"
        onSubmit={() => void submit()}
      >
        <Email value={email} onChange={setEmail} />
        <Password value={password} onChange={setPassword} />
        <Feedback error={error} />
        <Button block size="large" type="submit" loading={busy}>
          {tr("登录")}
        </Button>
      </Form>
      <Button variant="text" href="#/forgot-password">
        {tr("忘记密码")}
      </Button>
    </Frame>
  );
}
export function SetupPage({ onComplete }: Done) {
  const nav = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("StatusHub");
  const [slug, setSlug] = useState("main");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("setup", {
        token: token(),
        email,
        password,
        name,
        slug,
      });
      setPassword("");
      history.replaceState(null, "", "/ui/#/overview");
      await onComplete();
      nav("/overview", { replace: true });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Frame title={tr("初始化 StatusHub")}>
      <Form
        className="team-form"
        labelAlign="top"
        onSubmit={() => void submit()}
      >
        <Email value={email} onChange={setEmail} />
        <Password
          fresh
          value={password}
          onChange={setPassword}
          label={tr("新密码（12–128 个字符）")}
        />
        <Form.FormItem label={tr("工作区名称")}>
          <Input value={name} onChange={setName} />
        </Form.FormItem>
        <Form.FormItem label={tr("工作区标识")}>
          <Input value={slug} onChange={setSlug} />
        </Form.FormItem>
        <Feedback error={error} />
        <Button type="submit" loading={busy}>
          {tr("创建管理员与工作区")}
        </Button>
      </Form>
    </Frame>
  );
}
export function ForgotPage() {
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("password/forgot", { email });
      setMessage(tr("若该邮箱符合条件，系统将发送邮件，请检查收件箱。"));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Frame title={tr("找回密码")}>
      <Form
        className="team-form"
        labelAlign="top"
        onSubmit={() => void submit()}
      >
        <Email value={email} onChange={setEmail} />
        <Feedback error={error} message={message} />
        <Button type="submit" loading={busy}>
          {tr("发送重置邮件")}
        </Button>
        <Button href="/ui/" variant="text">
          {tr("返回登录")}
        </Button>
      </Form>
    </Frame>
  );
}
export function ResetPage() {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("password/reset", { token: token(), password });
      setPassword("");
      history.replaceState(null, "", "/ui/#/reset-password");
      setDone(true);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Frame title={tr("重置密码")}>
      {done ? (
        <>
          <Alert
            theme="success"
            message={tr("密码已重置，请使用新密码登录。")}
          />
          <Button href="/ui/">{tr("返回登录")}</Button>
        </>
      ) : (
        <Form
          className="team-form"
          labelAlign="top"
          onSubmit={() => void submit()}
        >
          <Password
            fresh
            value={password}
            onChange={setPassword}
            label={tr("新密码（12–128 个字符）")}
          />
          <Feedback error={error} />
          <Button type="submit" loading={busy}>
            {tr("设置密码")}
          </Button>
        </Form>
      )}
    </Frame>
  );
}
export function InvitationPage({
  snapshot,
  onComplete,
}: Done & { snapshot: AccountSession | null }) {
  const [preview, setPreview] = useState<{
    email: string;
    existing_user: boolean;
    workspace: AccountSession["workspaces"][number];
  } | null>(null);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  useEffect(() => {
    accountRequest<typeof preview>("invitations/preview", { token: token() })
      .then(setPreview)
      .catch((e) => setError(e.message));
  }, []);
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("invitations/accept", {
        token: token(),
        password: preview?.existing_user ? "" : password,
      });
      setPassword("");
      await onComplete();
      if (preview) enterWorkspace(preview.workspace);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const mismatch = snapshot && snapshot.user.email !== preview?.email;
  return (
    <Frame title={tr("加入工作区")}>
      <Feedback error={error} />
      {preview && (
        <>
          <p>
            {preview.workspace.name} · {preview.workspace.role}
          </p>
          <p>{preview.email}</p>
          {mismatch ? (
            <>
              <Alert message={tr("当前登录邮箱与邀请邮箱不一致。")} />
              <Button
                onClick={async () => {
                  try {
                    await accountRequest("logout");
                    await onComplete();
                  } catch (e) {
                    setError((e as Error).message);
                  }
                }}
              >
                {tr("退出登录")}
              </Button>
            </>
          ) : preview.existing_user && !snapshot ? (
            <>
              <Alert message={tr("请先使用受邀邮箱登录，再接受邀请。")} />
              <LoginInline email={preview.email} onComplete={onComplete} />
            </>
          ) : (
            <Form
              className="team-form"
              labelAlign="top"
              onSubmit={() => void submit()}
            >
              {!preview.existing_user && (
                <Password
                  fresh
                  value={password}
                  onChange={setPassword}
                  label={tr("新密码（12–128 个字符）")}
                />
              )}
              <Button type="submit" loading={busy}>
                {tr("接受邀请")}
              </Button>
            </Form>
          )}
        </>
      )}
      <Button variant="text" onClick={() => nav("/overview")}>
        {tr("返回登录")}
      </Button>
    </Frame>
  );
}
function LoginInline({ email, onComplete }: Done & { email: string }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest("login", { email, password });
      setPassword("");
      await onComplete();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Form className="team-form" labelAlign="top" onSubmit={() => void submit()}>
      <Password value={password} onChange={setPassword} />
      <Feedback error={error} />
      <Button type="submit" loading={busy}>
        {tr("登录")}
      </Button>
    </Form>
  );
}
export function WorkspacePicker({
  snapshot,
  denied,
  onComplete,
}: Done & { snapshot: AccountSession; denied: boolean }) {
  const [error, setError] = useState("");
  return (
    <Frame title={tr("选择工作区")}>
      <Feedback error={error} />
      {denied && (
        <Alert theme="warning" message={tr("你没有此工作区的访问权限。")} />
      )}
      {!snapshot.workspaces.length && (
        <Alert message={tr("暂无可访问工作区，请联系管理员邀请。")} />
      )}
      <div className="auth-workspaces">
        {snapshot.workspaces.map((w) => (
          <Button
            key={w.id}
            block
            variant="outline"
            onClick={() => enterWorkspace(w)}
          >
            {w.name} · {w.role}
          </Button>
        ))}
      </div>
      <Button href="#/account" variant="text">
        {tr("个人账号")}
      </Button>
      <Button
        variant="text"
        onClick={async () => {
          try {
            await accountRequest("logout");
            await onComplete();
          } catch (e) {
            setError((e as Error).message);
          }
        }}
      >
        {tr("退出登录")}
      </Button>
    </Frame>
  );
}
export function AccountPage({
  snapshot,
  onComplete,
}: Done & { snapshot: AccountSession }) {
  const [old, setOld] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function run(action: string) {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await accountRequest(
        action,
        action === "password/change" ? { old_password: old, password } : {},
      );
      setOld("");
      setPassword("");
      if (action === "email/request")
        setMessage(tr("若该邮箱符合条件，系统将发送邮件，请检查收件箱。"));
      else await onComplete();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Frame title={tr("个人账号")}>
      <p>{snapshot.user.email}</p>
      <Feedback error={error} message={message} />
      {!snapshot.user.email_verified_at && (
        <Button
          disabled={!snapshot.mail_configured}
          onClick={() => void run("email/request")}
        >
          {tr("发送验证邮件")}
        </Button>
      )}
      <Form
        className="team-form"
        labelAlign="top"
        onSubmit={() => void run("password/change")}
      >
        <Password value={old} onChange={setOld} label={tr("原密码")} />
        <Password
          fresh
          value={password}
          onChange={setPassword}
          label={tr("新密码（12–128 个字符）")}
        />
        <Button type="submit" loading={busy}>
          {tr("修改密码")}
        </Button>
      </Form>
      <Button variant="text" onClick={() => void run("logout-all")}>
        {tr("退出全部会话")}
      </Button>
      <Button variant="text" href="#/workspaces">
        {tr("返回工作区")}
      </Button>
    </Frame>
  );
}
export function VerifyEmailPage({ onComplete }: Done) {
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Frame title={tr("验证邮箱")}>
      <Feedback error={error} message={done ? tr("邮箱已验证。") : ""} />
      {!done && (
        <Button
          loading={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await accountRequest("email/verify", { token: token() });
              setDone(true);
              history.replaceState(null, "", "/ui/#/verify-email");
              await onComplete();
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {tr("验证邮箱")}
        </Button>
      )}
      <Button href="/ui/" variant="text">
        {tr("返回登录")}
      </Button>
    </Frame>
  );
}
