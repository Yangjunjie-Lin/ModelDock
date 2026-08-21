-- Provider commercial governance, cost-change approval, and tenant-bound BYOK.
-- This migration is forward-only. See docs/provider-governance.md for the
-- operational rollback procedure.

ALTER TABLE providers
  ADD COLUMN IF NOT EXISTS commercial_status text NOT NULL DEFAULT 'CONTRACT_PENDING',
  ADD COLUMN IF NOT EXISTS legal_entity text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS contract_type text NOT NULL DEFAULT 'UNSPECIFIED',
  ADD COLUMN IF NOT EXISTS contract_start_at timestamptz,
  ADD COLUMN IF NOT EXISTS contract_end_at timestamptz,
  ADD COLUMN IF NOT EXISTS commercial_resale_status text NOT NULL DEFAULT 'NOT_APPROVED',
  ADD COLUMN IF NOT EXISTS credential_owner text NOT NULL DEFAULT 'PLATFORM',
  ADD COLUMN IF NOT EXISTS allowed_customer_regions jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS prohibited_regions jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS data_processing_regions jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS data_retention_policy text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS terms_version text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cost_limit numeric(30,12),
  ADD COLUMN IF NOT EXISTS rate_limit integer,
  ADD COLUMN IF NOT EXISTS settlement_currency text NOT NULL DEFAULT 'USD',
  ADD COLUMN IF NOT EXISTS emergency_kill_switch boolean NOT NULL DEFAULT false;

-- A pre-existing ACTIVE value is not proof of commercial approval. Only rows
-- carrying the earlier explicit review timestamp retain production approval.
UPDATE providers
SET commercial_status = CASE
  WHEN contract_status='ACTIVE' AND contract_reviewed_at IS NOT NULL THEN 'COMMERCIAL_APPROVED'
  WHEN contract_status IN ('ACTIVE','PENDING_REVIEW') THEN 'CONTRACT_PENDING'
  ELSE contract_status
END;

-- Preserve the released allowed_regions column and copy it only when the new
-- customer-region list has not yet been governed independently.
UPDATE providers
SET allowed_customer_regions = allowed_regions
WHERE allowed_customer_regions='[]'::jsonb;

ALTER TABLE providers ADD CONSTRAINT providers_commercial_status_check CHECK (
  commercial_status IN ('TECHNICALLY_AVAILABLE','CONTRACT_PENDING','COMMERCIAL_APPROVED','SUSPENDED','EXPIRED','TERMINATED')
);
ALTER TABLE providers ADD CONSTRAINT providers_contract_period_check CHECK (
  contract_end_at IS NULL OR contract_start_at IS NULL OR contract_end_at > contract_start_at
);
ALTER TABLE providers ADD CONSTRAINT providers_resale_status_check CHECK (
  commercial_resale_status IN ('NOT_APPROVED','PENDING','APPROVED','PROHIBITED')
);
ALTER TABLE providers ADD CONSTRAINT providers_credential_owner_check CHECK (credential_owner IN ('PLATFORM','CUSTOMER','MIXED'));
ALTER TABLE providers ADD CONSTRAINT providers_customer_regions_array_check CHECK (jsonb_typeof(allowed_customer_regions)='array');
ALTER TABLE providers ADD CONSTRAINT providers_prohibited_regions_array_check CHECK (jsonb_typeof(prohibited_regions)='array');
ALTER TABLE providers ADD CONSTRAINT providers_processing_regions_array_check CHECK (jsonb_typeof(data_processing_regions)='array');
ALTER TABLE providers ADD CONSTRAINT providers_cost_limit_check CHECK (cost_limit IS NULL OR cost_limit >= 0);
ALTER TABLE providers ADD CONSTRAINT providers_rate_limit_check CHECK (rate_limit IS NULL OR rate_limit > 0);
ALTER TABLE providers ADD CONSTRAINT providers_settlement_currency_check CHECK (settlement_currency ~ '^[A-Z]{3}$');

