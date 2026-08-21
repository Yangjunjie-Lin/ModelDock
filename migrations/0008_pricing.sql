-- ModelDock commercial pricing domain. This migration is forward-only; see
-- docs/pricing.md for the rollback procedure.

ALTER TABLE providers
  ADD COLUMN IF NOT EXISTS contract_status text NOT NULL DEFAULT 'ACTIVE',
  ADD COLUMN IF NOT EXISTS allowed_regions jsonb NOT NULL DEFAULT '["*"]'::jsonb,
  ADD COLUMN IF NOT EXISTS pricing_disabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS contract_reviewed_at timestamptz;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='providers_contract_status_check') THEN
    ALTER TABLE providers ADD CONSTRAINT providers_contract_status_check
      CHECK (contract_status IN ('ACTIVE','PENDING_REVIEW','SUSPENDED','TERMINATED'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='providers_allowed_regions_object_check') THEN
    ALTER TABLE providers ADD CONSTRAINT providers_allowed_regions_object_check
      CHECK (jsonb_typeof(allowed_regions)='array');
  END IF;
END;
$$;

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS billing_region text NOT NULL DEFAULT '*';
CREATE UNIQUE INDEX IF NOT EXISTS models_id_provider_uq ON models(id,provider_id);

-- Commercial settlement uses twelve decimal places end to end. These are
-- widening conversions of existing ledger fields; no column or value is
-- removed and legacy clients continue to receive JSON numbers.
ALTER TABLE wallets
  ALTER COLUMN available_balance TYPE numeric(30,12) USING available_balance::numeric(30,12),
  ALTER COLUMN reserved_balance TYPE numeric(30,12) USING reserved_balance::numeric(30,12),
  ALTER COLUMN credit_limit TYPE numeric(30,12) USING credit_limit::numeric(30,12);
ALTER TABLE billing_usage_records
  ALTER COLUMN amount TYPE numeric(30,12) USING amount::numeric(30,12);
ALTER TABLE wallet_transactions
  ALTER COLUMN amount TYPE numeric(30,12) USING amount::numeric(30,12),
  ALTER COLUMN balance_after TYPE numeric(30,12) USING balance_after::numeric(30,12);

CREATE TABLE IF NOT EXISTS provider_cost_price_book (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  input_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (input_token_cost >= 0),
  cached_input_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_cost >= 0),
  output_token_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (output_token_cost >= 0),
  request_fixed_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (request_fixed_cost >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL DEFAULT 1000000 CHECK (unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  source text NOT NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approval_status text NOT NULL DEFAULT 'PENDING_APPROVAL'
    CHECK (approval_status IN ('DRAFT','PENDING_APPROVAL','APPROVED','REJECTED','FORCED_APPROVED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at),
  UNIQUE(id,provider_id,model_id)
);
CREATE INDEX IF NOT EXISTS provider_cost_price_book_lookup_idx
  ON provider_cost_price_book(provider_id,model_id,approval_status,effective_at DESC);

CREATE TABLE IF NOT EXISTS customer_retail_price_book (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  input_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (input_token_price >= 0),
  cached_input_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_price >= 0),
  output_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (output_token_price >= 0),
  request_fixed_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (request_fixed_price >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL DEFAULT 1000000 CHECK (unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  source text NOT NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approval_status text NOT NULL DEFAULT 'PENDING_APPROVAL'
    CHECK (approval_status IN ('DRAFT','PENDING_APPROVAL','APPROVED','REJECTED','FORCED_APPROVED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at),
  UNIQUE(id,provider_id,model_id)
);
CREATE INDEX IF NOT EXISTS customer_retail_price_book_lookup_idx
  ON customer_retail_price_book(organization_id,provider_id,model_id,approval_status,effective_at DESC);

CREATE TABLE IF NOT EXISTS organization_price_plan (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  name text NOT NULL,
  plan_type text NOT NULL CHECK (plan_type IN ('SUBSCRIPTION','ORGANIZATION_OVERRIDE')),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  input_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (input_token_price >= 0),
  cached_input_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_price >= 0),
  output_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (output_token_price >= 0),
  request_fixed_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (request_fixed_price >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL DEFAULT 1000000 CHECK (unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  source text NOT NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approval_status text NOT NULL DEFAULT 'PENDING_APPROVAL'
    CHECK (approval_status IN ('DRAFT','PENDING_APPROVAL','APPROVED','REJECTED','FORCED_APPROVED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at)
);
CREATE INDEX IF NOT EXISTS organization_price_plan_lookup_idx
  ON organization_price_plan(organization_id,plan_type,provider_id,model_id,approval_status,effective_at DESC);

ALTER TABLE provider_cost_price_book ADD CONSTRAINT provider_cost_price_book_model_provider_fk
  FOREIGN KEY(model_id,provider_id) REFERENCES models(id,provider_id) ON DELETE RESTRICT;
ALTER TABLE customer_retail_price_book ADD CONSTRAINT customer_retail_price_book_model_provider_fk
  FOREIGN KEY(model_id,provider_id) REFERENCES models(id,provider_id) ON DELETE RESTRICT;
ALTER TABLE organization_price_plan ADD CONSTRAINT organization_price_plan_model_provider_fk
  FOREIGN KEY(model_id,provider_id) REFERENCES models(id,provider_id) ON DELETE RESTRICT;

CREATE TABLE IF NOT EXISTS pricing_margin_policies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
  provider_id uuid REFERENCES providers(id) ON DELETE CASCADE,
  model_id uuid REFERENCES models(id) ON DELETE CASCADE,
  minimum_margin_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (minimum_margin_amount >= 0),
  minimum_margin_bps integer NOT NULL DEFAULT 0 CHECK (minimum_margin_bps BETWEEN 0 AND 100000),
  enabled boolean NOT NULL DEFAULT true,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (organization_id IS NOT NULL OR provider_id IS NOT NULL OR model_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS pricing_margin_policies_lookup_idx
  ON pricing_margin_policies(organization_id,provider_id,model_id,enabled);

CREATE SEQUENCE IF NOT EXISTS model_price_version_sequence;
CREATE TABLE IF NOT EXISTS model_price_version (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_cost_price_book_id uuid REFERENCES provider_cost_price_book(id) ON DELETE RESTRICT,
  customer_retail_price_book_id uuid REFERENCES customer_retail_price_book(id) ON DELETE RESTRICT,
  organization_price_plan_id uuid REFERENCES organization_price_plan(id) ON DELETE RESTRICT,
  version bigint NOT NULL,
  provider_input_token_cost numeric(30,12) NOT NULL CHECK (provider_input_token_cost >= 0),
  provider_cached_input_token_cost numeric(30,12) NOT NULL CHECK (provider_cached_input_token_cost >= 0),
  provider_output_token_cost numeric(30,12) NOT NULL CHECK (provider_output_token_cost >= 0),
  provider_request_fixed_cost numeric(30,12) NOT NULL CHECK (provider_request_fixed_cost >= 0),
  retail_input_token_price numeric(30,12) NOT NULL CHECK (retail_input_token_price >= 0),
  retail_cached_input_token_price numeric(30,12) NOT NULL CHECK (retail_cached_input_token_price >= 0),
  retail_output_token_price numeric(30,12) NOT NULL CHECK (retail_output_token_price >= 0),
  retail_request_fixed_price numeric(30,12) NOT NULL CHECK (retail_request_fixed_price >= 0),
  provider_currency text NOT NULL CHECK (provider_currency ~ '^[A-Z]{3}$'),
  retail_currency text NOT NULL CHECK (retail_currency ~ '^[A-Z]{3}$'),
  provider_unit bigint NOT NULL CHECK (provider_unit > 0),
  retail_unit bigint NOT NULL CHECK (retail_unit > 0),
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  source text NOT NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approval_status text NOT NULL DEFAULT 'APPROVED'
    CHECK (approval_status IN ('APPROVED','FORCED_APPROVED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at),
  UNIQUE(provider_id,model_id,organization_id,version)
);
CREATE INDEX IF NOT EXISTS model_price_version_lookup_idx
  ON model_price_version(organization_id,provider_id,model_id,effective_at DESC,version DESC);
ALTER TABLE model_price_version ADD CONSTRAINT model_price_version_model_provider_fk
  FOREIGN KEY(model_id,provider_id) REFERENCES models(id,provider_id) ON DELETE RESTRICT;

CREATE TABLE IF NOT EXISTS pricing_quote (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
  model text NOT NULL,
  estimated_input_tokens bigint NOT NULL CHECK (estimated_input_tokens >= 0),
  estimated_cached_input_tokens bigint NOT NULL CHECK (estimated_cached_input_tokens >= 0),
  estimated_output_tokens bigint NOT NULL CHECK (estimated_output_tokens >= 0),
  pricing_version_id uuid NOT NULL REFERENCES model_price_version(id) ON DELETE RESTRICT,
  provider_cost_amount numeric(30,12) NOT NULL CHECK (provider_cost_amount >= 0),
  retail_amount numeric(30,12) NOT NULL CHECK (retail_amount >= 0),
  promotion_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (promotion_amount >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  exchange_rate numeric(30,12) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
  gross_margin numeric(30,12) NOT NULL,
  pre_tax_amount numeric(30,12) NOT NULL CHECK (pre_tax_amount >= 0),
  tax_rate numeric(12,8) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
  tax_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
  final_amount numeric(30,12) NOT NULL CHECK (final_amount >= 0),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS pricing_quote_org_created_idx ON pricing_quote(organization_id,created_at DESC);

CREATE TABLE IF NOT EXISTS promotion_credit (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  amount_granted numeric(30,12) NOT NULL CHECK (amount_granted > 0),
  amount_remaining numeric(30,12) NOT NULL CHECK (amount_remaining >= 0 AND amount_remaining <= amount_granted),
  idempotency_key text NOT NULL,
  source text NOT NULL,
  non_refundable boolean NOT NULL DEFAULT true,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','EXHAUSTED','EXPIRED','REVOKED')),
  expires_at timestamptz,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS promotion_credit_org_active_idx
  ON promotion_credit(organization_id,status,expires_at);

CREATE TABLE IF NOT EXISTS promotion_credit_redemptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  promotion_credit_id uuid NOT NULL REFERENCES promotion_credit(id) ON DELETE RESTRICT,
  usage_snapshot_id uuid,
  request_id text NOT NULL,
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(request_id,promotion_credit_id)
);

CREATE TABLE IF NOT EXISTS usage_price_snapshot (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id text NOT NULL UNIQUE REFERENCES request_logs(request_id) ON DELETE RESTRICT,
  pricing_version_id uuid NOT NULL REFERENCES model_price_version(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  model_id uuid REFERENCES models(id) ON DELETE SET NULL,
  input_tokens bigint NOT NULL CHECK (input_tokens >= 0),
  cached_input_tokens bigint NOT NULL CHECK (cached_input_tokens >= 0),
  output_tokens bigint NOT NULL CHECK (output_tokens >= 0),
  provider_input_token_cost numeric(30,12) NOT NULL CHECK (provider_input_token_cost >= 0),
  provider_cached_input_token_cost numeric(30,12) NOT NULL CHECK (provider_cached_input_token_cost >= 0),
  provider_output_token_cost numeric(30,12) NOT NULL CHECK (provider_output_token_cost >= 0),
  provider_request_fixed_cost numeric(30,12) NOT NULL CHECK (provider_request_fixed_cost >= 0),
  retail_input_token_price numeric(30,12) NOT NULL CHECK (retail_input_token_price >= 0),
  retail_cached_input_token_price numeric(30,12) NOT NULL CHECK (retail_cached_input_token_price >= 0),
  retail_output_token_price numeric(30,12) NOT NULL CHECK (retail_output_token_price >= 0),
  retail_request_fixed_price numeric(30,12) NOT NULL CHECK (retail_request_fixed_price >= 0),
  provider_unit bigint NOT NULL CHECK (provider_unit > 0),
  retail_unit bigint NOT NULL CHECK (retail_unit > 0),
  provider_cost_amount numeric(30,12) NOT NULL CHECK (provider_cost_amount >= 0),
  provider_currency text NOT NULL CHECK (provider_currency ~ '^[A-Z]{3}$'),
  customer_sale_amount numeric(30,12) NOT NULL CHECK (customer_sale_amount >= 0),
  customer_currency text NOT NULL CHECK (customer_currency ~ '^[A-Z]{3}$'),
  exchange_rate numeric(30,12) NOT NULL CHECK (exchange_rate > 0),
  platform_gross_margin numeric(30,12) NOT NULL,
  promotion_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (promotion_amount >= 0),
  pre_tax_amount numeric(30,12) NOT NULL CHECK (pre_tax_amount >= 0),
  tax_rate numeric(12,8) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
  tax_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
  final_user_amount numeric(30,12) NOT NULL CHECK (final_user_amount >= 0),
  pricing_rule_version text NOT NULL,
  settled_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(id,request_id)
);
CREATE INDEX IF NOT EXISTS usage_price_snapshot_settled_idx ON usage_price_snapshot(settled_at DESC);

CREATE OR REPLACE FUNCTION reject_immutable_pricing_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable', TG_TABLE_NAME USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS model_price_version_immutable_trigger ON model_price_version;
CREATE TRIGGER model_price_version_immutable_trigger BEFORE UPDATE OR DELETE ON model_price_version
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_pricing_mutation();
DROP TRIGGER IF EXISTS pricing_quote_immutable_trigger ON pricing_quote;
CREATE TRIGGER pricing_quote_immutable_trigger BEFORE UPDATE OR DELETE ON pricing_quote
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_pricing_mutation();
DROP TRIGGER IF EXISTS usage_price_snapshot_immutable_trigger ON usage_price_snapshot;
CREATE TRIGGER usage_price_snapshot_immutable_trigger BEFORE UPDATE OR DELETE ON usage_price_snapshot
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_pricing_mutation();

ALTER TABLE billing_usage_records
  ADD COLUMN IF NOT EXISTS usage_price_snapshot_id uuid,
  ADD COLUMN IF NOT EXISTS provider_cost_amount numeric(30,12),
  ADD COLUMN IF NOT EXISTS customer_sale_amount numeric(30,12),
  ADD COLUMN IF NOT EXISTS promotion_amount numeric(30,12) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS tax_amount numeric(30,12) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS final_user_amount numeric(30,12),
  ADD COLUMN IF NOT EXISTS pricing_rule_version text;

ALTER TABLE request_logs
  ADD COLUMN IF NOT EXISTS pricing_version_id uuid,
  ADD COLUMN IF NOT EXISTS usage_price_snapshot_id uuid;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_logs_pricing_version_fk') THEN
    ALTER TABLE request_logs ADD CONSTRAINT request_logs_pricing_version_fk
      FOREIGN KEY(pricing_version_id) REFERENCES model_price_version(id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_logs_usage_price_snapshot_fk') THEN
    ALTER TABLE request_logs ADD CONSTRAINT request_logs_usage_price_snapshot_fk
      FOREIGN KEY(usage_price_snapshot_id) REFERENCES usage_price_snapshot(id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='billing_usage_price_snapshot_fk') THEN
    ALTER TABLE billing_usage_records ADD CONSTRAINT billing_usage_price_snapshot_fk
      FOREIGN KEY(usage_price_snapshot_id) REFERENCES usage_price_snapshot(id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='promotion_credit_redemptions_snapshot_fk') THEN
    ALTER TABLE promotion_credit_redemptions ADD CONSTRAINT promotion_credit_redemptions_snapshot_fk
      FOREIGN KEY(usage_snapshot_id) REFERENCES usage_price_snapshot(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
  END IF;
END;
$$;

-- Seed both system books from the legacy effective model price. This keeps
-- existing /v1 behavior available while making future customer pricing
-- explicit and immutable.
INSERT INTO provider_cost_price_book(provider_id,model_id,input_token_cost,cached_input_token_cost,output_token_cost,
  currency,unit,effective_at,source,approval_status)
SELECT m.provider_id,m.id,mp.input_price,mp.cached_input_price,mp.output_price,mp.currency,mp.unit,
  mp.effective_from,'legacy_model_prices','APPROVED'
FROM model_prices mp JOIN models m ON m.id=mp.model_id
WHERE NOT EXISTS (
  SELECT 1 FROM provider_cost_price_book pcp
  WHERE pcp.provider_id=m.provider_id AND pcp.model_id=m.id AND pcp.effective_at=mp.effective_from
    AND pcp.source='legacy_model_prices'
);
INSERT INTO customer_retail_price_book(organization_id,provider_id,model_id,input_token_price,cached_input_token_price,
  output_token_price,currency,unit,effective_at,source,approval_status)
SELECT NULL,m.provider_id,m.id,mp.input_price,mp.cached_input_price,mp.output_price,mp.currency,mp.unit,
  mp.effective_from,'legacy_model_prices','APPROVED'
FROM model_prices mp JOIN models m ON m.id=mp.model_id
WHERE NOT EXISTS (
  SELECT 1 FROM customer_retail_price_book crp
  WHERE crp.organization_id IS NULL AND crp.provider_id=m.provider_id AND crp.model_id=m.id
    AND crp.effective_at=mp.effective_from AND crp.source='legacy_model_prices'
);
