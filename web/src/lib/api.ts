import { tr } from "./i18n";
import type { Page, Session } from "./types";
export const demo =
  import.meta.env?.DEV &&
  new URLSearchParams(location.search).get("demo") === "1";
let tenant = new URLSearchParams(location.search).get("tenant") || "";
let csrf = "";
export interface AccountSession {
  user: { id: string; email: string; name: string; email_verified_at?: string };
  workspaces: { id: string; slug: string; name: string; role: string }[];
  csrf_token: string;
  mail_configured: boolean;
  platform_admin: boolean;
}
export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export function configure(next: { tenant?: string; csrf?: string }) {
  if (next.tenant !== undefined) tenant = next.tenant;
  if (next.csrf !== undefined) csrf = next.csrf;
}
export function currentTenant() {
  return tenant;
}
export function authHeaders(): Record<string, string> {
  return {};
}
export function clearSession() {
  csrf = "";
}
export async function accountSession() {
  const snapshot = await request<AccountSession>("/auth/session");
  csrf = snapshot.csrf_token;
  return snapshot;
}
export function accountRequest<T = unknown>(
  action: string,
  body: unknown = {},
) {
  return request<T>("/auth/" + action, {
    method: "POST",
    body: JSON.stringify(body),
  });
}
export function platformRequest<T = unknown>(
  path: string,
  init: RequestInit = {},
) {
  return request<T>(`/admin/v1${path}`, init);
}
export function enterWorkspace(w: AccountSession["workspaces"][number]) {
  const url = new URL(location.href);
  url.searchParams.set("tenant", w.slug);
  url.hash = "/overview";
  location.assign(url);
}
export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  if (demo)
    return (await import("./demo")).demoRequest(path, init) as Promise<T>;
  const writing = !!init.method && init.method !== "GET";
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: {
      Accept: "application/json",
      ...authHeaders(),
      ...(writing
        ? { "Content-Type": "application/json", "X-CSRF-Token": csrf }
        : {}),
      ...init.headers,
    },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const messages: Record<string, string> = {
      invalid_credentials: tr("邮箱或密码不正确。"),
      origin_mismatch: tr("服务公开地址与当前页面不一致，请联系管理员。"),
      session_expired: tr("会话已过期，请重新登录。"),
      unauthenticated: tr("会话已过期，请重新登录。"),
      workspace_forbidden: tr("你没有此工作区的访问权限。"),
      platform_forbidden: tr("当前账号没有平台管理权限。"),
      last_platform_admin: tr("必须保留至少一名有效的平台管理员。"),
      platform_self_disable: tr("平台管理员不能停用自己的账号。"),
      workspace_admin_disable: tr("请先转移该用户负责的工作区管理员，再停用账号。"),
      invitation_invalid: tr("邀请已过期、撤销或接受，请联系管理员重新邀请。"),
      setup_unavailable: tr("初始化链接已失效，或实例已经初始化。"),
      invalid_password: tr("密码必须包含 12–128 个字符。"),
      login_required: tr("请先使用受邀邮箱登录，再接受邀请。"),
      email_mismatch: tr("当前登录邮箱与邀请邮箱不一致。"),
      membership_exists: tr("成员关系已存在，请联系管理员调整权限。"),
      token_invalid: tr("链接已过期或已使用，请重新申请。"),
      csrf_failed: tr("页面凭据已过期，请刷新后重试。"),
      rate_limited: tr("请求过于频繁，请稍后重试。"),
      mail_unavailable: tr("邮件服务未配置或不可用，请联系管理员。"),
      auth_unavailable: tr("认证服务暂时不可用，请稍后重试。"),
      unsafe_source_url: tr("请输入可公开访问的 HTTPS 状态页地址。"),
      source_dns_failure: tr("无法解析该状态页的域名。"),
      source_connection_failure: tr("无法连接到该状态页。"),
      source_tls_failure: tr("该状态页的 HTTPS 证书或连接无效。"),
      source_probe_timeout: tr("状态页响应超时，请稍后重试。"),
      source_response_too_large: tr("状态页返回的内容过大，无法安全检测。"),
      unsupported_source: tr("没有在该地址检测到受支持的状态页接口。"),
      source_probe_failed: tr("暂时无法检测该状态页。"),
    };
    const code =
      String(body.type || "")
        .split("/")
        .pop() || "";
    if (response.status === 401 && !path.startsWith("/auth/"))
      window.dispatchEvent(new Event("statushub-session-expired"));
    throw new APIError(
      response.status,
      (messages[code] || body.detail || tr("请求失败")) +
        (body.request_id ? " (" + body.request_id + ")" : ""),
    );
  }
  return body;
}
export function api<T>(path: string, init?: RequestInit) {
  return request<T>(`/v1/tenants/${encodeURIComponent(tenant)}${path}`, init);
}
// A logical mutation retains its key until it succeeds or receives a definitive 4xx.
export function createWriter() {
  let pending:
    | {
        signature: string;
        key: string;
      }
    | undefined;
  return async <T>(
    path: string,
    body: unknown,
    method = "POST",
  ): Promise<T> => {
    const encoded = JSON.stringify(body);
    const signature = JSON.stringify([tenant, path, method, encoded]);
    if (!pending || pending.signature !== signature)
      pending = { signature, key: crypto.randomUUID() };
    try {
      const result = await api<T>(path, {
        method,
        body: encoded,
        headers: { "Idempotency-Key": pending.key },
      });
      pending = undefined;
      return result;
    } catch (error) {
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500 &&
        error.status !== 409 &&
        error.status !== 429
      )
        pending = undefined;
      throw error;
    }
  };
}
export async function allPages<T>(path: string, signal?: AbortSignal) {
  const result: T[] = [];
  let cursor = "";
  do {
    const separator = path.includes("?") ? "&" : "?";
    const page = await api<Page<T>>(
      `${path}${separator}limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      { signal },
    );
    result.push(...(page.data || []));
    cursor = page.next_cursor || "";
  } while (cursor);
  return result;
}
