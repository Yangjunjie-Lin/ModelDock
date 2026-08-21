-- End-to-end financial close: source-attributed cash, customer requests,
-- provider statements, reconciliation evidence, and reversal-only repairs.
-- This migration is forward-only.  Guarded rollback is documented in
-- docs/financial-close.md; posted journals and evidence rows are retained.

ALTER TABLE wallet_transactions
  ADD COLUMN IF NOT EXISTS funding_operation_id uuid REFERENCES funding_operation(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS wallet_transactions_funding_operation_idx
  ON wallet_transactions(funding_operation_id,created_at,id) WHERE funding_operation_id IS NOT NULL;
UPDATE wallet_transactions transaction
SET funding_operation_id=(transaction.metadata->>'funding_operation_id')::uuid
WHERE transaction.funding_operation_id IS NULL
  AND transaction.metadata->>'funding_operation_id' ~ '^[0-9a-fA-F-]{36}$'
  AND EXISTS (SELECT 1 FROM funding_operation operation
              WHERE operation.id=(transaction.metadata->>'funding_operation_id')::uuid)
  AND NOT EXISTS (SELECT 1 FROM wallet_transactions other
                  WHERE other.funding_operation_id=(transaction.metadata->>'funding_operation_id')::uuid);

ALTER TABLE funding_operation
  ADD COLUMN IF NOT EXISTS consumed_promotion_amount numeric(30,12) NOT NULL DEFAULT 0
    CHECK (consumed_promotion_amount>=0 AND consumed_promotion_amount<=promotion_amount);

CREATE TABLE funding_promotion_allocation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  promotion_credit_id uuid NOT NULL REFERENCES promotion_credit(id) ON DELETE RESTRICT,
  reserved_amount numeric(30,12) NOT NULL CHECK (reserved_amount>0),
  consumed_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (consumed_amount>=0),
  released_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (released_amount>=0),
  reversed_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (reversed_amount>=0),
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  CHECK (consumed_amount+released_amount<=reserved_amount),
  CHECK (reversed_amount<=consumed_amount),
  UNIQUE(operation_id,promotion_credit_id)
);
CREATE INDEX funding_promotion_credit_idx ON funding_promotion_allocation(promotion_credit_id,created_at,id);

CREATE OR REPLACE FUNCTION protect_funding_promotion_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'funding promotion allocations cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF NEW.id=OLD.id AND NEW.operation_id=OLD.operation_id
     AND NEW.promotion_credit_id=OLD.promotion_credit_id
     AND NEW.reserved_amount=OLD.reserved_amount AND NEW.created_at=OLD.created_at
     AND NEW.consumed_amount>=OLD.consumed_amount
     AND NEW.released_amount>=OLD.released_amount
     AND NEW.reversed_amount>=OLD.reversed_amount
     AND NEW.consumed_amount+NEW.released_amount<=NEW.reserved_amount
     AND NEW.reversed_amount<=NEW.consumed_amount THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'funding promotion allocation evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER funding_promotion_allocation_protect_trigger
  BEFORE UPDATE OR DELETE ON funding_promotion_allocation
  FOR EACH ROW EXECUTE FUNCTION protect_funding_promotion_allocation();

CREATE TABLE wallet_cash_lot (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  source_transaction_id uuid REFERENCES wallet_transactions(id) ON DELETE RESTRICT,
  source_kind text NOT NULL CHECK (source_kind IN ('RECHARGE','TOPUP','ADJUSTMENT','OPENING','REVERSAL')),
  source_reference text NOT NULL,
  original_amount numeric(30,12) NOT NULL CHECK (original_amount > 0),
  remaining_amount numeric(30,12) NOT NULL CHECK (remaining_amount >= 0 AND remaining_amount <= original_amount),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  refundable boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(wallet_id,source_reference),
  UNIQUE(source_transaction_id),
  UNIQUE(recharge_order_id)
);
CREATE INDEX wallet_cash_lot_available_idx
  ON wallet_cash_lot(wallet_id,created_at,id) WHERE remaining_amount>0;

