-- Subscription commercial model. Token usage remains independently metered by
-- model_price_version, usage_price_snapshot, billing_usage_records, and the
-- wallet funding ledger. This migration never rewrites historical usage or
-- recharge orders.

CREATE TABLE subscription_plan (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][a-z0-9_-]{1,63}$'),
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  plan_kind text NOT NULL DEFAULT 'STANDARD' CHECK (plan_kind IN ('STANDARD','ENTERPRISE_CONTRACT')),
  enabled boolean NOT NULL DEFAULT true,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan_version (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_plan_id uuid NOT NULL REFERENCES subscription_plan(id) ON DELETE RESTRICT,
  version integer NOT NULL CHECK (version > 0),
  status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','FROZEN','RETIRED')),
  billing_interval text NOT NULL CHECK (billing_interval IN ('MONTHLY','YEARLY','CUSTOM')),
  subscription_fee numeric(30,12) NOT NULL CHECK (subscription_fee >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  trial_days integer NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 365),
  grace_period_days integer NOT NULL DEFAULT 7 CHECK (grace_period_days BETWEEN 0 AND 90),
  token_billing_mode text NOT NULL DEFAULT 'METERED_SEPARATE' CHECK (token_billing_mode='METERED_SEPARATE'),
  enterprise_contract boolean NOT NULL DEFAULT false,
  effective_at timestamptz NOT NULL DEFAULT now(),
  frozen_at timestamptz,
  retired_at timestamptz,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(subscription_plan_id,version),
  CHECK ((status='FROZEN' AND frozen_at IS NOT NULL) OR status IN ('DRAFT','RETIRED')),
  CHECK (NOT enterprise_contract OR billing_interval='CUSTOM')
);
CREATE INDEX plan_version_catalog_idx ON plan_version(subscription_plan_id,status,effective_at DESC,version DESC);
ALTER TABLE subscription_plan ADD COLUMN current_version_id uuid REFERENCES plan_version(id) ON DELETE RESTRICT;

CREATE TABLE plan_entitlement (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  plan_version_id uuid NOT NULL REFERENCES plan_version(id) ON DELETE RESTRICT,
  entitlement_key text NOT NULL CHECK (entitlement_key IN (
    'api_key_count','organization_member_count','concurrency','requests_per_minute',
    'log_retention_days','advanced_routing','cost_analysis','custom_budget',
    'webhook_count','priority_support','sla_level'
  )),
  value_type text NOT NULL CHECK (value_type IN ('INTEGER','BOOLEAN','STRING')),
  integer_value bigint,
  boolean_value boolean,
  string_value text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(plan_version_id,entitlement_key),
  CHECK (
    (value_type='INTEGER' AND integer_value IS NOT NULL AND integer_value >= 0 AND boolean_value IS NULL AND string_value IS NULL) OR
    (value_type='BOOLEAN' AND integer_value IS NULL AND boolean_value IS NOT NULL AND string_value IS NULL) OR
    (value_type='STRING' AND integer_value IS NULL AND boolean_value IS NULL AND length(trim(string_value)) > 0)
  )
);

CREATE TABLE coupon (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9][A-Z0-9_-]{2,63}$'),
  name text NOT NULL,
  discount_type text NOT NULL CHECK (discount_type IN ('PERCENT','FIXED')),
  percent_bps integer CHECK (percent_bps BETWEEN 1 AND 10000),
  fixed_amount numeric(30,12) CHECK (fixed_amount > 0),
  currency text CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
  max_redemptions integer CHECK (max_redemptions IS NULL OR max_redemptions > 0),
  redeemed_count integer NOT NULL DEFAULT 0 CHECK (redeemed_count >= 0),
  starts_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz,
  enabled boolean NOT NULL DEFAULT true,
  applies_to_subscription_only boolean NOT NULL DEFAULT true CHECK (applies_to_subscription_only),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((discount_type='PERCENT' AND percent_bps IS NOT NULL AND fixed_amount IS NULL AND currency IS NULL) OR
         (discount_type='FIXED' AND percent_bps IS NULL AND fixed_amount IS NOT NULL AND currency IS NOT NULL)),
  CHECK (expires_at IS NULL OR expires_at > starts_at),
  CHECK (max_redemptions IS NULL OR redeemed_count <= max_redemptions)
);

