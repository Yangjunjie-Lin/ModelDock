-- Provider account bindings and durable provisioning jobs. This migration
-- intentionally models only official enterprise/project provisioning, BYOK,
-- and operator-reviewed manual bindings. It does not store upstream secrets in
-- binding metadata and does not manufacture consumer accounts.

ALTER TABLE recharge_order
  ADD COLUMN IF NOT EXISTS target_provider_id uuid REFERENCES providers(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS target_provisioning_mode text;

ALTER TABLE recharge_order DROP CONSTRAINT IF EXISTS recharge_order_target_provisioning_check;
ALTER TABLE recharge_order ADD CONSTRAINT recharge_order_target_provisioning_check CHECK (
  (target_provider_id IS NULL AND target_provisioning_mode IS NULL)
  OR (target_provider_id IS NOT NULL AND target_provisioning_mode IN ('OFFICIAL_ENTERPRISE','BYOK','MANUAL','MOCK_ENTERPRISE'))
);
CREATE INDEX IF NOT EXISTS recharge_order_target_provider_idx ON recharge_order(target_provider_id,created_at DESC)
  WHERE target_provider_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS provider_account_binding (
  id uuid PRIMARY KEY,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  provisioning_mode text NOT NULL CHECK (provisioning_mode IN ('OFFICIAL_ENTERPRISE','BYOK','MANUAL','MOCK_ENTERPRISE')),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PROVISIONING','ACTIVE','ACTION_REQUIRED','FAILED','DISABLED')),
  external_account_id text,
  external_project_id text,
  credential_id uuid REFERENCES provider_credentials(id) ON DELETE RESTRICT,
  allocated_amount numeric(38,12) NOT NULL DEFAULT 0 CHECK (allocated_amount>=0),
  currency char(3),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  last_synced_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,user_id,provider_id),
  UNIQUE(credential_id),
  CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
  CHECK (jsonb_typeof(metadata)='object')
);
CREATE INDEX IF NOT EXISTS provider_account_binding_provider_idx ON provider_account_binding(provider_id,status,created_at DESC);
CREATE INDEX IF NOT EXISTS provider_account_binding_user_idx ON provider_account_binding(user_id,organization_id,created_at DESC);

CREATE TABLE IF NOT EXISTS provider_provisioning_job (
  id uuid PRIMARY KEY,
  binding_id uuid NOT NULL REFERENCES provider_account_binding(id) ON DELETE RESTRICT,
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  operation text NOT NULL CHECK (operation IN ('ENSURE_BINDING','ALLOCATE_CREDIT','REFRESH_BINDING')),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PROCESSING','RETRYABLE','SUCCEEDED','FAILED','ACTION_REQUIRED')),
  amount numeric(38,12),
  currency char(3),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
  max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
  available_at timestamptz NOT NULL DEFAULT now(),
  locked_at timestamptz,
  locked_until timestamptz,
  locked_by text,
  claim_token text,
  external_reference text,
  error_code text,
  error_detail text,
  result jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE(binding_id,idempotency_key),
  UNIQUE(recharge_order_id),
  CHECK ((amount IS NULL AND currency IS NULL) OR (amount>0 AND currency ~ '^[A-Z]{3}$')),
  CHECK ((operation='ALLOCATE_CREDIT') = (amount IS NOT NULL)),
  CHECK (jsonb_typeof(result)='object')
);
CREATE INDEX IF NOT EXISTS provider_provisioning_job_recovery_idx
  ON provider_provisioning_job(status,available_at,created_at)
  WHERE status IN ('PENDING','RETRYABLE','PROCESSING');
CREATE INDEX IF NOT EXISTS provider_provisioning_job_binding_idx ON provider_provisioning_job(binding_id,created_at DESC);

CREATE TABLE IF NOT EXISTS provider_credit_allocation (
  id uuid PRIMARY KEY,
  binding_id uuid NOT NULL REFERENCES provider_account_binding(id) ON DELETE RESTRICT,
  job_id uuid NOT NULL UNIQUE REFERENCES provider_provisioning_job(id) ON DELETE RESTRICT,
  recharge_order_id uuid NOT NULL UNIQUE REFERENCES recharge_order(id) ON DELETE RESTRICT,
  amount numeric(38,12) NOT NULL CHECK (amount>0),
  currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  external_reference text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  allocated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS provider_credit_allocation_binding_idx ON provider_credit_allocation(binding_id,allocated_at DESC);
