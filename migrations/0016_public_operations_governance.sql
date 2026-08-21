-- Public operations governance: risk, content policy, reports, and privacy.
-- Forward-only migration. Rollback is documented as an operational restore or
-- a follow-up disable migration; released columns are never dropped.

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS risk_score integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS verification_level text NOT NULL DEFAULT 'UNVERIFIED',
  ADD COLUMN IF NOT EXISTS payment_risk text NOT NULL DEFAULT 'UNKNOWN',
  ADD COLUMN IF NOT EXISTS abuse_status text NOT NULL DEFAULT 'CLEAR',
  ADD COLUMN IF NOT EXISTS manual_review_status text NOT NULL DEFAULT 'NOT_REQUIRED',
  ADD COLUMN IF NOT EXISTS new_account_spend_limit numeric(20,8),
  ADD COLUMN IF NOT EXISTS closed_at timestamptz,
  ADD COLUMN IF NOT EXISTS legal_hold boolean NOT NULL DEFAULT false;

ALTER TABLE users
  DROP CONSTRAINT IF EXISTS users_risk_score_check,
  ADD CONSTRAINT users_risk_score_check CHECK (risk_score BETWEEN 0 AND 100),
  DROP CONSTRAINT IF EXISTS users_verification_level_check,
  ADD CONSTRAINT users_verification_level_check CHECK (verification_level IN ('UNVERIFIED','EMAIL','IDENTITY','BUSINESS')),
  DROP CONSTRAINT IF EXISTS users_payment_risk_check,
  ADD CONSTRAINT users_payment_risk_check CHECK (payment_risk IN ('UNKNOWN','LOW','MEDIUM','HIGH','BLOCKED')),
  DROP CONSTRAINT IF EXISTS users_abuse_status_check,
  ADD CONSTRAINT users_abuse_status_check CHECK (abuse_status IN ('CLEAR','WATCH','RESTRICTED','FROZEN')),
  DROP CONSTRAINT IF EXISTS users_manual_review_status_check,
  ADD CONSTRAINT users_manual_review_status_check CHECK (manual_review_status IN ('NOT_REQUIRED','PENDING','IN_REVIEW','APPROVED','REJECTED'));
UPDATE users SET verification_level='EMAIL' WHERE email_verified_at IS NOT NULL AND verification_level='UNVERIFIED';

ALTER TABLE organizations
  ADD COLUMN IF NOT EXISTS risk_score integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS verification_level text NOT NULL DEFAULT 'UNVERIFIED',
  ADD COLUMN IF NOT EXISTS payment_risk text NOT NULL DEFAULT 'UNKNOWN',
  ADD COLUMN IF NOT EXISTS abuse_status text NOT NULL DEFAULT 'CLEAR',
  ADD COLUMN IF NOT EXISTS manual_review_status text NOT NULL DEFAULT 'NOT_REQUIRED',
  ADD COLUMN IF NOT EXISTS new_account_spend_limit numeric(20,8),
  ADD COLUMN IF NOT EXISTS legal_hold boolean NOT NULL DEFAULT false;

ALTER TABLE organizations
  DROP CONSTRAINT IF EXISTS organizations_risk_score_check,
  ADD CONSTRAINT organizations_risk_score_check CHECK (risk_score BETWEEN 0 AND 100),
  DROP CONSTRAINT IF EXISTS organizations_verification_level_check,
  ADD CONSTRAINT organizations_verification_level_check CHECK (verification_level IN ('UNVERIFIED','EMAIL','IDENTITY','BUSINESS')),
  DROP CONSTRAINT IF EXISTS organizations_payment_risk_check,
  ADD CONSTRAINT organizations_payment_risk_check CHECK (payment_risk IN ('UNKNOWN','LOW','MEDIUM','HIGH','BLOCKED')),
  DROP CONSTRAINT IF EXISTS organizations_abuse_status_check,
  ADD CONSTRAINT organizations_abuse_status_check CHECK (abuse_status IN ('CLEAR','WATCH','RESTRICTED','FROZEN')),
  DROP CONSTRAINT IF EXISTS organizations_manual_review_status_check,
  ADD CONSTRAINT organizations_manual_review_status_check CHECK (manual_review_status IN ('NOT_REQUIRED','PENDING','IN_REVIEW','APPROVED','REJECTED'));

ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS frozen_reason text,
  ADD COLUMN IF NOT EXISTS frozen_at timestamptz,
  ADD COLUMN IF NOT EXISTS last_leak_detected_at timestamptz;