CREATE TABLE organization_subscription (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  plan_version_id uuid NOT NULL REFERENCES plan_version(id) ON DELETE RESTRICT,
  pending_plan_version_id uuid REFERENCES plan_version(id) ON DELETE RESTRICT,
  status text NOT NULL CHECK (status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD','CANCELED','EXPIRED')),
  current_period_start timestamptz NOT NULL,
  current_period_end timestamptz NOT NULL,
  grace_period_end timestamptz,
  cancel_at_period_end boolean NOT NULL DEFAULT false,
  canceled_at timestamptz,
  ended_at timestamptz,
  contract_reference text,
  contract_starts_at timestamptz,
  contract_ends_at timestamptz,
  coupon_id uuid REFERENCES coupon(id) ON DELETE RESTRICT,
  idempotency_key text NOT NULL,
  request_fingerprint text NOT NULL,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key),
  CHECK (current_period_end > current_period_start),
  CHECK (grace_period_end IS NULL OR grace_period_end >= current_period_start),
  CHECK (status NOT IN ('CANCELED','EXPIRED') OR ended_at IS NOT NULL)
);
CREATE UNIQUE INDEX organization_subscription_current_idx ON organization_subscription(organization_id)
  WHERE status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD');
CREATE INDEX organization_subscription_lifecycle_idx ON organization_subscription(status,current_period_end,grace_period_end);

CREATE TABLE trial (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_subscription_id uuid NOT NULL UNIQUE REFERENCES organization_subscription(id) ON DELETE RESTRICT,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  plan_version_id uuid NOT NULL REFERENCES plan_version(id) ON DELETE RESTRICT,
  status text NOT NULL CHECK (status IN ('ACTIVE','CONVERTED','CANCELED','EXPIRED')),
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  converted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,plan_version_id),
  CHECK (ends_at > starts_at)
);

CREATE TABLE subscription_invoice (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_number text NOT NULL UNIQUE,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  organization_subscription_id uuid NOT NULL REFERENCES organization_subscription(id) ON DELETE RESTRICT,
  plan_version_id uuid NOT NULL REFERENCES plan_version(id) ON DELETE RESTRICT,
  coupon_id uuid REFERENCES coupon(id) ON DELETE RESTRICT,
  invoice_type text NOT NULL CHECK (invoice_type IN ('INITIAL','RENEWAL','UPGRADE','MANUAL_CONTRACT','ADJUSTMENT')),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','PAID','VOID','FAILED','UNCOLLECTIBLE')),
  subtotal numeric(30,12) NOT NULL CHECK (subtotal >= 0),
  discount_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),
  tax_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
  total_amount numeric(30,12) NOT NULL CHECK (total_amount >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  due_at timestamptz NOT NULL,
  paid_at timestamptz,
  failed_at timestamptz,
  payment_provider text,
  provider_payment_reference text,
  ledger_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  idempotency_key text NOT NULL,
  request_fingerprint text NOT NULL,
  plan_snapshot jsonb NOT NULL CHECK (jsonb_typeof(plan_snapshot)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_subscription_id,idempotency_key),
  UNIQUE(payment_provider,provider_payment_reference),
  UNIQUE(ledger_journal_id),
  CHECK (period_end > period_start),
  CHECK (subtotal - discount_amount + tax_amount = total_amount),
  CHECK (discount_amount <= subtotal),
  CHECK ((status='PAID') = (paid_at IS NOT NULL)),
  CHECK (status<>'PAID' OR total_amount=0 OR ledger_journal_id IS NOT NULL)
);
CREATE INDEX subscription_invoice_org_created_idx ON subscription_invoice(organization_id,created_at DESC,id DESC);
CREATE INDEX subscription_invoice_due_idx ON subscription_invoice(status,due_at) WHERE status='OPEN';

CREATE TABLE subscription_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  organization_subscription_id uuid REFERENCES organization_subscription(id) ON DELETE RESTRICT,
  event_type text NOT NULL,
  from_status text,
  to_status text,
  actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  idempotency_key text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key)
);
CREATE INDEX subscription_event_org_created_idx ON subscription_event(organization_id,created_at DESC,id DESC);

