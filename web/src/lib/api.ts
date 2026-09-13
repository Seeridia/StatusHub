import { tr } from "./i18n";
import type { Page, Session } from "./types";
export const demo =
  import.meta.env?.DEV &&
  new URLSearchParams(location.search).get("demo") === "1";
let tenant =
  new URLSearchParams(location.search).get("tenant") ||
  sessionStorage.getItem("statusmon-tenant") ||
  "";
let token = sessionStorage.getItem("statusmon-token") || "";
let csrf = "";
export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
export function configure(next: {
  tenant: string;
  token?: string;
  csrf?: string;
}) {
  tenant = next.tenant;
  token = next.token ?? token;
  csrf = next.csrf ?? "";
}
export function currentTenant() {
  return tenant;
}
export function authHeaders(): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {};
}
export function persistSession() {
  sessionStorage.setItem("statusmon-tenant", tenant);
  if (token) sessionStorage.setItem("statusmon-token", token);
  else sessionStorage.removeItem("statusmon-token");
}
export function clearSession() {
  token = "";
  csrf = "";
  sessionStorage.removeItem("statusmon-token");
  sessionStorage.removeItem("statusmon-tenant");
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
  if (!response.ok)
    throw new APIError(
      response.status,
      response.status === 401
        ? tr(
            "\u767B\u5F55\u5DF2\u8FC7\u671F\u6216\u51ED\u636E\u65E0\u6548\uFF0C\u8BF7\u91CD\u65B0\u767B\u5F55\u3002",
          )
        : response.status === 403
          ? tr(
              "\u5F53\u524D\u89D2\u8272\u65E0\u6743\u6267\u884C\u6B64\u64CD\u4F5C\u3002",
            )
          : body.detail ||
            tr("\u8BF7\u6C42\u5931\u8D25\uFF08HTTP {{value0}}\uFF09", {
              value0: response.status,
            }),
    );
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
export async function login(workspace: string, secret = "") {
  configure({ tenant: workspace, token: secret });
  const session = secret
    ? await api<Session>("/session")
    : await request<Session>("/auth/session");
  configure({
    tenant: session.tenant.slug || session.tenant.key || workspace,
    csrf: session.csrf_token,
  });
  persistSession();
  return session;
}
export async function restore() {
  return token && tenant ? login(tenant, token) : login(tenant);
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
