-- Supplier payable subledger, invoice reconciliation, settlement approval,
-- payout recovery, and appeals. This migration is additive and preserves the
-- existing /v1 API, RelayDock key formats, environment variables, Provider
-- governance, wallet balances, and posted journals.

CREATE TABLE supplier_settlement_policy (
  supplier_id uuid PRIMARY KEY REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  enabled boolean NOT NULL DEFAULT false,
  settlement_cycle text NOT NULL DEFAULT 'MONTHLY'
    CHECK (settlement_cycle IN ('DAILY','WEEKLY','MONTHLY')),
  minimum_payout numeric(30,12) NOT NULL DEFAULT 100 CHECK (minimum_payout >= 0),
  commission_bps integer NOT NULL DEFAULT 0 CHECK (commission_bps BETWEEN 0 AND 10000),
  risk_reserve_bps integer NOT NULL DEFAULT 0 CHECK (risk_reserve_bps BETWEEN 0 AND 10000),
  reserve_hold_days integer NOT NULL DEFAULT 30 CHECK (reserve_hold_days BETWEEN 0 AND 3660),
  payout_adapter text NOT NULL DEFAULT 'disabled' CHECK (payout_adapter ~ '^[a-z0-9_-]{1,64}$'),
  payout_region text NOT NULL DEFAULT '' CHECK (payout_region='' OR payout_region ~ '^[A-Z]{2}$'),
  tax_verification_required boolean NOT NULL DEFAULT true,
  invoice_required boolean NOT NULL DEFAULT true,
  next_settlement_at timestamptz,
  last_period_end date,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((enabled AND next_settlement_at IS NOT NULL AND payout_adapter<>'disabled') OR NOT enabled)
);

INSERT INTO supplier_settlement_policy(supplier_id)
SELECT id FROM supplier_organizations ON CONFLICT(supplier_id) DO NOTHING;

