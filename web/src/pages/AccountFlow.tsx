import { Form } from "tdesign-react";
import { useState } from "react";
import { Alert, Button, Card, Input, Select } from "tdesign-react";
import { currentLanguage, setLanguage, tr } from "../lib/i18n";
import { currentTenant, clearSession } from "../lib/api";
export async function accountRequest(action: string, body: unknown) {
  const response = await fetch("/auth/team/" + action, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(
      response.status === 429
        ? tr("请求过于频繁，请稍后重试。")
        : tr("操作未完成，请检查凭据、邀请有效期和工作区。"),
    );
  return data as { tenant?: string };
}
export default function AccountFlow() {
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const mode = params.get("mode") || "forgot";
  const token = params.get("token") || "";
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [workspace, setWorkspace] = useState(currentTenant());
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  async function submit(action: string) {
    setBusy(true);
    setError("");
    try {
      const out = await accountRequest(action, {
        email,
        password,
        tenant: workspace,
        token,
        invitation: token,
      });
      if (out.tenant) setWorkspace(out.tenant);
      setPassword("");
      setMessage(
        action === "verify-request" || action === "forgot"
          ? tr("若该邮箱符合条件，系统将发送邮件，请检查收件箱。")
          : tr("操作完成，请登录工作区。"),
      );
      if (action !== "verify-request" && action !== "forgot") {
        history.replaceState(
          null,
          "",
          location.pathname + location.search + "#/account-flow?mode=done",
        );
        setDone(true);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="account-flow">
      <Card title={tr("账号与邀请")}>
        <Select
          aria-label={tr("界面语言")}
          value={currentLanguage()}
          options={[
            { value: "en", label: "English" },
            { value: "zh", label: tr("简体中文") },
          ]}
          onChange={(v) => setLanguage(String(v) as "en" | "zh")}
        />
        <Form className="team-form" labelAlign="top">
          {error && <Alert theme="error" message={error} />}{" "}
          {message && <Alert theme="success" message={message} />}
          {!done && (
            <>
              {(mode === "invite" || mode === "forgot") && (
                <Form.FormItem label={tr("邮箱")}>
                  <Input
                    aria-label={tr("邮箱")}
                    type="text"
                    autocomplete="email"
                    value={email}
                    onChange={setEmail}
                  />
                </Form.FormItem>
              )}
              {mode === "invite" && (
                <>
                  <Alert
                    message={tr(
                      "新成员先验证邮箱；已有邮箱密码账号可输入密码接受邀请。",
                    )}
                  />
                  <Form.FormItem label={tr("密码")}>
                    <Input
                      aria-label={tr("密码")}
                      type="password"
                      value={password}
                      onChange={setPassword}
                    />
                  </Form.FormItem>
                  <Button
                    loading={busy}
                    onClick={() => submit("verify-request")}
                  >
                    {tr("发送验证邮件")}
                  </Button>
                  <Button loading={busy} onClick={() => submit("accept")}>
                    {tr("登录并接受邀请")}
                  </Button>
                  <Form.FormItem label={tr("工作区标识")}>
                    <Input
                      aria-label={tr("工作区标识")}
                      value={workspace}
                      onChange={setWorkspace}
                    />
                  </Form.FormItem>
                  <Button
                    onClick={() => {
                      location.href = `/auth/${encodeURIComponent(workspace)}/login?invitation=${encodeURIComponent(token)}`;
                    }}
                  >
                    {tr("通过 SSO 接受邀请")}
                  </Button>
                </>
              )}
              {(mode === "verify" || mode === "reset") && (
                <>
                  <Form.FormItem label={tr("新密码（12–128 个字符）")}>
                    <Input
                      aria-label={tr("新密码（12–128 个字符）")}
                      type="password"
                      autocomplete="new-password"
                      value={password}
                      onChange={setPassword}
                    />
                  </Form.FormItem>
                  <Button loading={busy} onClick={() => submit(mode)}>
                    {tr("设置密码")}
                  </Button>
                </>
              )}
              {mode === "forgot" && (
                <Button loading={busy} onClick={() => submit("forgot")}>
                  {tr("发送重置邮件")}
                </Button>
              )}
            </>
          )}
          <Button
            variant="text"
            onClick={() => {
              clearSession();
              location.href = "/ui/?tenant=" + encodeURIComponent(workspace);
            }}
          >
            {tr("返回登录")}
          </Button>
        </Form>
      </Card>
    </main>
  );
}
