import { currentLanguage, tr } from "./i18n";
import type { Scope, Vendor } from "./types";
export const labels: Record<string, string> = {
  operational: tr("\u8FD0\u884C\u6B63\u5E38"),
  healthy: tr("\u6B63\u5E38"),
  degraded: tr("\u6027\u80FD\u964D\u7EA7"),
  partial_outage: tr("\u90E8\u5206\u4E2D\u65AD"),
  major_outage: tr("\u4E25\u91CD\u4E2D\u65AD"),
  maintenance: tr("\u7EF4\u62A4\u4E2D"),
  under_maintenance: tr("\u7EF4\u62A4\u4E2D"),
  unknown: tr("\u672A\u77E5"),
  stale: tr("\u6570\u636E\u8FC7\u671F"),
  investigating: tr("\u8C03\u67E5\u4E2D"),
  identified: tr("\u5DF2\u5B9A\u4F4D"),
  monitoring: tr("\u89C2\u5BDF\u4E2D"),
  resolved: tr("\u5DF2\u6062\u590D"),
  scheduled: tr("\u8BA1\u5212\u7EF4\u62A4"),
  in_progress: tr("\u7EF4\u62A4\u4E2D"),
  completed: tr("\u5DF2\u5B8C\u6210"),
  none: tr("\u65E0\u5F71\u54CD"),
  minor: tr("\u8F7B\u5FAE"),
  major: tr("\u91CD\u5927"),
  critical: tr("\u4E25\u91CD"),
  succeeded: tr("\u6210\u529F"),
  accepted: tr("\u6E20\u9053\u5DF2\u63A5\u53D7"),
  delivered: tr("\u5DF2\u9001\u8FBE"),
  failed: tr("\u5931\u8D25"),
  dead_letter: tr("\u5DF2\u505C\u6B62\u91CD\u8BD5"),
  retry_wait: tr("\u7B49\u5F85\u91CD\u8BD5"),
  pending: tr("\u5F85\u5904\u7406"),
  queued: tr("\u6392\u961F\u4E2D"),
  running: tr("\u6267\u884C\u4E2D"),
  sending: tr("\u53D1\u9001\u4E2D"),
  disabled: tr("\u5DF2\u505C\u7528"),
  enabled: tr("\u5DF2\u542F\u7528"),
  shadow: tr("\u5F71\u5B50\u8FD0\u884C"),
  promoted: tr("\u5DF2\u664B\u7EA7"),
  rolled_back: tr("\u5DF2\u56DE\u6EDA"),
  "incident.created": tr("\u4E8B\u4EF6\u521B\u5EFA"),
  "incident.updated": tr("\u4E8B\u4EF6\u66F4\u65B0"),
  "incident.resolved": tr("\u4E8B\u4EF6\u6062\u590D"),
  "component.status_changed": tr("\u670D\u52A1\u72B6\u6001\u53D8\u5316"),
  "source.degraded": tr("\u91C7\u96C6\u964D\u7EA7"),
  "source.recovered": tr("\u91C7\u96C6\u6062\u590D"),
  slack: "Slack",
  lark: tr("飞书"),
  smtp: tr("SMTP 邮件"),
  generic_webhook: "Webhook",
  viewer: tr("\u53EA\u8BFB\u6210\u5458"),
  operator: tr("\u64CD\u4F5C\u5458"),
  admin: tr("\u7BA1\u7406\u5458"),
  owner: tr("\u6240\u6709\u8005"),
};
export const label = (value?: string) => (value ? labels[value] || value : "—");
export function tone(
  value?: string,
): "success" | "warning" | "danger" | "primary" | "default" {
  if (
    [
      "operational",
      "healthy",
      "succeeded",
      "accepted",
      "delivered",
      "resolved",
      "enabled",
      "promoted",
      "completed",
    ].includes(value || "")
  )
    return "success";
  if (
    ["critical", "major_outage", "failed", "dead_letter"].includes(value || "")
  )
    return "danger";
  if (
    [
      "major",
      "minor",
      "degraded",
      "partial_outage",
      "stale",
      "investigating",
      "identified",
      "retry_wait",
    ].includes(value || "")
  )
    return "warning";
  if (
    [
      "monitoring",
      "running",
      "sending",
      "shadow",
      "maintenance",
      "scheduled",
      "in_progress",
      "under_maintenance",
    ].includes(value || "")
  )
    return "primary";
  return "default";
}
export function stale(vendor: Vendor, now = Date.now()) {
  const c = vendor.collection;
  if (!c) return vendor.source_health_state === "degraded";
  if (c.state === "disabled") return false;
  return (
    c.state === "stale" ||
    (c.state === "fresh" && !!c.fresh_until && now >= Date.parse(c.fresh_until))
  );
}