CREATE TABLE wallet_cash_allocation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
  wallet_transaction_id uuid NOT NULL REFERENCES wallet_transactions(id) ON DELETE RESTRICT,
  cash_lot_id uuid REFERENCES wallet_cash_lot(id) ON DELETE RESTRICT,
  source_allocation_id uuid REFERENCES wallet_cash_allocation(id) ON DELETE RESTRICT,
  bucket text NOT NULL CHECK (bucket IN ('CASH','CREDIT')),
  direction text NOT NULL CHECK (direction IN ('DEBIT','CREDIT','RESTORE')),
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((bucket='CASH')=(cash_lot_id IS NOT NULL)),
  CHECK ((direction='RESTORE')=(source_allocation_id IS NOT NULL)),
  UNIQUE(source_allocation_id,wallet_transaction_id),
  UNIQUE(wallet_transaction_id,cash_lot_id,bucket,direction)
);
CREATE INDEX wallet_cash_allocation_lot_idx ON wallet_cash_allocation(cash_lot_id,created_at,id);

-- Existing aggregate balances cannot always be attributed to a historical
-- source.  Allocate the still-positive balance to the newest verified,
-- unrefunded recharges (FIFO consumption leaves newest lots), then preserve
-- any remainder as a non-refundable migration opening lot.  No old balance is
-- changed and no unsupported historical attribution is invented.
DO $$
DECLARE w record; payment record; budget numeric(30,12); net numeric(30,12); remainder numeric(30,12);
BEGIN
  FOR w IN SELECT id,currency,GREATEST(available_balance,0)::numeric(30,12) available FROM wallets LOOP
    budget := w.available;
    FOR payment IN
      SELECT recharge.id,recharge.platform_order_no,recharge.wallet_transaction_id,recharge.amount,
             COALESCE((SELECT sum(refund.amount) FROM refund_order refund
                       WHERE refund.recharge_order_id=recharge.id AND refund.status='SUCCEEDED'),0) refunded,
             COALESCE(recharge.credited_at,recharge.created_at) credited_at
      FROM recharge_order recharge
      WHERE recharge.wallet_id=w.id AND recharge.wallet_transaction_id IS NOT NULL
        AND recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')
      ORDER BY COALESCE(recharge.credited_at,recharge.created_at) DESC,recharge.id DESC
    LOOP
      net := GREATEST(payment.amount-payment.refunded,0);
      remainder := LEAST(net,budget);
      IF net>0 THEN
        INSERT INTO wallet_cash_lot(wallet_id,recharge_order_id,source_transaction_id,source_kind,
          source_reference,original_amount,remaining_amount,currency,refundable,created_at)
        VALUES(w.id,payment.id,payment.wallet_transaction_id,'RECHARGE',payment.platform_order_no,
          net,remainder,w.currency,true,payment.credited_at)
        ON CONFLICT DO NOTHING;
        budget := GREATEST(budget-remainder,0);
      END IF;
    END LOOP;
    IF budget>0 THEN
      INSERT INTO wallet_cash_lot(wallet_id,source_kind,source_reference,original_amount,remaining_amount,
        currency,refundable,created_at)
      VALUES(w.id,'OPENING','migration:0012:opening',budget,budget,w.currency,false,now())
      ON CONFLICT DO NOTHING;
    END IF;
  END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION protect_wallet_cash_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION '% evidence cannot be deleted',TG_TABLE_NAME USING ERRCODE='55000';
  END IF;
  IF TG_TABLE_NAME='wallet_cash_allocation' THEN
    RAISE EXCEPTION 'wallet cash allocations are immutable' USING ERRCODE='55000';
  END IF;
  IF NEW.id=OLD.id AND NEW.wallet_id=OLD.wallet_id
     AND NEW.recharge_order_id IS NOT DISTINCT FROM OLD.recharge_order_id
     AND NEW.source_transaction_id IS NOT DISTINCT FROM OLD.source_transaction_id
     AND NEW.source_kind=OLD.source_kind AND NEW.source_reference=OLD.source_reference
     AND NEW.original_amount=OLD.original_amount AND NEW.currency=OLD.currency
     AND NEW.refundable=OLD.refundable AND NEW.created_at=OLD.created_at
     AND NEW.remaining_amount>=0 AND NEW.remaining_amount<=NEW.original_amount THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'wallet cash lot identity is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER wallet_cash_lot_protect_trigger BEFORE UPDATE OR DELETE ON wallet_cash_lot
  FOR EACH ROW EXECUTE FUNCTION protect_wallet_cash_evidence();