CREATE TABLE subscription_coupon_redemption (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  coupon_id uuid NOT NULL REFERENCES coupon(id) ON DELETE RESTRICT,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  subscription_invoice_id uuid NOT NULL REFERENCES subscription_invoice(id) ON DELETE RESTRICT,
  discount_amount numeric(30,12) NOT NULL CHECK (discount_amount > 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(coupon_id,organization_id,subscription_invoice_id)
);

ALTER TABLE ledger_journal DROP CONSTRAINT IF EXISTS ledger_journal_journal_type_check;
ALTER TABLE ledger_journal ADD CONSTRAINT ledger_journal_journal_type_check CHECK (
  journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL',
                   'LATE_USAGE_ADJUSTMENT','PAYMENT_CREDIT','PAYMENT_REFUND',
                   'SUBSCRIPTION_PAYMENT','SUBSCRIPTION_REFUND')
);
ALTER TABLE ledger_journal ADD COLUMN subscription_invoice_id uuid REFERENCES subscription_invoice(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ledger_journal_subscription_invoice_idx ON ledger_journal(subscription_invoice_id)
  WHERE subscription_invoice_id IS NOT NULL;

CREATE OR REPLACE FUNCTION protect_posted_ledger_journal()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'posted ledger journals cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF OLD.status='DRAFT' AND NEW.status='POSTED'
     AND NEW.id=OLD.id AND NEW.wallet_id IS NOT DISTINCT FROM OLD.wallet_id
     AND NEW.journal_type=OLD.journal_type AND NEW.external_key=OLD.external_key
     AND NEW.currency=OLD.currency AND NEW.reference IS NOT DISTINCT FROM OLD.reference
     AND NEW.metadata=OLD.metadata AND NEW.created_by IS NOT DISTINCT FROM OLD.created_by
     AND NEW.recharge_order_id IS NOT DISTINCT FROM OLD.recharge_order_id
     AND NEW.refund_order_id IS NOT DISTINCT FROM OLD.refund_order_id
     AND NEW.subscription_invoice_id IS NOT DISTINCT FROM OLD.subscription_invoice_id
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;

INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:subscription-cash:'||currency,'Subscription cash','ASSET','DEBIT',currency
FROM (VALUES ('USD'),('CNY')) currency_set(currency) ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:subscription-revenue:'||currency,'Subscription revenue','REVENUE','CREDIT',currency
FROM (VALUES ('USD'),('CNY')) currency_set(currency) ON CONFLICT(account_key) DO NOTHING;

INSERT INTO subscription_plan(id,slug,name,description,plan_kind) VALUES
 ('10000000-0000-4000-8000-000000000001','free','Free','Entry subscription; Token usage is billed separately.','STANDARD'),
 ('10000000-0000-4000-8000-000000000002','developer','Developer','Individual production development; Token usage is billed separately.','STANDARD'),
 ('10000000-0000-4000-8000-000000000003','team','Team','Collaboration and advanced routing; Token usage is billed separately.','STANDARD'),
 ('10000000-0000-4000-8000-000000000004','enterprise','Enterprise','Manual contract template with finite entitlements; Token usage is billed separately.','ENTERPRISE_CONTRACT'),
 ('10000000-0000-4000-8000-000000000005','legacy-compat','Legacy Compatibility','Finite grandfathered entitlements for in-place upgrades; Token usage is billed separately.','STANDARD')
ON CONFLICT(slug) DO NOTHING;
UPDATE subscription_plan SET enabled=false WHERE slug='legacy-compat';

INSERT INTO plan_version(id,subscription_plan_id,version,status,billing_interval,subscription_fee,currency,trial_days,grace_period_days,enterprise_contract,frozen_at,metadata) VALUES
 ('11000000-0000-4000-8000-000000000001','10000000-0000-4000-8000-000000000001',1,'FROZEN','MONTHLY',0,'USD',0,7,false,now(),'{"template":"default","token_billing":"metered_separate"}'),
 ('11000000-0000-4000-8000-000000000002','10000000-0000-4000-8000-000000000002',1,'FROZEN','MONTHLY',29,'USD',14,7,false,now(),'{"template":"default","token_billing":"metered_separate"}'),
 ('11000000-0000-4000-8000-000000000003','10000000-0000-4000-8000-000000000003',1,'FROZEN','MONTHLY',199,'USD',14,14,false,now(),'{"template":"default","token_billing":"metered_separate"}'),
 ('11000000-0000-4000-8000-000000000004','10000000-0000-4000-8000-000000000004',1,'FROZEN','CUSTOM',0,'USD',0,30,true,now(),'{"template":"default","manual_contract":true,"token_billing":"metered_separate"}'),
 ('11000000-0000-4000-8000-000000000005','10000000-0000-4000-8000-000000000005',1,'FROZEN','MONTHLY',0,'USD',0,30,false,now(),'{"migration_compatibility":true,"token_billing":"metered_separate"}')
ON CONFLICT(subscription_plan_id,version) DO NOTHING;

UPDATE subscription_plan plan SET current_version_id=version.id
FROM plan_version version WHERE version.subscription_plan_id=plan.id AND version.version=1 AND plan.current_version_id IS NULL;

INSERT INTO plan_entitlement(plan_version_id,entitlement_key,value_type,integer_value,boolean_value,string_value) VALUES
 ('11000000-0000-4000-8000-000000000001','api_key_count','INTEGER',2,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','organization_member_count','INTEGER',1,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','concurrency','INTEGER',1,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','requests_per_minute','INTEGER',60,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','log_retention_days','INTEGER',7,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','advanced_routing','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000001','cost_analysis','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000001','custom_budget','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000001','webhook_count','INTEGER',0,NULL,NULL),
 ('11000000-0000-4000-8000-000000000001','priority_support','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000001','sla_level','STRING',NULL,NULL,'COMMUNITY'),
 ('11000000-0000-4000-8000-000000000002','api_key_count','INTEGER',10,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','organization_member_count','INTEGER',3,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','concurrency','INTEGER',5,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','requests_per_minute','INTEGER',600,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','log_retention_days','INTEGER',30,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','advanced_routing','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000002','cost_analysis','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000002','custom_budget','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000002','webhook_count','INTEGER',3,NULL,NULL),
 ('11000000-0000-4000-8000-000000000002','priority_support','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000002','sla_level','STRING',NULL,NULL,'STANDARD'),
 ('11000000-0000-4000-8000-000000000003','api_key_count','INTEGER',50,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','organization_member_count','INTEGER',25,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','concurrency','INTEGER',25,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','requests_per_minute','INTEGER',3000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','log_retention_days','INTEGER',90,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','advanced_routing','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000003','cost_analysis','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000003','custom_budget','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000003','webhook_count','INTEGER',20,NULL,NULL),
 ('11000000-0000-4000-8000-000000000003','priority_support','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000003','sla_level','STRING',NULL,NULL,'BUSINESS'),
 ('11000000-0000-4000-8000-000000000004','api_key_count','INTEGER',1000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','organization_member_count','INTEGER',1000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','concurrency','INTEGER',500,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','requests_per_minute','INTEGER',30000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','log_retention_days','INTEGER',365,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','advanced_routing','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000004','cost_analysis','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000004','custom_budget','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000004','webhook_count','INTEGER',500,NULL,NULL),
 ('11000000-0000-4000-8000-000000000004','priority_support','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000004','sla_level','STRING',NULL,NULL,'ENTERPRISE'),
 ('11000000-0000-4000-8000-000000000005','api_key_count','INTEGER',1000000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','organization_member_count','INTEGER',1000000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','concurrency','INTEGER',100000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','requests_per_minute','INTEGER',1000000000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','log_retention_days','INTEGER',36500,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','advanced_routing','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000005','cost_analysis','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000005','custom_budget','BOOLEAN',NULL,true,NULL),
 ('11000000-0000-4000-8000-000000000005','webhook_count','INTEGER',1000000,NULL,NULL),
 ('11000000-0000-4000-8000-000000000005','priority_support','BOOLEAN',NULL,false,NULL),
 ('11000000-0000-4000-8000-000000000005','sla_level','STRING',NULL,NULL,'LEGACY_COMPATIBILITY')
ON CONFLICT(plan_version_id,entitlement_key) DO NOTHING;

CREATE OR REPLACE FUNCTION protect_frozen_plan_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('FROZEN','RETIRED') THEN
    IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.subscription_plan_id<>OLD.subscription_plan_id OR
       NEW.version<>OLD.version OR NEW.billing_interval<>OLD.billing_interval OR
       NEW.subscription_fee<>OLD.subscription_fee OR NEW.currency<>OLD.currency OR
       NEW.trial_days<>OLD.trial_days OR NEW.grace_period_days<>OLD.grace_period_days OR
       NEW.token_billing_mode<>OLD.token_billing_mode OR NEW.enterprise_contract<>OLD.enterprise_contract OR
       NEW.effective_at<>OLD.effective_at OR NEW.metadata<>OLD.metadata OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
       NEW.created_at<>OLD.created_at OR NOT (OLD.status='FROZEN' AND NEW.status='RETIRED' AND NEW.retired_at IS NOT NULL) THEN
      RAISE EXCEPTION 'frozen plan versions are immutable' USING ERRCODE='55000';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER plan_version_frozen_protect_trigger BEFORE UPDATE OR DELETE ON plan_version
  FOR EACH ROW EXECUTE FUNCTION protect_frozen_plan_version();

CREATE OR REPLACE FUNCTION protect_frozen_entitlement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE frozen boolean;
BEGIN
  SELECT status IN ('FROZEN','RETIRED') INTO frozen FROM plan_version WHERE id=COALESCE(NEW.plan_version_id,OLD.plan_version_id);
  IF frozen THEN RAISE EXCEPTION 'frozen plan entitlements are immutable' USING ERRCODE='55000'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER plan_entitlement_frozen_protect_trigger BEFORE INSERT OR UPDATE OR DELETE ON plan_entitlement
  FOR EACH ROW EXECUTE FUNCTION protect_frozen_entitlement();

CREATE OR REPLACE FUNCTION reject_immutable_subscription_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION '% records are immutable',TG_TABLE_NAME USING ERRCODE='55000'; END;
$$;
CREATE TRIGGER subscription_event_immutable_trigger BEFORE UPDATE OR DELETE ON subscription_event
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_subscription_evidence();
CREATE TRIGGER coupon_redemption_immutable_trigger BEFORE UPDATE OR DELETE ON subscription_coupon_redemption
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_subscription_evidence();

CREATE OR REPLACE FUNCTION protect_subscription_invoice() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'subscription invoices cannot be deleted' USING ERRCODE='55000'; END IF;
  IF NEW.id<>OLD.id OR NEW.invoice_number<>OLD.invoice_number OR NEW.organization_id<>OLD.organization_id OR
     NEW.organization_subscription_id<>OLD.organization_subscription_id OR NEW.plan_version_id<>OLD.plan_version_id OR
     NEW.coupon_id IS DISTINCT FROM OLD.coupon_id OR NEW.invoice_type<>OLD.invoice_type OR
     NEW.subtotal<>OLD.subtotal OR NEW.discount_amount<>OLD.discount_amount OR NEW.tax_amount<>OLD.tax_amount OR
     NEW.total_amount<>OLD.total_amount OR NEW.currency<>OLD.currency OR NEW.period_start<>OLD.period_start OR
     NEW.period_end<>OLD.period_end OR NEW.due_at<>OLD.due_at OR NEW.idempotency_key<>OLD.idempotency_key OR
     NEW.request_fingerprint<>OLD.request_fingerprint OR NEW.plan_snapshot<>OLD.plan_snapshot OR
     NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at<>OLD.created_at THEN
    RAISE EXCEPTION 'subscription invoice commercial terms are immutable' USING ERRCODE='55000';
  END IF;
  IF OLD.status IN ('PAID','VOID','UNCOLLECTIBLE') THEN
    RAISE EXCEPTION 'terminal subscription invoices are immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER subscription_invoice_protect_trigger BEFORE UPDATE OR DELETE ON subscription_invoice
  FOR EACH ROW EXECUTE FUNCTION protect_subscription_invoice();

INSERT INTO organization_subscription(id,organization_id,plan_version_id,status,current_period_start,current_period_end,idempotency_key,request_fingerprint,metadata)
SELECT gen_random_uuid(),organization.id,'11000000-0000-4000-8000-000000000005','ACTIVE',now(),now()+interval '100 years',
	   'migration:0011:legacy-compat','migration:0011:legacy-compat:'||organization.id,'{"source":"migration_0011","compatibility":true}'::jsonb
FROM organizations organization
WHERE NOT EXISTS (SELECT 1 FROM organization_subscription current_subscription
                  WHERE current_subscription.organization_id=organization.id
                    AND current_subscription.status IN ('TRIALING','ACTIVE','PAST_DUE','GRACE_PERIOD'));

CREATE OR REPLACE FUNCTION create_default_free_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO organization_subscription(organization_id,plan_version_id,status,current_period_start,current_period_end,idempotency_key,request_fingerprint,metadata)
  VALUES(NEW.id,'11000000-0000-4000-8000-000000000001','ACTIVE',now(),now()+interval '100 years',
         'default:free','default:free:'||NEW.id,'{"source":"organization_trigger"}'::jsonb);
  RETURN NEW;
END;
$$;
CREATE TRIGGER organizations_default_subscription_trigger AFTER INSERT ON organizations
  FOR EACH ROW EXECUTE FUNCTION create_default_free_subscription();

DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM plan_entitlement WHERE entitlement_key ILIKE '%token%') THEN
    RAISE EXCEPTION 'Token entitlements are forbidden; Token usage must remain metered separately';
  END IF;
END $$;