CREATE TABLE supplier_payable_accrual (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  billing_usage_record_id uuid NOT NULL UNIQUE REFERENCES billing_usage_records(id) ON DELETE RESTRICT,
  usage_price_snapshot_id uuid NOT NULL UNIQUE REFERENCES usage_price_snapshot(id) ON DELETE RESTRICT,
  funding_operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  request_id text NOT NULL UNIQUE REFERENCES request_logs(request_id) ON DELETE RESTRICT,
  gross_amount numeric(30,12) NOT NULL CHECK (gross_amount >= 0),
  commission_bps integer NOT NULL CHECK (commission_bps BETWEEN 0 AND 10000),
  commission_amount numeric(30,12) NOT NULL CHECK (commission_amount >= 0),
  reserve_bps integer NOT NULL CHECK (reserve_bps BETWEEN 0 AND 10000),
  reserve_amount numeric(30,12) NOT NULL CHECK (reserve_amount >= 0),
  initial_payable_amount numeric(30,12) NOT NULL CHECK (initial_payable_amount >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  usage_settled_at timestamptz NOT NULL,
  reserve_releasable_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (gross_amount=commission_amount+reserve_amount+initial_payable_amount)
);
CREATE INDEX supplier_payable_accrual_supplier_idx
  ON supplier_payable_accrual(supplier_id,provider_id,currency,usage_settled_at,id);

CREATE TABLE supplier_bill (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  bill_reference text NOT NULL,
  period_start date NOT NULL,
  period_end date NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  total_amount numeric(30,12) NOT NULL CHECK (total_amount >= 0),
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
  import_fingerprint_sha256 text NOT NULL CHECK (import_fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
  status text NOT NULL DEFAULT 'DECLARED'
    CHECK (status IN ('DECLARED','RECONCILED','DISCREPANT','REJECTED')),
  declared_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  declared_at timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end>=period_start),
  UNIQUE(supplier_id,provider_id,bill_reference),
  UNIQUE(supplier_id,provider_id,source_sha256)
);
CREATE INDEX supplier_bill_period_idx ON supplier_bill(supplier_id,provider_id,period_start,period_end);

CREATE TABLE supplier_bill_line (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_bill_id uuid NOT NULL REFERENCES supplier_bill(id) ON DELETE RESTRICT,
  external_line_id text NOT NULL,
  request_id text,
  upstream_request_id text,
  usage_date date NOT NULL,
  input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens>=0),
  cached_input_tokens bigint CHECK (cached_input_tokens IS NULL OR cached_input_tokens>=0),
  output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens>=0),
  amount numeric(30,12) NOT NULL CHECK (amount>=0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (request_id IS NOT NULL OR upstream_request_id IS NOT NULL),
  UNIQUE(supplier_bill_id,external_line_id)
);
CREATE INDEX supplier_bill_line_request_idx ON supplier_bill_line(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX supplier_bill_line_upstream_idx ON supplier_bill_line(upstream_request_id) WHERE upstream_request_id IS NOT NULL;

CREATE TABLE supplier_settlement_batch (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_number text NOT NULL UNIQUE,
  idempotency_key text NOT NULL UNIQUE,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  period_start date NOT NULL,
  period_end date NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  gross_usage_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (gross_usage_amount >= 0),
  commission_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (commission_amount >= 0),
  reserve_held_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (reserve_held_amount >= 0),
  adjustment_amount numeric(30,12) NOT NULL DEFAULT 0,
  payout_amount numeric(30,12) NOT NULL CHECK (payout_amount > 0),
  status text NOT NULL DEFAULT 'PENDING_APPROVAL'
    CHECK (status IN ('PENDING_APPROVAL','APPROVED','PROCESSING','PAID','FAILED','DISPUTED','CANCELLED')),
  tax_status text NOT NULL DEFAULT 'PENDING'
    CHECK (tax_status IN ('NOT_REQUIRED','PENDING','VERIFIED','REJECTED')),
  invoice_status text NOT NULL DEFAULT 'MISSING'
    CHECK (invoice_status IN ('NOT_REQUIRED','MISSING','SUBMITTED','APPROVED','REJECTED')),
  provider_statement_id uuid REFERENCES provider_statement(id) ON DELETE RESTRICT,
  payout_adapter text NOT NULL,
  payout_region text NOT NULL CHECK (payout_region ~ '^[A-Z]{2}$'),
  payout_idempotency_key text NOT NULL UNIQUE,
  provider_payout_reference text NOT NULL DEFAULT '',
  retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
  max_attempts integer NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 100),
  next_retry_at timestamptz,
  last_failure_code text NOT NULL DEFAULT '',
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approved_by uuid REFERENCES users(id) ON DELETE SET NULL,
  approval_reason text NOT NULL DEFAULT '',
  approved_at timestamptz,
  paid_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end>=period_start),
  CHECK ((status IN ('APPROVED','PROCESSING','PAID','FAILED') AND approved_at IS NOT NULL AND approved_by IS NOT NULL)
      OR status IN ('PENDING_APPROVAL','DISPUTED','CANCELLED')),
  CHECK ((status='PAID')=(paid_at IS NOT NULL)),
  UNIQUE(supplier_id,provider_id,period_start,period_end,currency)
);
CREATE INDEX supplier_settlement_batch_queue_idx
  ON supplier_settlement_batch(status,next_retry_at,created_at,id);
CREATE INDEX supplier_settlement_batch_supplier_idx
  ON supplier_settlement_batch(supplier_id,created_at DESC,id);

