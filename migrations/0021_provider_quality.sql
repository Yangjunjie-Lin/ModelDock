-- Platform-measured Provider quality evidence, grading, ramp-up, and circuit
-- breaking. This migration is additive and leaves every existing /v1 field,
-- RelayDock key, environment variable, ledger row, and Provider declaration
-- intact. Supplier declarations and marketplace uptime are deliberately not
-- referenced by the routing-quality state.

CREATE TABLE IF NOT EXISTS provider_quality_policies (
  provider_id uuid PRIMARY KEY REFERENCES providers(id) ON DELETE RESTRICT,
  enabled boolean NOT NULL DEFAULT false,
  probe_model_id uuid REFERENCES models(id) ON DELETE RESTRICT,
  probe_interval_seconds integer NOT NULL DEFAULT 300 CHECK (probe_interval_seconds BETWEEN 30 AND 86400),
  probe_timeout_ms bigint NOT NULL DEFAULT 30000 CHECK (probe_timeout_ms BETWEEN 1000 AND 600000),
  evaluation_window_minutes integer NOT NULL DEFAULT 30 CHECK (evaluation_window_minutes BETWEEN 5 AND 10080),
  minimum_samples integer NOT NULL DEFAULT 20 CHECK (minimum_samples BETWEEN 1 AND 1000000),
  availability_target_pct numeric(7,4) NOT NULL DEFAULT 99.0000 CHECK (availability_target_pct > 0 AND availability_target_pct <= 100),
  maximum_error_rate_pct numeric(7,4) NOT NULL DEFAULT 5.0000 CHECK (maximum_error_rate_pct >= 0 AND maximum_error_rate_pct <= 100),
  maximum_429_rate_pct numeric(7,4) NOT NULL DEFAULT 10.0000 CHECK (maximum_429_rate_pct >= 0 AND maximum_429_rate_pct <= 100),
  maximum_ttft_ms bigint NOT NULL DEFAULT 5000 CHECK (maximum_ttft_ms > 0),
  maximum_full_latency_ms bigint NOT NULL DEFAULT 30000 CHECK (maximum_full_latency_ms > 0),
  minimum_throughput_tps numeric(20,6) NOT NULL DEFAULT 1.000000 CHECK (minimum_throughput_tps >= 0),
  minimum_output_quality_score numeric(7,4) NOT NULL DEFAULT 90.0000 CHECK (minimum_output_quality_score >= 0 AND minimum_output_quality_score <= 100),
  price_truth_tolerance_bps integer NOT NULL DEFAULT 0 CHECK (price_truth_tolerance_bps BETWEEN 0 AND 10000),
  required_test_regions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(required_test_regions)='array'),
  auto_downweight_enabled boolean NOT NULL DEFAULT true,
  auto_circuit_breaker_enabled boolean NOT NULL DEFAULT true,
  circuit_failure_threshold integer NOT NULL DEFAULT 3 CHECK (circuit_failure_threshold BETWEEN 1 AND 100),
  circuit_recovery_threshold integer NOT NULL DEFAULT 2 CHECK (circuit_recovery_threshold BETWEEN 1 AND 100),
  circuit_open_seconds integer NOT NULL DEFAULT 300 CHECK (circuit_open_seconds BETWEEN 30 AND 86400),
  ramp_enabled boolean NOT NULL DEFAULT true,
  ramp_initial_bps integer NOT NULL DEFAULT 500 CHECK (ramp_initial_bps BETWEEN 1 AND 10000),
  ramp_step_bps integer NOT NULL DEFAULT 500 CHECK (ramp_step_bps BETWEEN 1 AND 10000),
  ramp_step_interval_seconds integer NOT NULL DEFAULT 3600 CHECK (ramp_step_interval_seconds BETWEEN 60 AND 604800),
  configuration_source text NOT NULL DEFAULT 'PLATFORM_ADMIN' CHECK (configuration_source='PLATFORM_ADMIN'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_quality_states (
  provider_id uuid PRIMARY KEY REFERENCES providers(id) ON DELETE RESTRICT,
  grade text NOT NULL DEFAULT 'UNKNOWN' CHECK (grade IN ('UNKNOWN','A','B','C','D','F')),
  quality_score numeric(7,4) NOT NULL DEFAULT 50.0000 CHECK (quality_score BETWEEN 0 AND 100),
  routing_multiplier numeric(7,6) NOT NULL DEFAULT 1.000000 CHECK (routing_multiplier BETWEEN 0 AND 1),
  traffic_cap_bps integer NOT NULL DEFAULT 10000 CHECK (traffic_cap_bps BETWEEN 0 AND 10000),
  circuit_state text NOT NULL DEFAULT 'CLOSED' CHECK (circuit_state IN ('CLOSED','OPEN','HALF_OPEN')),
  circuit_open_until timestamptz,
  consecutive_breaches integer NOT NULL DEFAULT 0 CHECK (consecutive_breaches >= 0),
  consecutive_recoveries integer NOT NULL DEFAULT 0 CHECK (consecutive_recoveries >= 0),
  availability_pct numeric(7,4) NOT NULL DEFAULT 0 CHECK (availability_pct BETWEEN 0 AND 100),
  error_rate_pct numeric(7,4) NOT NULL DEFAULT 0 CHECK (error_rate_pct BETWEEN 0 AND 100),
  rate_limited_pct numeric(7,4) NOT NULL DEFAULT 0 CHECK (rate_limited_pct BETWEEN 0 AND 100),
  p95_ttft_ms bigint,
  p95_full_latency_ms bigint,
  throughput_tps numeric(20,6),
  output_quality_score numeric(7,4) NOT NULL DEFAULT 0 CHECK (output_quality_score BETWEEN 0 AND 100),
  price_truth_score numeric(7,4) NOT NULL DEFAULT 0 CHECK (price_truth_score BETWEEN 0 AND 100),
  region_coverage_pct numeric(7,4) NOT NULL DEFAULT 0 CHECK (region_coverage_pct BETWEEN 0 AND 100),
  measurement_count bigint NOT NULL DEFAULT 0 CHECK (measurement_count >= 0),
  ramp_started_at timestamptz,
  last_ramp_step_at timestamptz,
  last_probe_at timestamptz,
  last_evaluated_at timestamptz,
  state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO provider_quality_policies(provider_id)
SELECT id FROM providers ON CONFLICT(provider_id) DO NOTHING;
INSERT INTO provider_quality_states(provider_id)
SELECT id FROM providers ON CONFLICT(provider_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS supplier_provider_links (
  provider_id uuid PRIMARY KEY REFERENCES providers(id) ON DELETE RESTRICT,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','SUSPENDED','ENDED')),
  linked_by uuid REFERENCES users(id) ON DELETE SET NULL,
  linked_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz,
  reason text NOT NULL DEFAULT '',
  CHECK ((status='ENDED')=(ended_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS supplier_provider_links_supplier_idx ON supplier_provider_links(supplier_id,status);

CREATE TABLE IF NOT EXISTS provider_quality_probe_schedules (
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  region text NOT NULL CHECK (region ~ '^[A-Z]{2}$'),
  next_probe_at timestamptz NOT NULL DEFAULT now(),
  lease_token uuid,
  lease_until timestamptz,
  last_started_at timestamptz,
  last_finished_at timestamptz,
  last_status text NOT NULL DEFAULT 'PENDING' CHECK (last_status IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(provider_id,region),
  CHECK ((lease_token IS NULL)=(lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS provider_quality_probe_due_idx ON provider_quality_probe_schedules(next_probe_at,provider_id,region);

CREATE TABLE IF NOT EXISTS provider_quality_observations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  credential_id uuid REFERENCES provider_credentials(id) ON DELETE SET NULL,
  provider_attempt_id uuid REFERENCES funding_provider_attempt(id) ON DELETE RESTRICT,
  source text NOT NULL CHECK (source IN ('PLATFORM_TRAFFIC','SCHEDULED_HEALTH','SYNTHETIC_QUALITY','REGION_PROBE')),
  region text CHECK (region IS NULL OR region ~ '^[A-Z]{2}$'),
  model_id uuid REFERENCES models(id) ON DELETE RESTRICT,
  status_code integer CHECK (status_code IS NULL OR status_code BETWEEN 0 AND 599),
  succeeded boolean NOT NULL,
  rate_limited boolean NOT NULL DEFAULT false,
  ttft_ms bigint CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
  full_latency_ms bigint CHECK (full_latency_ms IS NULL OR full_latency_ms >= 0),
  input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens >= 0),
  output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens >= 0),
  throughput_tps numeric(20,6) CHECK (throughput_tps IS NULL OR throughput_tps >= 0),
  output_quality_score numeric(7,4) CHECK (output_quality_score IS NULL OR output_quality_score BETWEEN 0 AND 100),
  response_sha256 text CHECK (response_sha256 IS NULL OR response_sha256 ~ '^[0-9a-f]{64}$'),
  error_class text NOT NULL DEFAULT '',
  observed_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS provider_quality_observations_window_idx ON provider_quality_observations(provider_id,observed_at DESC);
CREATE INDEX IF NOT EXISTS provider_quality_observations_region_idx ON provider_quality_observations(provider_id,region,observed_at DESC) WHERE region IS NOT NULL;

CREATE TABLE IF NOT EXISTS provider_price_verifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  request_fingerprint text NOT NULL CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  price_book_id uuid REFERENCES provider_cost_price_book(id) ON DELETE RESTRICT,
  source_type text NOT NULL CHECK (source_type IN ('OFFICIAL_API','OFFICIAL_DOCUMENT','CONTRACT_INVOICE')),
  source_reference text NOT NULL,
  evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
  observed_input_token_cost numeric(30,12) NOT NULL CHECK (observed_input_token_cost >= 0),
  observed_cached_input_token_cost numeric(30,12) NOT NULL CHECK (observed_cached_input_token_cost >= 0),
  observed_output_token_cost numeric(30,12) NOT NULL CHECK (observed_output_token_cost >= 0),
  observed_request_fixed_cost numeric(30,12) NOT NULL CHECK (observed_request_fixed_cost >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL CHECK (unit > 0),
  result text NOT NULL CHECK (result IN ('MATCH','MISMATCH','UNVERIFIABLE')),
  maximum_deviation_bps bigint CHECK (maximum_deviation_bps IS NULL OR maximum_deviation_bps >= 0),
  observed_at timestamptz NOT NULL,
  expires_at timestamptz,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > observed_at)
);
CREATE INDEX IF NOT EXISTS provider_price_verifications_lookup_idx ON provider_price_verifications(provider_id,model_id,observed_at DESC);

CREATE TABLE IF NOT EXISTS provider_quality_rollups (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  window_start timestamptz NOT NULL,
  window_end timestamptz NOT NULL,
  measurement_count bigint NOT NULL CHECK (measurement_count >= 0),
  availability_pct numeric(7,4) NOT NULL CHECK (availability_pct BETWEEN 0 AND 100),
  error_rate_pct numeric(7,4) NOT NULL CHECK (error_rate_pct BETWEEN 0 AND 100),
  rate_limited_pct numeric(7,4) NOT NULL CHECK (rate_limited_pct BETWEEN 0 AND 100),
  p95_ttft_ms bigint,
  p95_full_latency_ms bigint,
  throughput_tps numeric(20,6),
  output_quality_score numeric(7,4) NOT NULL CHECK (output_quality_score BETWEEN 0 AND 100),
  price_truth_score numeric(7,4) NOT NULL CHECK (price_truth_score BETWEEN 0 AND 100),
  region_coverage_pct numeric(7,4) NOT NULL CHECK (region_coverage_pct BETWEEN 0 AND 100),
  grade text NOT NULL CHECK (grade IN ('UNKNOWN','A','B','C','D','F')),
  quality_score numeric(7,4) NOT NULL CHECK (quality_score BETWEEN 0 AND 100),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider_id,window_start,window_end),
  CHECK (window_end > window_start)
);
CREATE INDEX IF NOT EXISTS provider_quality_rollups_provider_idx ON provider_quality_rollups(provider_id,window_end DESC);

CREATE TABLE IF NOT EXISTS provider_sla_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  metric text NOT NULL CHECK (metric IN ('AVAILABILITY','ERROR_RATE','RATE_LIMITED_RATE','TTFT','FULL_LATENCY','THROUGHPUT','OUTPUT_QUALITY','PRICE_TRUTH','REGION_COVERAGE','CIRCUIT_BREAKER')),
  severity text NOT NULL CHECK (severity IN ('WARNING','CRITICAL')),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','RESOLVED')),
  dedupe_key text NOT NULL,
  observed_value numeric(30,12),
  threshold_value numeric(30,12),
  window_start timestamptz NOT NULL,
  window_end timestamptz NOT NULL,
  details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
  started_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status='RESOLVED')=(resolved_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_sla_events_open_dedupe_idx ON provider_sla_events(dedupe_key) WHERE status='OPEN';
CREATE INDEX IF NOT EXISTS provider_sla_events_provider_idx ON provider_sla_events(provider_id,started_at DESC);

CREATE OR REPLACE FUNCTION prevent_provider_quality_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'provider quality evidence is append-only' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS provider_quality_observations_immutable_trigger ON provider_quality_observations;
CREATE TRIGGER provider_quality_observations_immutable_trigger BEFORE UPDATE OR DELETE ON provider_quality_observations
  FOR EACH ROW EXECUTE FUNCTION prevent_provider_quality_evidence_mutation();
DROP TRIGGER IF EXISTS provider_price_verifications_immutable_trigger ON provider_price_verifications;
CREATE TRIGGER provider_price_verifications_immutable_trigger BEFORE UPDATE OR DELETE ON provider_price_verifications
  FOR EACH ROW EXECUTE FUNCTION prevent_provider_quality_evidence_mutation();
DROP TRIGGER IF EXISTS provider_quality_rollups_immutable_trigger ON provider_quality_rollups;
CREATE TRIGGER provider_quality_rollups_immutable_trigger BEFORE UPDATE OR DELETE ON provider_quality_rollups
  FOR EACH ROW EXECUTE FUNCTION prevent_provider_quality_evidence_mutation();

-- Providers created after this migration receive a disabled policy and neutral
-- state. Operators must explicitly enable probes after contract/region review.
CREATE OR REPLACE FUNCTION seed_provider_quality_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO provider_quality_policies(provider_id) VALUES(NEW.id) ON CONFLICT(provider_id) DO NOTHING;
  INSERT INTO provider_quality_states(provider_id) VALUES(NEW.id) ON CONFLICT(provider_id) DO NOTHING;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS providers_seed_quality_state_trigger ON providers;
CREATE TRIGGER providers_seed_quality_state_trigger AFTER INSERT ON providers
  FOR EACH ROW EXECUTE FUNCTION seed_provider_quality_state();