CREATE TRIGGER wallet_cash_allocation_protect_trigger BEFORE DELETE ON wallet_cash_allocation
  FOR EACH ROW EXECUTE FUNCTION protect_wallet_cash_evidence();

CREATE TABLE provider_statement (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  statement_reference text NOT NULL,
  period_start date NOT NULL,
  period_end date NOT NULL,
  region text NOT NULL CHECK (region ~ '^[A-Z]{2}$'),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  total_amount numeric(30,12) NOT NULL CHECK (total_amount >= 0),
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
  status text NOT NULL DEFAULT 'IMPORTED' CHECK (status IN ('IMPORTED','VERIFIED','DISPUTED')),
  imported_by uuid REFERENCES users(id) ON DELETE SET NULL,
  imported_at timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end>=period_start),
  UNIQUE(provider_id,statement_reference),
  UNIQUE(provider_id,source_sha256)
);
CREATE INDEX provider_statement_period_idx ON provider_statement(provider_id,period_start,period_end);

CREATE TABLE provider_statement_line (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_statement_id uuid NOT NULL REFERENCES provider_statement(id) ON DELETE RESTRICT,
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
  UNIQUE(provider_statement_id,external_line_id)
);
CREATE INDEX provider_statement_line_request_idx ON provider_statement_line(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX provider_statement_line_upstream_idx ON provider_statement_line(upstream_request_id) WHERE upstream_request_id IS NOT NULL;

CREATE TABLE refund_application (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  application_number text NOT NULL UNIQUE,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  source_type text NOT NULL CHECK (source_type IN ('RECHARGE','SUBSCRIPTION')),
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  subscription_invoice_id uuid REFERENCES subscription_invoice(id) ON DELETE RESTRICT,
  requested_amount numeric(30,12) NOT NULL CHECK (requested_amount>0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unused_cash_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (unused_cash_amount>=0),
  used_service_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (used_service_amount>=0),
  bonus_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (bonus_amount>=0),
  subscription_fee_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (subscription_fee_amount>=0),
  provider_irrecoverable_cost numeric(30,12) NOT NULL DEFAULT 0 CHECK (provider_irrecoverable_cost>=0),
  reason text NOT NULL,
  idempotency_key text NOT NULL,
  status text NOT NULL DEFAULT 'SUBMITTED' CHECK (status IN
    ('SUBMITTED','UNDER_REVIEW','APPROVED','REJECTED','PROCESSING','COMPLETED','FAILED','CANCELED')),
  refund_order_id uuid REFERENCES refund_order(id) ON DELETE RESTRICT,
  requested_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  review_reason text,
  reviewed_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((source_type='RECHARGE' AND recharge_order_id IS NOT NULL AND subscription_invoice_id IS NULL)
      OR (source_type='SUBSCRIPTION' AND subscription_invoice_id IS NOT NULL AND recharge_order_id IS NULL)),
  UNIQUE(organization_id,idempotency_key),
  UNIQUE(refund_order_id)
);
CREATE INDEX refund_application_queue_idx ON refund_application(status,created_at,id);
ALTER TABLE refund_order
  ADD COLUMN IF NOT EXISTS refund_application_id uuid REFERENCES refund_application(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX IF NOT EXISTS refund_order_application_uq ON refund_order(refund_application_id)
  WHERE refund_application_id IS NOT NULL;

CREATE TABLE invoice_application (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  application_number text NOT NULL UNIQUE,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  invoice_title text NOT NULL,
  tax_identifier text,
  amount numeric(30,12) NOT NULL CHECK (amount>0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  period_start date NOT NULL,
  period_end date NOT NULL,
  status text NOT NULL DEFAULT 'SUBMITTED' CHECK (status IN
    ('SUBMITTED','UNDER_REVIEW','APPROVED','REJECTED','EXPORTED','CANCELED')),
  idempotency_key text NOT NULL,
  requested_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  processed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  processing_reason text,
  processed_at timestamptz,
  exported_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end>=period_start),
  UNIQUE(organization_id,idempotency_key)
);
CREATE INDEX invoice_application_queue_idx ON invoice_application(status,created_at,id);

CREATE TABLE invoice_application_item (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_application_id uuid NOT NULL REFERENCES invoice_application(id) ON DELETE RESTRICT,
  source_type text NOT NULL CHECK (source_type IN ('RECHARGE','SUBSCRIPTION')),
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  subscription_invoice_id uuid REFERENCES subscription_invoice(id) ON DELETE RESTRICT,
  amount numeric(30,12) NOT NULL CHECK (amount>0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((source_type='RECHARGE' AND recharge_order_id IS NOT NULL AND subscription_invoice_id IS NULL)
      OR (source_type='SUBSCRIPTION' AND subscription_invoice_id IS NOT NULL AND recharge_order_id IS NULL)),
  UNIQUE(invoice_application_id,source_type,recharge_order_id,subscription_invoice_id)
);
CREATE INDEX invoice_application_item_recharge_idx ON invoice_application_item(recharge_order_id) WHERE recharge_order_id IS NOT NULL;
CREATE INDEX invoice_application_item_subscription_idx ON invoice_application_item(subscription_invoice_id) WHERE subscription_invoice_id IS NOT NULL;

CREATE TABLE financial_reconciliation_run (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_key text NOT NULL UNIQUE,
  business_date date NOT NULL,
  status text NOT NULL DEFAULT 'RUNNING' CHECK (status IN ('RUNNING','COMPLETED','FAILED')),
  trigger_source text NOT NULL CHECK (trigger_source IN ('SCHEDULED','MANUAL','TEST')),
  summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(summary)='object'),
  started_by uuid REFERENCES users(id) ON DELETE SET NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  error_code text,
  CHECK ((status='RUNNING')=(completed_at IS NULL))
);
CREATE INDEX financial_reconciliation_run_date_idx ON financial_reconciliation_run(business_date DESC,started_at DESC);

CREATE TABLE financial_reconciliation_case (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  case_key text NOT NULL UNIQUE,
  check_type text NOT NULL CHECK (check_type IN
    ('PAYMENT_TO_RECHARGE','RECHARGE_TO_WALLET','USAGE_TO_USER_CHARGE','USAGE_TO_PROVIDER_USAGE',
     'PROVIDER_USAGE_TO_BILL','SUBSCRIPTION_TO_STATE')),
  classification text NOT NULL,
  severity text NOT NULL CHECK (severity IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','IN_REVIEW','RESOLVED','ACCEPTED')),
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  provider_id uuid REFERENCES providers(id) ON DELETE RESTRICT,
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  funding_operation_id uuid REFERENCES funding_operation(id) ON DELETE RESTRICT,
  subscription_invoice_id uuid REFERENCES subscription_invoice(id) ON DELETE RESTRICT,
  expected_amount numeric(30,12),
  actual_amount numeric(30,12),
  currency text CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
  details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
  first_seen_run_id uuid NOT NULL REFERENCES financial_reconciliation_run(id) ON DELETE RESTRICT,
  last_seen_run_id uuid NOT NULL REFERENCES financial_reconciliation_run(id) ON DELETE RESTRICT,
  occurrence_count bigint NOT NULL DEFAULT 1 CHECK (occurrence_count>0),
  handled_by uuid REFERENCES users(id) ON DELETE SET NULL,
  handling_reason text,
  handled_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX financial_reconciliation_case_queue_idx ON financial_reconciliation_case(status,severity,created_at,id);

CREATE TABLE financial_reconciliation_observation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL REFERENCES financial_reconciliation_run(id) ON DELETE RESTRICT,
  observation_key text NOT NULL,
  check_type text NOT NULL,
  result text NOT NULL CHECK (result IN ('MATCHED','MISMATCH','ERROR')),
  case_id uuid REFERENCES financial_reconciliation_case(id) ON DELETE RESTRICT,
  details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
  observed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,observation_key),
  CHECK ((result='MATCHED')=(case_id IS NULL))
);

ALTER TABLE ledger_journal DROP CONSTRAINT IF EXISTS ledger_journal_journal_type_check;
ALTER TABLE ledger_journal ADD CONSTRAINT ledger_journal_journal_type_check CHECK (
  journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL',
                   'LATE_USAGE_ADJUSTMENT','PAYMENT_CREDIT','PAYMENT_REFUND',
                   'SUBSCRIPTION_PAYMENT','SUBSCRIPTION_REFUND','RECONCILIATION_REVERSAL')
);
ALTER TABLE ledger_journal
  ADD COLUMN IF NOT EXISTS reversal_of_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS reconciliation_case_id uuid REFERENCES financial_reconciliation_case(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ledger_journal_reversal_case_uq ON ledger_journal(reconciliation_case_id)
  WHERE reconciliation_case_id IS NOT NULL;

CREATE TABLE financial_reconciliation_resolution (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reconciliation_case_id uuid NOT NULL REFERENCES financial_reconciliation_case(id) ON DELETE RESTRICT,
  action text NOT NULL CHECK (action IN ('REVERSE_JOURNAL','ACCEPT_EXCEPTION')),
  source_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  reversal_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  reason text NOT NULL,
  idempotency_key text NOT NULL,
  resolved_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(reconciliation_case_id,idempotency_key),
  UNIQUE(reversal_journal_id),
  CHECK ((action='REVERSE_JOURNAL')=(source_journal_id IS NOT NULL AND reversal_journal_id IS NOT NULL))
);

CREATE OR REPLACE FUNCTION reject_financial_evidence_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable',TG_TABLE_NAME USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER provider_statement_immutable_trigger BEFORE UPDATE OR DELETE ON provider_statement
  FOR EACH ROW EXECUTE FUNCTION reject_financial_evidence_mutation();
CREATE TRIGGER provider_statement_line_immutable_trigger BEFORE UPDATE OR DELETE ON provider_statement_line
  FOR EACH ROW EXECUTE FUNCTION reject_financial_evidence_mutation();
CREATE TRIGGER invoice_application_item_immutable_trigger BEFORE UPDATE OR DELETE ON invoice_application_item
  FOR EACH ROW EXECUTE FUNCTION reject_financial_evidence_mutation();
CREATE TRIGGER financial_observation_immutable_trigger BEFORE UPDATE OR DELETE ON financial_reconciliation_observation
  FOR EACH ROW EXECUTE FUNCTION reject_financial_evidence_mutation();
CREATE TRIGGER financial_resolution_immutable_trigger BEFORE UPDATE OR DELETE ON financial_reconciliation_resolution
  FOR EACH ROW EXECUTE FUNCTION reject_financial_evidence_mutation();

-- Replace the cumulative journal protection function so every linkage added
-- by migrations 0010-0012 is immutable once the draft is posted.
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
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;
