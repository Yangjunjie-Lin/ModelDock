export type Row = Record<string, unknown>

export interface DashboardSummary {
  total_requests?: number
  requests_today?: number
  active_credentials?: number
  healthy_credentials?: number
  rate_limited_credentials?: number
  total_input_tokens?: number
  total_cached_tokens?: number
  total_output_tokens?: number
  estimated_cost?: number
  today_tokens?: number
  today_cost?: number
  savings_amount?: number
  success_rate?: number
  average_latency_ms?: number
  p95_latency_ms?: number
  request_trend?: Array<{ time?: string; label?: string; value?: number; requests?: number }>
  token_trend?: Array<{ time?: string; label?: string; input?: number; cached?: number; output?: number; value?: number }>
  model_distribution?: Array<{ label?: string; model?: string; value?: number; requests?: number }>
  alerts?: Array<{ id?: string; severity?: string; title?: string; message?: string; created_at?: string }>
}

export interface Credential extends Row {
  id?: string
  name?: string
  provider?: string | { name?: string }
  provider_name?: string
  project_id?: string
  group?: string | { name?: string }
  group_name?: string
  status?: string
  current_health?: string
  health?: string
  weight?: number
  priority?: number
  max_concurrency?: number
  active_requests?: number
  last_request_at?: string
  last_success_at?: string
  last_failure_at?: string
  cooldown_until?: string
  recent_rpm?: number
  recent_tpm?: number
  error_rate?: number
  secret_last4?: string
  tags?: string[]
}