export function collectionLabel(value?: string) {
  const names: Record<string, string> = {
    rate_limited: tr("上游限流，请等待退避后重试"),
    access_denied: tr("上游拒绝访问，请检查来源权限"),
    upstream_server: tr("上游服务错误，请等待恢复"),
    upstream_http: tr("上游 HTTP 错误，请检查状态页地址"),
    timeout: tr("请求超时，请检查网络和上游可用性"),
    network: tr("网络连接异常，请检查 DNS、证书和连通性"),
    invalid_payload: tr("响应解析异常，请检查适配器兼容性"),
    unsupported_source: tr("来源或操作不受支持，请重新探测适配器"),
    cancelled: tr("采集被取消，请检查 worker 运行状态"),
    unclassified: tr("未分类错误，请查看采集日志"),
    fresh: tr("按计划采集"),
    stale: tr("采集已逾期"),
    unknown: tr("等待资源检查点"),
    disabled: tr("已停用"),
    no_enabled_sources: tr("没有启用的数据源"),
    on_schedule: tr("按计划采集"),
    awaiting_first_success: tr("等待首次成功采集"),
    awaiting_resource_checkpoint: tr("等待资源检查点"),
    resource_overdue: tr("部分资源超过新鲜度期限"),
    failure_backoff: tr("失败后退避，具体原因请查采集日志"),
    collection_degraded: tr("采集健康异常"),
    active: tr("事件活跃期"),
    hot: tr("近期变化期"),
    warm: tr("观察期"),
    stable: tr("稳定期"),
    backoff: tr("失败退避"),
    cadence: tr("常规调度"),
    cache_policy: tr("上游缓存策略"),
    retry_after: tr("上游要求延后"),
    summary: tr("状态概览"),
    components: tr("服务组件"),
    incidents: tr("事件记录"),
    unresolved_incidents: tr("未结束事件"),
    scheduled_maintenances: tr("计划维护"),
    status: tr("服务状态"),
  };
  return value ? names[value] || value : "—";
}
export function relative(value?: string, now = Date.now()) {
  if (!value) return tr("\u5C1A\u65E0\u6210\u529F\u91C7\u96C6");
  const minutes = Math.max(0, Math.floor((now - Date.parse(value)) / 60000));
  if (!Number.isFinite(minutes)) return tr("\u65F6\u95F4\u672A\u77E5");
  if (!minutes) return tr("\u521A\u521A");
  if (minutes < 60) return tr("minutesAgo", { count: minutes });
  if (minutes < 1440)
    return tr("hoursAgo", {
      count: Math.floor(minutes / 60),
    });
  return tr("daysAgo", { count: Math.floor(minutes / 1440) });
}
export function fmt(value?: string) {
  return value
    ? new Intl.DateTimeFormat(currentLanguage() === "zh" ? "zh-CN" : "en-US", {
        dateStyle: "medium",
        timeStyle: "medium",
        hour12: false,
      }).format(new Date(value))
    : "—";
}
export const impactOptions = ["unknown", "minor", "major", "critical"].map(
  (value) => ({
    value,
    label:
      value === "unknown"
        ? tr("\u6240\u6709\u5F71\u54CD\u7EA7\u522B")
        : tr("{{value0}}\u53CA\u4EE5\u4E0A", { value0: label(value) }),
  }),
);
export const eventOptions = [
  "incident.created",
  "incident.updated",
  "incident.resolved",
  "component.status_changed",
  "source.degraded",
  "source.recovered",
].map((value) => ({ value, label: label(value) }));
export function buildScopes(vendors: string[], events: string[]): Scope[] {
  if (!vendors.length && !events.length) return [];
  return (vendors.length ? vendors : [""]).flatMap((vendor) =>
    (events.length ? events : [""]).map((event) => ({
      ...(vendor ? { vendor_id: vendor } : {}),
      ...(event ? { event_kind: event } : {}),
    })),
  );
}
export function editableScopes(scopes: Scope[]) {
  if (scopes.some((s) => s.component_id || s.component_key || s.tag))
    return false;
  const vendors = [...new Set(scopes.map((s) => s.vendor_id || ""))];
  const events = [...new Set(scopes.map((s) => s.event_kind || ""))];
  if (
    (vendors.includes("") && vendors.length > 1) ||
    (events.includes("") && events.length > 1)
  )
    return false;
  const expected = buildScopes(vendors.filter(Boolean), events.filter(Boolean));
  const key = (s: Scope) => `${s.vendor_id || ""}|${s.event_kind || ""}`;
  return (
    (expected.length === scopes.length &&
      expected.every((s) => scopes.some((t) => key(s) === key(t)))) ||
    scopes.length === 0
  );
}
