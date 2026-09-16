export interface Page<T> {
  data: T[] | null;
  next_cursor?: string;
}
export interface Session {
  tenant: { id: string; key?: string; slug: string; name: string };
  identity: {
    role: string;
    display_name?: string;
    actor_type?: string;
    actor_id?: string;
    email?: string;
  };
  csrf_token?: string;
}
export interface Vendor {
  collection?: CollectionStatus;
  id: string;
  slug: string;
  name: string;
  canonical_domain?: string;
  status: string;
  active_incidents: number;
  visible_sources: number;
  last_observed_at?: string;
  last_successful_at?: string;
  source_health_state: string;
}
export interface Incident {
  official_url?: string;
  id: string;
  source_id: string;
  vendor_id: string;
  vendor_slug: string;
  vendor_name: string;
  name: string;
  phase: string;
  impact: string;
  started_at?: string;
  source_updated_at?: string;
  observed_at: string;
  updated_at: string;
  updates?: {
    id: string;
    phase: string;
    body: string;
    source_updated_at?: string;
    observed_at: string;
  }[];
}
export interface Scope {
  vendor_id?: string;
  component_id?: string;
  component_key?: string;
  tag?: string;
  event_kind?: string;
}
export interface Rule {
  minimum_impact?: string;
  include_keywords?: string[];
  exclude_keywords?: string[];
  quiet_hours?: { timezone: string; from: string; to: string };
  delivery_policy?: { critical_bypass_quiet_hours: boolean };
}
export interface Subscription {
  id: string;
  name: string;
  enabled: boolean;
  pause_reason?: string;
  pause_dependencies?: {
    type: "source" | "endpoint";
    id: string;
    name: string;
  }[];
  archived_at?: string;
  rule_version: number;
  rule: Rule;
  scopes: Scope[];
  endpoint_ids: string[];
  updated_at: string;
}
export interface Endpoint {
  id: string;
  name: string;
  channel: string;
  enabled: boolean;
  archived_at?: string;
  health_state: string;
  secret_version: number;
  key_id: string;
  updated_at: string;
  config?: {
    url?: string;
    signing_key_id?: string;
    smtp_address?: string;
    smtp_username?: string;
    smtp_security?: string;
    from?: string;
    to?: string;
  };
}
export interface TestJob {
  id: string;
  status: string;
  http_status?: number;
  error_class?: string;
  error_summary?: string;
}
export interface Source {
  collection?: CollectionStatus;
  last_attempt_at?: string;
  next_poll_at?: string;
  id: string;
  tenant_id?: string;
  ownership: "platform" | "workspace";
  workspace_display_name?: string;
  archived_at?: string;
  archive_reason?: string;
  allowed_actions: string[];
  vendor_id: string;
  vendor_name: string;
  canonical_url: string;
  adapter_name: string;
  adapter_version: string;
  health_state: string;
  enabled: boolean;
  last_success_at?: string;
  failure_streak: number;
}
export interface SourceCatalogItem {
  id: string;
  vendor_id: string;
  vendor_slug: string;
  vendor_name: string;
  canonical_url: string;
  health_state: string;
  added: boolean;
}
export interface CollectionStatus {
  failure_code?: string;
  state: "fresh" | "stale" | "unknown" | "disabled";
  reason: string;
  mode?: string;
  fresh_until?: string;
  next_poll_at?: string;
  evaluated_at: string;
  resources?: {
    kind: string;
    state: string;
    last_success_at?: string;
    next_poll_at?: string;
    fresh_until?: string;
    schedule_reason: string;
  }[];
}
export interface Delivery {
  id: string;
  endpoint_name: string;
  subscription_name: string;
  event_kind: string;
  channel: string;
  status: string;
  attempt_count: number;
  updated_at: string;
  last_error_summary?: string;
  attempts?: {
    id: string;
    status: string;
    started_at: string;
    http_status?: number;
    error_summary?: string;
  }[];
}
export interface Audit {
  sequence: number;
  action: string;
  resource_type: string;
  resource_id: string;
  actor_type: string;
  actor_id: string;
  occurred_at: string;
}
export interface RolloutView {
  rollout: {
    id: string;
    state: string;
    candidate_adapter_name: string;
    candidate_adapter_version: string;
    baseline_adapter_name: string;
    minimum_samples: number;
    maximum_mismatch_rate: number;
    maximum_error_rate: number;
    decision_reason?: string;
  };
  statistics: {
    total: number;
    mismatch_rate: number;
    error_rate: number;
    candidate_p95_ms: number;
    primary_p95_ms: number;
  };
}
