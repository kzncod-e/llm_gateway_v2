// Types mirror the control-plane JSON (see docs/API_REFERENCE.md).
// Money is integer nano-USD (1 USD = 1e9).

export type UUID = string;

export interface User {
  id: UUID;
  email: string;
  name: string;
  role: "admin" | "member";
  disabled_at: string | null;
  last_login_at: string | null;
  created_at: string;
}

export type TeamRole = "owner" | "developer" | "viewer";

export interface Team {
  id: UUID;
  name: string;
  slug: string;
  description: string;
  rpm_limit: number | null;
  concurrency_limit: number | null;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface TeamWithRole extends Team {
  role: TeamRole;
}

export interface Me {
  user: User;
  teams: TeamWithRole[];
}

export interface TeamMember {
  team_id: UUID;
  user_id: UUID;
  email: string;
  name: string;
  role: TeamRole;
  created_at: string;
}

export interface ApiKey {
  id: UUID;
  team_id: UUID;
  name: string;
  prefix: string;
  created_by: UUID | null;
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface CreatedApiKey extends ApiKey {
  key: string;
}

export interface BudgetStatus {
  team_id: UUID;
  monthly_limit_nanos: number | null;
  daily_limit_nanos: number | null;
  warn_threshold_pct: number;
  month_spent_nanos: number;
  month_reserved_nanos: number;
  day_spent_nanos: number;
  day_reserved_nanos: number;
  month_remaining_nanos: number | null;
  day_remaining_nanos: number | null;
  warning: boolean;
  exhausted: boolean;
  period_month: string;
}

export type ProviderType = "openai" | "anthropic" | "gemini";
export type CircuitState = "closed" | "half_open" | "open";

export interface Provider {
  id: UUID;
  slug: string;
  name: string;
  type: ProviderType;
  base_url: string;
  has_api_key: boolean;
  api_key_last4: string;
  key_version: number;
  enabled: boolean;
  priority: number;
  timeout_ms: number;
  created_at: string;
  updated_at: string;
  circuit_state?: CircuitState;
}

export interface Circuit {
  state: CircuitState;
  consecutive_failures: number;
  last_error: string;
  opened_at: string | null;
  last_failure_at: string | null;
}

export interface ModelPrice {
  id: UUID;
  model_id: UUID;
  effective_from: string;
  currency: string;
  input_per_mtok_nanos: number;
  cached_input_per_mtok_nanos: number;
  cache_write_per_mtok_nanos: number;
  output_per_mtok_nanos: number;
  created_at: string;
}

export interface Model {
  id: UUID;
  provider_id: UUID;
  provider_slug: string;
  name: string;
  display_name: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  current_price?: ModelPrice;
}

export interface RoutingPolicy {
  id: UUID;
  alias: string;
  description: string;
  max_attempts_per_target: number;
  retry_base_ms: number;
  retry_max_ms: number;
  enabled: boolean;
  target_model_ids: UUID[];
  created_at: string;
  updated_at: string;
}

export type RequestStatus = "success" | "error" | "interrupted" | "client_cancelled" | "rejected";
export type UsageStatus = "reported" | "unknown" | "none";
export type CostStatus = "exact" | "estimated" | "unknown" | "none";

export interface RequestRow {
  id: UUID;
  team_id: UUID;
  team_name: string;
  api_key_id: UUID | null;
  api_key_prefix: string | null;
  user_id: UUID | null;
  source: "api" | "playground";
  requested_model: string;
  stream: boolean;
  status: RequestStatus;
  http_status: number;
  error_code: string;
  error_message: string;
  provider_slug: string | null;
  model_name: string | null;
  attempt_count: number;
  fallback_count: number;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  usage_status: UsageStatus;
  cost_nanos: number | null;
  cost_status: CostStatus;
  budget_charge_nanos: number;
  latency_ms: number;
  ttfb_ms: number | null;
  created_at: string;
  completed_at: string;
}

export interface Attempt {
  id: UUID;
  attempt_number: number;
  provider_slug: string;
  model_name: string;
  status: "success" | "error" | "interrupted" | "client_cancelled" | "skipped";
  http_status: number | null;
  error_kind: string;
  error_message: string;
  retryable: boolean;
  started_at: string;
  duration_ms: number;
  ttfb_ms: number | null;
  usage_status: UsageStatus | null;
  input_tokens: number | null;
  cached_input_tokens: number | null;
  cache_write_tokens: number | null;
  output_tokens: number | null;
  reasoning_tokens: number | null;
  total_tokens: number | null;
  cost_nanos: number | null;
  pricing_id: UUID | null;
}

export interface RequestDetail extends RequestRow {
  attempts: Attempt[];
}

export interface Summary {
  requests: number;
  succeeded: number;
  failed: number;
  rejected: number;
  success_rate: number;
  cost_nanos: number;
  estimated_cost_nanos: number;
  unpriced_requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  avg_latency_ms: number;
  p95_latency_ms: number;
  fallbacks: number;
  stream_requests: number;
}

export interface TimePoint {
  bucket: string;
  requests: number;
  failed: number;
  cost_nanos: number;
  input_tokens: number;
  output_tokens: number;
  avg_latency_ms: number;
}

export interface BreakdownRow {
  key: string;
  label: string;
  attempts: number;
  cost_nanos: number;
  input_tokens: number;
  output_tokens: number;
  unknown_usage: number;
  share: number;
}

export interface ProviderStats {
  provider_id: UUID;
  attempts: number;
  errors: number;
  skipped: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  avg_ttfb_ms: number;
  last_error_at: string | null;
  last_error: string | null;
}

export interface Reliability {
  window: string;
  providers: {
    provider_id: UUID;
    slug: string;
    name: string;
    type: ProviderType;
    enabled: boolean;
    circuit: Circuit;
    stats: ProviderStats;
  }[];
  series: { bucket: string; provider_slug: string; attempts: number; errors: number; p95_ms: number }[];
  recent_failures: RequestRow[];
  summary: Summary;
}

export interface AuditLog {
  id: number;
  actor_user_id: UUID | null;
  actor_email: string;
  action: string;
  target_type: string;
  target_id: string;
  metadata: Record<string, unknown>;
  ip: string;
  created_at: string;
}

export interface GatewaySettings {
  environment: string;
  default_team_rpm: number;
  default_team_concurrency: number;
  default_max_output_tokens: number;
  request_timeout_ms: number;
  stream_timeout_ms: number;
  reservation_ttl_ms: number;
  breaker_failure_threshold: number;
  breaker_open_duration_ms: number;
  rate_limit_fail_open: boolean;
  tracing_enabled: boolean;
  currency: string;
}

export interface ListResponse<T> {
  data: T[];
  next_cursor?: string | null;
}