CREATE TABLE supplier_payable_entry (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  accrual_id uuid REFERENCES supplier_payable_accrual(id) ON DELETE RESTRICT,
  settlement_batch_id uuid REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  entry_type text NOT NULL CHECK (entry_type IN ('USAGE_ACCRUAL','RESERVE_RELEASE','REFUND_SHARE','PAYOUT')),
  entry_side text NOT NULL CHECK (entry_side IN ('CREDIT','DEBIT')),
  amount numeric(30,12) NOT NULL CHECK (amount>0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  available_at timestamptz NOT NULL,
  reference text NOT NULL DEFAULT '',
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((entry_type='PAYOUT')=(settlement_batch_id IS NOT NULL)),
  CHECK (entry_type='PAYOUT' OR accrual_id IS NOT NULL)
);
CREATE UNIQUE INDEX supplier_payable_usage_accrual_uq ON supplier_payable_entry(accrual_id)
  WHERE entry_type='USAGE_ACCRUAL';
CREATE UNIQUE INDEX supplier_payable_reserve_release_uq ON supplier_payable_entry(accrual_id)
  WHERE entry_type='RESERVE_RELEASE';
CREATE UNIQUE INDEX supplier_payable_payout_uq ON supplier_payable_entry(settlement_batch_id)
  WHERE entry_type='PAYOUT';
CREATE INDEX supplier_payable_entry_available_idx
  ON supplier_payable_entry(supplier_id,provider_id,currency,available_at,id);

CREATE TABLE supplier_settlement_item (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  settlement_batch_id uuid NOT NULL REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  payable_entry_id uuid NOT NULL REFERENCES supplier_payable_entry(id) ON DELETE RESTRICT,
  accrual_id uuid REFERENCES supplier_payable_accrual(id) ON DELETE RESTRICT,
  entry_side text NOT NULL CHECK (entry_side IN ('CREDIT','DEBIT')),
  amount numeric(30,12) NOT NULL CHECK (amount>0),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX supplier_settlement_item_batch_idx ON supplier_settlement_item(settlement_batch_id,id);
CREATE INDEX supplier_settlement_item_entry_idx ON supplier_settlement_item(payable_entry_id,settlement_batch_id);

CREATE TABLE supplier_usage_statement_match (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  accrual_id uuid NOT NULL UNIQUE REFERENCES supplier_payable_accrual(id) ON DELETE RESTRICT,
  provider_statement_id uuid NOT NULL REFERENCES provider_statement(id) ON DELETE RESTRICT,
  provider_statement_line_id uuid NOT NULL UNIQUE REFERENCES provider_statement_line(id) ON DELETE RESTRICT,
  matched_amount numeric(30,12) NOT NULL CHECK (matched_amount>=0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  matched_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  matched_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE supplier_appeal (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_number text NOT NULL UNIQUE,
  idempotency_key text NOT NULL,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  appeal_type text NOT NULL CHECK (appeal_type IN ('USAGE','SETTLEMENT','BILL','RECONCILIATION')),
  accrual_id uuid REFERENCES supplier_payable_accrual(id) ON DELETE RESTRICT,
  settlement_batch_id uuid REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  supplier_bill_id uuid REFERENCES supplier_bill(id) ON DELETE RESTRICT,
  reconciliation_case_id uuid REFERENCES financial_reconciliation_case(id) ON DELETE RESTRICT,
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','UNDER_REVIEW','UPHELD','REJECTED','WITHDRAWN')),
  reason text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence)='object'),
  resolution_reason text NOT NULL DEFAULT '',
  submitted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  resolved_by uuid REFERENCES users(id) ON DELETE SET NULL,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((appeal_type='USAGE' AND accrual_id IS NOT NULL AND settlement_batch_id IS NULL AND supplier_bill_id IS NULL AND reconciliation_case_id IS NULL)
      OR (appeal_type='SETTLEMENT' AND settlement_batch_id IS NOT NULL AND accrual_id IS NULL AND supplier_bill_id IS NULL AND reconciliation_case_id IS NULL)
      OR (appeal_type='BILL' AND supplier_bill_id IS NOT NULL AND accrual_id IS NULL AND settlement_batch_id IS NULL AND reconciliation_case_id IS NULL)
      OR (appeal_type='RECONCILIATION' AND reconciliation_case_id IS NOT NULL AND accrual_id IS NULL AND settlement_batch_id IS NULL AND supplier_bill_id IS NULL)),
  CHECK ((status IN ('UPHELD','REJECTED','WITHDRAWN'))=(resolved_at IS NOT NULL)),
  UNIQUE(supplier_id,idempotency_key)
);
CREATE UNIQUE INDEX supplier_appeal_open_usage_uq ON supplier_appeal(accrual_id)
  WHERE appeal_type='USAGE' AND status IN ('OPEN','UNDER_REVIEW');
CREATE INDEX supplier_appeal_queue_idx ON supplier_appeal(status,created_at,id);

CREATE TABLE supplier_payout_attempt (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  settlement_batch_id uuid NOT NULL REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  attempt_no integer NOT NULL CHECK (attempt_no>0),
  adapter text NOT NULL,
  idempotency_key text NOT NULL,
  status text NOT NULL DEFAULT 'STARTED' CHECK (status IN ('STARTED','SUCCEEDED','FAILED')),
  provider_reference text NOT NULL DEFAULT '',
  failure_code text NOT NULL DEFAULT '',
  response_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(response_metadata)='object'),
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  UNIQUE(settlement_batch_id,attempt_no),
  UNIQUE(adapter,idempotency_key,attempt_no),
  CHECK ((status='STARTED')=(finished_at IS NULL))
);

CREATE TABLE supplier_settlement_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  settlement_batch_id uuid NOT NULL REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  event_type text NOT NULL,
  from_status text NOT NULL DEFAULT '',
  to_status text NOT NULL DEFAULT '',
  reason text NOT NULL DEFAULT '',
  actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX supplier_settlement_event_batch_idx ON supplier_settlement_event(settlement_batch_id,created_at,id);

ALTER TABLE ledger_journal
  ADD COLUMN supplier_id uuid REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  ADD COLUMN supplier_settlement_batch_id uuid REFERENCES supplier_settlement_batch(id) ON DELETE RESTRICT,
  ADD COLUMN supplier_payable_accrual_id uuid REFERENCES supplier_payable_accrual(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ledger_journal_supplier_payout_uq ON ledger_journal(supplier_settlement_batch_id)
  WHERE supplier_settlement_batch_id IS NOT NULL;

ALTER TABLE ledger_journal DROP CONSTRAINT ledger_journal_journal_type_check;
ALTER TABLE ledger_journal ADD CONSTRAINT ledger_journal_journal_type_check CHECK (
  journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL',
                   'LATE_USAGE_ADJUSTMENT','PAYMENT_CREDIT','PAYMENT_REFUND',
                   'SUBSCRIPTION_PAYMENT','SUBSCRIPTION_REFUND','RECONCILIATION_REVERSAL',
                   'PROVIDER_STATEMENT','SUPPLIER_PAYOUT')
);

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
     AND NEW.reversal_of_journal_id IS NOT DISTINCT FROM OLD.reversal_of_journal_id
     AND NEW.reconciliation_case_id IS NOT DISTINCT FROM OLD.reconciliation_case_id
     AND NEW.provider_statement_id IS NOT DISTINCT FROM OLD.provider_statement_id
     AND NEW.supplier_id IS NOT DISTINCT FROM OLD.supplier_id
     AND NEW.supplier_settlement_batch_id IS NOT DISTINCT FROM OLD.supplier_settlement_batch_id
     AND NEW.supplier_payable_accrual_id IS NOT DISTINCT FROM OLD.supplier_payable_accrual_id
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;

ALTER TABLE financial_reconciliation_case DROP CONSTRAINT financial_reconciliation_case_check_type_check;
ALTER TABLE financial_reconciliation_case ADD CONSTRAINT financial_reconciliation_case_check_type_check CHECK (
  check_type IN ('PAYMENT_TO_RECHARGE','RECHARGE_TO_WALLET','USAGE_TO_USER_CHARGE','USAGE_TO_PROVIDER_USAGE',
                 'PROVIDER_USAGE_TO_BILL','SUBSCRIPTION_TO_STATE','SUPPLIER_PAYABLE_TO_USAGE',
                 'SUPPLIER_BILL_TO_PAYABLE','SUPPLIER_PAYOUT_TO_LEDGER')
);

CREATE OR REPLACE FUNCTION reject_supplier_financial_evidence_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable',TG_TABLE_NAME USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER supplier_payable_accrual_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_payable_accrual
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();
CREATE TRIGGER supplier_payable_entry_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_payable_entry
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();
CREATE TRIGGER supplier_settlement_item_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_settlement_item
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();
CREATE TRIGGER supplier_usage_statement_match_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_usage_statement_match
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();
CREATE TRIGGER supplier_bill_line_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_bill_line
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();
CREATE TRIGGER supplier_settlement_event_immutable_trigger BEFORE UPDATE OR DELETE ON supplier_settlement_event
  FOR EACH ROW EXECUTE FUNCTION reject_supplier_financial_evidence_mutation();

CREATE OR REPLACE FUNCTION protect_supplier_bill_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.supplier_id<>OLD.supplier_id OR NEW.provider_id<>OLD.provider_id
     OR NEW.bill_reference<>OLD.bill_reference OR NEW.period_start<>OLD.period_start OR NEW.period_end<>OLD.period_end
     OR NEW.currency<>OLD.currency OR NEW.total_amount<>OLD.total_amount OR NEW.source_sha256<>OLD.source_sha256
     OR NEW.import_fingerprint_sha256<>OLD.import_fingerprint_sha256 OR NEW.declared_by<>OLD.declared_by
     OR NEW.declared_at<>OLD.declared_at THEN
    RAISE EXCEPTION 'supplier bill evidence identity is immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER supplier_bill_protect_trigger BEFORE UPDATE OR DELETE ON supplier_bill
  FOR EACH ROW EXECUTE FUNCTION protect_supplier_bill_identity();

CREATE OR REPLACE FUNCTION enforce_supplier_settlement_item_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE batch_supplier uuid; batch_provider uuid; batch_currency text;
        entry_supplier uuid; entry_provider uuid; entry_currency text; entry_accrual uuid;
BEGIN
  SELECT supplier_id,provider_id,currency INTO batch_supplier,batch_provider,batch_currency
    FROM supplier_settlement_batch WHERE id=NEW.settlement_batch_id;
  SELECT supplier_id,provider_id,currency,accrual_id INTO entry_supplier,entry_provider,entry_currency,entry_accrual
    FROM supplier_payable_entry WHERE id=NEW.payable_entry_id;
  IF batch_supplier IS NULL OR entry_supplier IS NULL OR batch_supplier<>entry_supplier
     OR batch_provider<>entry_provider OR batch_currency<>entry_currency
     OR NEW.accrual_id IS DISTINCT FROM entry_accrual THEN
    RAISE EXCEPTION 'supplier settlement item crosses supplier, Provider, currency, or accrual scope' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER supplier_settlement_item_scope_trigger BEFORE INSERT ON supplier_settlement_item
  FOR EACH ROW EXECUTE FUNCTION enforce_supplier_settlement_item_scope();

CREATE OR REPLACE FUNCTION enforce_supplier_appeal_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.appeal_type='USAGE' AND NOT EXISTS(SELECT 1 FROM supplier_payable_accrual WHERE id=NEW.accrual_id AND supplier_id=NEW.supplier_id))
     OR (NEW.appeal_type='SETTLEMENT' AND NOT EXISTS(SELECT 1 FROM supplier_settlement_batch WHERE id=NEW.settlement_batch_id AND supplier_id=NEW.supplier_id))
     OR (NEW.appeal_type='BILL' AND NOT EXISTS(SELECT 1 FROM supplier_bill WHERE id=NEW.supplier_bill_id AND supplier_id=NEW.supplier_id))
     OR (NEW.appeal_type='RECONCILIATION' AND NOT EXISTS(SELECT 1 FROM financial_reconciliation_case WHERE id=NEW.reconciliation_case_id AND details->>'supplier_id'=NEW.supplier_id::text)) THEN
    RAISE EXCEPTION 'supplier appeal target is outside supplier scope' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER supplier_appeal_scope_trigger BEFORE INSERT OR UPDATE ON supplier_appeal
  FOR EACH ROW EXECUTE FUNCTION enforce_supplier_appeal_scope();