ALTER TABLE organizations
  ADD COLUMN IF NOT EXISTS allowed_provider_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS prohibited_provider_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS required_data_regions jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS minimum_gross_margin numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE organizations ADD CONSTRAINT organizations_allowed_provider_ids_array_check CHECK (jsonb_typeof(allowed_provider_ids)='array');
ALTER TABLE organizations ADD CONSTRAINT organizations_prohibited_provider_ids_array_check CHECK (jsonb_typeof(prohibited_provider_ids)='array');
ALTER TABLE organizations ADD CONSTRAINT organizations_required_data_regions_array_check CHECK (jsonb_typeof(required_data_regions)='array');
ALTER TABLE organizations ADD CONSTRAINT organizations_minimum_gross_margin_check CHECK (minimum_gross_margin >= 0);

ALTER TABLE users ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';

ALTER TABLE models
  ADD COLUMN IF NOT EXISTS allowed_regions jsonb NOT NULL DEFAULT '["*"]'::jsonb;
ALTER TABLE models ADD CONSTRAINT models_allowed_regions_array_check CHECK (jsonb_typeof(allowed_regions)='array');

ALTER TABLE provider_credentials
  ADD COLUMN IF NOT EXISTS credential_owner text NOT NULL DEFAULT 'PLATFORM',
  ADD COLUMN IF NOT EXISTS owner_organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS ownership_confirmed_at timestamptz,
  ADD COLUMN IF NOT EXISTS ownership_confirmed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS ownership_terms_version text NOT NULL DEFAULT '';
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_owner_check CHECK (credential_owner IN ('PLATFORM','CUSTOMER'));
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_byok_owner_check CHECK (
  (credential_owner='PLATFORM' AND owner_organization_id IS NULL) OR
  (credential_owner='CUSTOMER' AND owner_organization_id IS NOT NULL AND ownership_confirmed_at IS NOT NULL AND ownership_terms_version <> '')
);
CREATE INDEX IF NOT EXISTS provider_credentials_owner_org_idx ON provider_credentials(owner_organization_id,status)
  WHERE credential_owner='CUSTOMER';

