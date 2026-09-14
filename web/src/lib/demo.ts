import { tr } from "./i18n";
// Development-only fixture adapter. Production builds eliminate the import branch.
// Every incident below is fictional and must only be shown with the demo banner.
import type {
  Delivery,
  Endpoint,
  Incident,
  Source,
  Subscription,
  Vendor,
} from "./types";
const ago = (minutes: number) =>
  new Date(Date.now() - minutes * 60000).toISOString();
const uuid = (n: number) =>
  `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const vendors: Vendor[] = [
  "OpenAI",
  "Anthropic",
  "AWS",
  "Cloudflare",
  "GitHub",
].map((name, i) => ({
  id: uuid(i + 1),
  name,
  slug: name.toLowerCase(),
  status: i === 0 ? "degraded" : i === 1 ? "partial_outage" : "operational",
  active_incidents: i < 2 ? 1 : 0,
  visible_sources: i === 2 ? 3 : 1,
  last_observed_at: ago(i === 4 ? 18 : 1),
  last_successful_at: ago(i === 4 ? 18 : 1),
  source_health_state: i === 4 ? "degraded" : "healthy",
  collection: {
    state: i === 4 ? "stale" : "fresh",
    reason: i === 4 ? "collection_degraded" : "on_schedule",
    evaluated_at: ago(0),
    fresh_until: ago(i === 4 ? 10 : -6),
    next_poll_at: ago(-4),
  },
}));
const incidents: Incident[] = [
  {
    id: uuid(11),
    source_id: uuid(31),
    vendor_id: uuid(1),
    vendor_slug: "openai",
    vendor_name: "OpenAI",
    name: tr(
      "\u90E8\u5206 API \u8BF7\u6C42\u5EF6\u8FDF\u5347\u9AD8\uFF08\u6F14\u793A\u4E8B\u4EF6\uFF09",
    ),
    impact: "minor",
    phase: "monitoring",
    started_at: ago(42),
    observed_at: ago(40),
    source_updated_at: ago(5),
    updated_at: ago(5),
    updates: [
      {
        id: uuid(111),
        phase: "monitoring",
        body: tr(
          "示例更新：缓解措施已生效，正在持续观察请求延迟。此内容为设计演示，不代表服务真实状态。",
        ),
        source_updated_at: ago(5),
        observed_at: ago(4),
      },
      {
        id: uuid(112),
        phase: "identified",
        body: tr(
          "\u793A\u4F8B\u66F4\u65B0\uFF1A\u5DF2\u5B9A\u4F4D\u5230\u53D7\u5F71\u54CD\u7684\u670D\u52A1\uFF0C\u6B63\u5728\u8FDB\u884C\u6062\u590D\u3002",
        ),
        source_updated_at: ago(26),
        observed_at: ago(25),
      },
      {
        id: uuid(113),
        phase: "investigating",
        body: tr(
          "\u793A\u4F8B\u66F4\u65B0\uFF1A\u6B63\u5728\u8C03\u67E5\u90E8\u5206\u8BF7\u6C42\u5EF6\u8FDF\u5347\u9AD8\u7684\u95EE\u9898\u3002",
        ),
        source_updated_at: ago(42),
        observed_at: ago(40),
      },
    ],
  },
  {
    id: uuid(12),
    source_id: uuid(32),
    vendor_id: uuid(2),
    vendor_slug: "anthropic",
    vendor_name: "Anthropic",
    name: tr(
      "\u90E8\u5206\u6A21\u578B\u8BF7\u6C42\u9519\u8BEF\u7387\u5347\u9AD8\uFF08\u6F14\u793A\u4E8B\u4EF6\uFF09",
    ),
    impact: "major",
    phase: "investigating",
    started_at: ago(16),
    observed_at: ago(15),
    source_updated_at: ago(8),
    updated_at: ago(8),
  },
  {
    id: uuid(13),
    source_id: uuid(34),
    vendor_id: uuid(4),
    vendor_slug: "cloudflare",
    vendor_name: "Cloudflare",
    name: tr(
      "\u533A\u57DF\u7F51\u7EDC\u8FDE\u63A5\u5DF2\u6062\u590D\uFF08\u6F14\u793A\u4E8B\u4EF6\uFF09",
    ),
    impact: "none",
    phase: "resolved",
    observed_at: ago(180),
    updated_at: ago(62),
  },
];
const endpoints: Endpoint[] = [
  {
    id: uuid(21),
    name: tr("\u751F\u4EA7\u544A\u8B66"),
    channel: "slack",
    enabled: true,
    health_state: "healthy",
    secret_version: 1,
    key_id: "demo",
    updated_at: ago(1440),
  },
  {
    id: uuid(22),
    name: tr("\u5185\u90E8\u4E8B\u4EF6\u4E2D\u5FC3"),
    channel: "generic_webhook",
    enabled: true,
    health_state: "healthy",
    secret_version: 2,
    key_id: "demo",
    updated_at: ago(2880),
  },
];
const subscriptions: Subscription[] = [
  {
    id: uuid(41),
    name: tr("\u751F\u4EA7\u73AF\u5883 \u00B7 AI \u670D\u52A1"),
    enabled: true,
    rule_version: 1,
    rule: { minimum_impact: "unknown" },
    scopes: [{ vendor_id: uuid(1) }, { vendor_id: uuid(2) }],
    endpoint_ids: [uuid(21)],
    updated_at: ago(60),
  },
  {
    id: uuid(42),
    name: tr("\u57FA\u7840\u8BBE\u65BD\u91CD\u5927\u4E8B\u4EF6"),
    enabled: true,
    rule_version: 2,
    rule: { minimum_impact: "major" },
    scopes: [{ vendor_id: uuid(3) }, { vendor_id: uuid(4) }],
    endpoint_ids: [uuid(21), uuid(22)],
    updated_at: ago(1440),
  },
];
const sources: Source[] = vendors.map((v, i) => ({
  id: uuid(31 + i),
  vendor_id: v.id,
  vendor_name: v.name,
  canonical_url: `https://status.${v.slug}.example.com`,
  adapter_name: "statuspage",
  adapter_version: "v1",
  health_state: v.source_health_state,
  enabled: true,
  failure_streak: i === 4 ? 3 : 0,
  last_success_at: v.last_successful_at,
  last_attempt_at: ago(1),
  next_poll_at: ago(-4),
  collection: {
    ...v.collection!,
    mode: i === 4 ? "backoff" : "stable",
    resources: [
      {
        kind: "summary",
        state: i === 4 ? "stale" : "fresh",
        last_success_at: v.last_successful_at,
        next_poll_at: ago(-4),
        fresh_until: ago(-6),
        schedule_reason: "cadence",
      },
    ],
  },
  ...(i === 4 ? { tenant_id: "demo" } : {}),
}));
const deliveries: Delivery[] = [
  {
    id: uuid(51),
    endpoint_name: tr("\u751F\u4EA7\u544A\u8B66"),
    subscription_name: subscriptions[0].name,
    event_kind: "incident.updated",
    channel: "slack",
    status: "accepted",
    attempt_count: 1,
    updated_at: ago(5),
    attempts: [
      {
        id: uuid(511),
        status: "succeeded",
        started_at: ago(5),
        http_status: 200,
      },
    ],
  },
  {
    id: uuid(52),
    endpoint_name: tr("\u5185\u90E8\u4E8B\u4EF6\u4E2D\u5FC3"),
    subscription_name: subscriptions[1].name,
    event_kind: "incident.created",
    channel: "generic_webhook",
    status: "dead_letter",
    attempt_count: 3,
    updated_at: ago(30),
    last_error_summary: tr(
      "\u793A\u4F8B\u63A5\u6536\u7AEF\u6682\u65F6\u4E0D\u53EF\u7528",
    ),
    attempts: [
      {
        id: uuid(521),
        status: "failed",
        started_at: ago(30),
        http_status: 503,
        error_summary: tr(
          "\u793A\u4F8B\u63A5\u6536\u7AEF\u8FD4\u56DE\u670D\u52A1\u4E0D\u53EF\u7528",
        ),
      },
    ],
  },
];
let testStarted = 0;
export async function demoRequest(raw: string, init: RequestInit) {
  const parsed = new URL(raw, location.origin);
  const path = parsed.pathname.replace(/^\/v1\/tenants\/[^/]+/, "");
  const method = init.method || "GET";
  const input = init.body ? JSON.parse(String(init.body)) : {};
  if (method === "DELETE") {
    const [, resource, id] = path.split("/");
    const items =
      resource === "sources"
        ? sources
        : resource === "subscriptions"
          ? subscriptions
          : resource === "endpoints"
            ? endpoints
            : undefined;
    if (items) {
      const index = items.findIndex((item) => item.id === id);
      if (index >= 0) items.splice(index, 1);
      if (resource === "endpoints") {
        for (const rule of subscriptions) {
          if (rule.endpoint_ids.includes(id)) {
            rule.endpoint_ids = rule.endpoint_ids.filter(
              (endpoint) => endpoint !== id,
            );
            rule.rule_version += 1;
            if (!rule.endpoint_ids.length) rule.enabled = false;
          }
        }
      }
      return { id, deleted: true };
    }
  }

  if (path === "/auth/session" || path === "/session")
    return {
      tenant: { id: "demo", slug: "demo", name: tr("Acme \u5DE5\u4F5C\u533A") },
      identity: { role: "owner", display_name: "Demo" },
    };
  if (path === "/auth/logout" || path === "/logout") return {};
  if (path === "/vendors") return { data: vendors };
  if (path === "/incidents")
    return {
      data: incidents.filter(
        (i) =>
          (!parsed.searchParams.get("vendor") ||
            i.vendor_id === parsed.searchParams.get("vendor")) &&
          (!parsed.searchParams.get("phase") ||
            i.phase === parsed.searchParams.get("phase")),
      ),
    };
  if (path.startsWith("/incidents/"))
    return incidents.find((i) => i.id === path.split("/")[2]);
  if (path === "/subscriptions" && method === "POST") {
    const item = {
      ...input,
      id: crypto.randomUUID(),
      rule_version: 1,
      updated_at: ago(0),
    };
    subscriptions.unshift(item);
    return item;
  }
  if (path === "/subscriptions") return { data: subscriptions };
  if (path.startsWith("/subscriptions/")) {
    const index = subscriptions.findIndex((i) => i.id === path.split("/")[2]);
    if (method === "PUT") {
      subscriptions[index] = {
        ...subscriptions[index],
        ...input,
        rule_version: subscriptions[index].rule_version + 1,
        updated_at: ago(0),
      };
    }
    return subscriptions[index];
  }
  if (path === "/endpoints" && method === "POST") {
    const item = {
      id: crypto.randomUUID(),
      name: input.name,
      channel: input.channel,
      enabled: true,
      health_state: "unknown",
      secret_version: 1,
      key_id: "demo",
      updated_at: ago(0),
    };
    endpoints.unshift(item);
    return item;
  }
  if (path === "/endpoints") return { data: endpoints };
  if (path.endsWith("/test")) {
    testStarted = Date.now();
    return { id: "demo-test", status: "pending" };
  }
  if (path.startsWith("/endpoint-tests/"))
    return {
      id: "demo-test",
      status: Date.now() - testStarted > 1600 ? "succeeded" : "running",
      ...(Date.now() - testStarted > 1600 ? { http_status: 200 } : {}),
    };
  if (path === "/deliveries")
    return {
      data: deliveries.filter(
        (d) =>
          !parsed.searchParams.get("status") ||
          d.status === parsed.searchParams.get("status"),
      ),
    };
  if (path.startsWith("/deliveries/")) {
    const item = deliveries.find((d) => d.id === path.split("/")[2]);
    if (path.endsWith("/retry") && item) item.status = "pending";
    return item;
  }
  if (path.endsWith("/adapter-rollout")) {
    const error = Object.assign(
      new Error(tr("\u5C1A\u672A\u542F\u52A8\u9002\u914D\u5668\u53D1\u5E03")),
      {
        status: 404,
      },
    );
    throw error;
  }
  if (path === "/sources" && method === "POST") {
    const v = vendors.find((v) => v.id === input.vendor_id)!;
    const item = {
      id: crypto.randomUUID(),
      tenant_id: "demo",
      vendor_id: v.id,
      vendor_name: v.name,
      canonical_url: input.url,
      adapter_name: "statuspage",
      adapter_version: "v1",
      health_state: "unknown",
      enabled: true,
      failure_streak: 0,
    };
    sources.unshift(item);
    return item;
  }
  if (path === "/sources") return { data: sources };
  if (path === "/audit-events")
    return {
      data: [
        {
          id: uuid(61),
          sequence: 1,
          action: "subscription.created",
          resource_type: "subscription",
          resource_id: uuid(41),
          actor_type: "user",
          actor_id: "demo-owner",
          occurred_at: ago(60),
        },
      ],
    };
  throw new Error(
    tr(
      "\u6B64\u64CD\u4F5C\u672A\u5305\u542B\u5728\u8BBE\u8BA1\u6F14\u793A\u4E2D\u3002",
    ),
  );
}