CREATE TABLE IF NOT EXISTS risk_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  event_type text NOT NULL,
  ip_hash bytea,
  device_hash bytea,
  score_delta integer NOT NULL DEFAULT 0 CHECK (score_delta BETWEEN -100 AND 100),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS risk_events_subject_idx ON risk_events(user_id,organization_id,created_at DESC);
CREATE INDEX IF NOT EXISTS risk_events_signal_idx ON risk_events(event_type,created_at DESC);

CREATE TABLE IF NOT EXISTS content_policies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
  model_id uuid REFERENCES models(id) ON DELETE CASCADE,
  phase text NOT NULL CHECK (phase IN ('PRE_REQUEST','PROVIDER_NATIVE','POST_RESPONSE')),
  action text NOT NULL DEFAULT 'ALLOW' CHECK (action IN ('ALLOW','BLOCK','REVIEW','REDACT')),
  failure_mode text NOT NULL DEFAULT 'FAIL_CLOSED' CHECK (failure_mode IN ('FAIL_OPEN','FAIL_CLOSED')),
  provider_name text NOT NULL DEFAULT 'builtin',
  config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config)='object'),
  enabled boolean NOT NULL DEFAULT true,
  legal_review_required boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (organization_id IS NOT NULL OR model_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS content_policies_scope_idx ON content_policies(organization_id,model_id,phase,enabled);

CREATE TABLE IF NOT EXISTS manual_review_queue (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  request_id text,
  policy_id uuid REFERENCES content_policies(id) ON DELETE SET NULL,
  reason text NOT NULL,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','IN_REVIEW','APPROVED','REJECTED','EXPIRED')),
  resolution text,
  assigned_to uuid REFERENCES users(id) ON DELETE SET NULL,
  due_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS manual_review_queue_status_idx ON manual_review_queue(status,due_at,created_at);

CREATE TABLE IF NOT EXISTS user_reports (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  report_type text NOT NULL CHECK (report_type IN ('CONTENT','API_KEY_ABUSE','ORDER','CHARGE','PRIVACY','OTHER')),
  request_id text,
  api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE SET NULL,
  description text NOT NULL CHECK (length(description) BETWEEN 1 AND 10000),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','ACKNOWLEDGED','IN_REVIEW','RESOLVED','REJECTED')), 
  sla_hours integer NOT NULL DEFAULT 72 CHECK (sla_hours BETWEEN 1 AND 8760),
  due_at timestamptz NOT NULL DEFAULT (now() + interval '72 hours'),
  resolution text,
  handled_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_reports_queue_idx ON user_reports(status,due_at,created_at);

CREATE TABLE IF NOT EXISTS privacy_settings (
  subject_type text NOT NULL CHECK (subject_type IN ('USER','ORGANIZATION')),
  subject_id uuid NOT NULL,
  save_content boolean NOT NULL DEFAULT false,
  retention_days integer NOT NULL DEFAULT 30 CHECK (retention_days BETWEEN 1 AND 3650),
  cross_border_route text NOT NULL DEFAULT 'UNSPECIFIED' CHECK (cross_border_route IN ('UNSPECIFIED','DOMESTIC','CROSS_BORDER','REGION_LOCKED')),
  legal_hold boolean NOT NULL DEFAULT false,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(subject_type,subject_id)
);

CREATE TABLE IF NOT EXISTS data_lifecycle_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_type text NOT NULL CHECK (subject_type IN ('USER','ORGANIZATION')),
  subject_id uuid NOT NULL,
  job_type text NOT NULL CHECK (job_type IN ('EXPORT','CLOSE','DELETE','PURGE')),
  status text NOT NULL DEFAULT 'REQUESTED' CHECK (status IN ('REQUESTED','RUNNING','COMPLETED','BLOCKED','FAILED')),
  legal_hold boolean NOT NULL DEFAULT false,
  idempotency_key text NOT NULL UNIQUE,
  requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
  completed_at timestamptz,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS data_lifecycle_jobs_subject_idx ON data_lifecycle_jobs(subject_type,subject_id,created_at DESC);

ALTER TABLE audit_logs
  ADD COLUMN IF NOT EXISTS data_classification text NOT NULL DEFAULT 'INTERNAL',
  ADD COLUMN IF NOT EXISTS retention_until timestamptz,
  ADD COLUMN IF NOT EXISTS legal_hold boolean NOT NULL DEFAULT false;
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_data_classification_check;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_data_classification_check
  CHECK (data_classification IN ('PUBLIC','INTERNAL','CONFIDENTIAL','RESTRICTED'));
COMMENT ON COLUMN audit_logs.data_classification IS 'Legal review required before changing classification or retention policy.';

ALTER TABLE request_logs
  ADD COLUMN IF NOT EXISTS data_classification text NOT NULL DEFAULT 'CONFIDENTIAL',
  ADD COLUMN IF NOT EXISTS content_stored boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS cross_border_route text NOT NULL DEFAULT 'UNSPECIFIED';
ALTER TABLE request_logs DROP CONSTRAINT IF EXISTS request_logs_data_classification_check;
ALTER TABLE request_logs ADD CONSTRAINT request_logs_data_classification_check
  CHECK (data_classification IN ('PUBLIC','INTERNAL','CONFIDENTIAL','RESTRICTED'));
COMMENT ON COLUMN request_logs.content_stored IS 'False by default; prompt and response content are not stored in request_logs.';
COMMENT ON COLUMN request_logs.cross_border_route IS 'Operator-assigned routing marker; legal review required.';

CREATE OR REPLACE FUNCTION set_request_data_governance()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.content_stored := false;
  NEW.data_classification := 'CONFIDENTIAL';
  IF NEW.organization_id IS NULL AND NEW.api_key_id IS NOT NULL THEN
    SELECT k.organization_id INTO NEW.organization_id FROM api_keys k WHERE k.id=NEW.api_key_id;
  END IF;
  SELECT COALESCE(p.cross_border_route,'UNSPECIFIED') INTO NEW.cross_border_route
  FROM privacy_settings p
  WHERE p.subject_type='ORGANIZATION' AND p.subject_id=NEW.organization_id;
  NEW.cross_border_route := COALESCE(NEW.cross_border_route,'UNSPECIFIED');
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS request_logs_data_governance_trigger ON request_logs;
DROP TRIGGER IF EXISTS zz_request_logs_data_governance_trigger ON request_logs;
CREATE TRIGGER zz_request_logs_data_governance_trigger
  BEFORE INSERT ON request_logs FOR EACH ROW EXECUTE FUNCTION set_request_data_governance();

ALTER TABLE models
  ADD COLUMN IF NOT EXISTS service_subject text,
  ADD COLUMN IF NOT EXISTS filing_info text,
  ADD COLUMN IF NOT EXISTS generated_content_label_capability text NOT NULL DEFAULT 'UNKNOWN',
  ADD COLUMN IF NOT EXISTS user_disclosure text;
ALTER TABLE models DROP CONSTRAINT IF EXISTS models_generated_content_label_capability_check;
ALTER TABLE models ADD CONSTRAINT models_generated_content_label_capability_check
  CHECK (generated_content_label_capability IN ('SUPPORTED','UNSUPPORTED','UNKNOWN'));
COMMENT ON COLUMN models.filing_info IS 'Regulatory filing or registration reference; legal review required.';
COMMENT ON COLUMN models.user_disclosure IS 'User-visible disclosure text; legal review required.';

CREATE TABLE IF NOT EXISTS anomaly_alert_dedupe (
  fingerprint text PRIMARY KEY,
  alert_id uuid NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  count bigint NOT NULL DEFAULT 1
);