CREATE TABLE IF NOT EXISTS provider_usage_budget (
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  period_month date NOT NULL,
  request_count bigint NOT NULL DEFAULT 0 CHECK (request_count >= 0),
  reserved_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (reserved_cost >= 0),
  provider_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (provider_cost >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(provider_id,period_month),
  CHECK (period_month=date_trunc('month',period_month)::date)
);

CREATE TABLE IF NOT EXISTS provider_budget_reservations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL UNIQUE REFERENCES funding_operation(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  period_month date NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  maximum_cost numeric(30,12) NOT NULL CHECK (maximum_cost >= 0),
  settled_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (settled_cost >= 0),
  status text NOT NULL DEFAULT 'RESERVED' CHECK (status IN ('RESERVED','SETTLED','RELEASED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  FOREIGN KEY(provider_id,period_month) REFERENCES provider_usage_budget(provider_id,period_month) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS provider_budget_reservations_provider_idx
  ON provider_budget_reservations(provider_id,period_month,status);

CREATE TABLE IF NOT EXISTS provider_cost_change_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  source_type text NOT NULL CHECK (source_type IN ('MANUAL','API','CSV')),
  source_reference text NOT NULL DEFAULT '',
  input_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (input_token_cost >= 0),
  cached_input_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_cost >= 0),
  output_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (output_token_cost >= 0),
  request_fixed_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (request_fixed_cost >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL CHECK (unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED','CANCELLED')),
  request_fingerprint text NOT NULL,
  previous_price_book_id uuid REFERENCES provider_cost_price_book(id) ON DELETE RESTRICT,
  published_price_book_id uuid UNIQUE REFERENCES provider_cost_price_book(id) ON DELETE RESTRICT,
  change_summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(change_summary)='object'),
  requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  review_reason text NOT NULL DEFAULT '',
  reviewed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at)
);
CREATE INDEX IF NOT EXISTS provider_cost_change_requests_status_idx ON provider_cost_change_requests(status,created_at DESC);

CREATE TABLE IF NOT EXISTS byok_service_fee_policies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_id uuid REFERENCES providers(id) ON DELETE RESTRICT,
  fixed_fee numeric(30,12) NOT NULL DEFAULT 0 CHECK (fixed_fee >= 0),
  input_token_fee numeric(30,12) NOT NULL DEFAULT 0 CHECK (input_token_fee >= 0),
  cached_input_token_fee numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_fee >= 0),
  output_token_fee numeric(30,12) NOT NULL DEFAULT 0 CHECK (output_token_fee >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL DEFAULT 1000000 CHECK (unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at)
);
CREATE INDEX IF NOT EXISTS byok_service_fee_policy_lookup_idx
  ON byok_service_fee_policies(organization_id,provider_id,enabled,effective_at DESC);

ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS credential_owner text NOT NULL DEFAULT 'PLATFORM';
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS provider_credential_id uuid REFERENCES provider_credentials(id) ON DELETE SET NULL;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS platform_service_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_service_fee_policy_id uuid REFERENCES byok_service_fee_policies(id) ON DELETE RESTRICT;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_fixed_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_input_token_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_cached_input_token_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_output_token_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE funding_operation ADD COLUMN IF NOT EXISTS byok_fee_unit bigint NOT NULL DEFAULT 1000000;
ALTER TABLE funding_operation ADD CONSTRAINT funding_operation_credential_owner_check CHECK (credential_owner IN ('PLATFORM','CUSTOMER'));
ALTER TABLE funding_operation ADD CONSTRAINT funding_operation_service_fee_check CHECK (platform_service_fee >= 0);
ALTER TABLE funding_operation ADD CONSTRAINT funding_operation_byok_fee_check CHECK (byok_fixed_fee >= 0 AND byok_input_token_fee >= 0 AND byok_cached_input_token_fee >= 0 AND byok_output_token_fee >= 0 AND byok_fee_unit > 0);

ALTER TABLE usage_price_snapshot ADD COLUMN IF NOT EXISTS credential_owner text NOT NULL DEFAULT 'PLATFORM';
ALTER TABLE usage_price_snapshot ADD COLUMN IF NOT EXISTS platform_service_fee numeric(30,12) NOT NULL DEFAULT 0;
ALTER TABLE usage_price_snapshot ADD CONSTRAINT usage_price_snapshot_credential_owner_check CHECK (credential_owner IN ('PLATFORM','CUSTOMER'));
ALTER TABLE usage_price_snapshot ADD CONSTRAINT usage_price_snapshot_service_fee_check CHECK (platform_service_fee >= 0);

CREATE OR REPLACE FUNCTION enforce_byok_organization_boundary()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.credential_owner <> 'CUSTOMER' THEN RETURN NEW; END IF;
  IF NEW.owner_organization_id IS NULL THEN
    RAISE EXCEPTION 'customer credential requires an owner organization' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS provider_credentials_byok_boundary_trigger ON provider_credentials;
CREATE TRIGGER provider_credentials_byok_boundary_trigger
  BEFORE INSERT OR UPDATE OF credential_owner,owner_organization_id,organization_id,project_id ON provider_credentials
  FOR EACH ROW EXECUTE FUNCTION enforce_byok_organization_boundary();

CREATE OR REPLACE FUNCTION prevent_byok_owner_transfer()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.credential_owner='CUSTOMER' AND (
    NEW.credential_owner IS DISTINCT FROM OLD.credential_owner OR
    NEW.owner_organization_id IS DISTINCT FROM OLD.owner_organization_id
  ) THEN
    RAISE EXCEPTION 'customer credentials cannot be transferred between organizations' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS provider_credentials_prevent_owner_transfer_trigger ON provider_credentials;
CREATE TRIGGER provider_credentials_prevent_owner_transfer_trigger
  BEFORE UPDATE OF credential_owner,owner_organization_id ON provider_credentials
  FOR EACH ROW EXECUTE FUNCTION prevent_byok_owner_transfer();

-- Historical price books and usage snapshots remain immutable. New cost rows
-- are published only by approving a change request; no historical row is
-- updated or deleted by this migration.