CREATE OR REPLACE FUNCTION enforce_supplier_statement_match() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS(
    SELECT 1 FROM supplier_payable_accrual accrual
    JOIN provider_statement statement ON statement.id=NEW.provider_statement_id
    JOIN provider_statement_line line ON line.id=NEW.provider_statement_line_id AND line.provider_statement_id=statement.id
    WHERE accrual.id=NEW.accrual_id AND accrual.provider_id=statement.provider_id
      AND line.request_id=accrual.request_id AND line.amount=accrual.gross_amount
      AND line.currency=accrual.currency AND NEW.matched_amount=accrual.gross_amount AND NEW.currency=accrual.currency
  ) THEN
    RAISE EXCEPTION 'Provider statement line does not match platform supplier accrual' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER supplier_statement_match_scope_trigger BEFORE INSERT ON supplier_usage_statement_match
  FOR EACH ROW EXECUTE FUNCTION enforce_supplier_statement_match();

CREATE OR REPLACE FUNCTION seed_supplier_settlement_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO supplier_settlement_policy(supplier_id) VALUES(NEW.id) ON CONFLICT(supplier_id) DO NOTHING;
  RETURN NEW;
END;
$$;
CREATE TRIGGER supplier_seed_settlement_policy_trigger AFTER INSERT ON supplier_organizations
  FOR EACH ROW EXECUTE FUNCTION seed_supplier_settlement_policy();
