-- Financial-close hardening discovered during production-readiness review.
-- Forward-only: this migration adds evidence, constraints, and accounting
-- links without deleting or rewriting posted financial records.

-- CASH HARDENING

-- A wallet reservation reduces the aggregate available balance before the
-- Provider request starts.  Persist the same hold against source cash lots so
-- "unused cash" and refund eligibility cannot include money committed to an
-- in-flight request.  Settlement consumes the hold; release returns it.
CREATE TABLE funding_cash_allocation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  cash_lot_id uuid NOT NULL REFERENCES wallet_cash_lot(id) ON DELETE RESTRICT,
  reserved_amount numeric(30,12) NOT NULL CHECK (reserved_amount>0),
  consumed_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (consumed_amount>=0),
  released_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (released_amount>=0),
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  CHECK (consumed_amount+released_amount=reserved_amount OR consumed_amount+released_amount=0),
  UNIQUE(operation_id,cash_lot_id)
);
CREATE INDEX funding_cash_allocation_lot_idx
  ON funding_cash_allocation(cash_lot_id,created_at,id);

-- Payment-provider refunds can remain pending after the local order is
-- created. Hold the exact source lot during that interval so it cannot fund a
-- Provider request before the verified refund result arrives.
CREATE TABLE refund_cash_allocation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  refund_order_id uuid NOT NULL UNIQUE REFERENCES refund_order(id) ON DELETE RESTRICT,
  cash_lot_id uuid NOT NULL REFERENCES wallet_cash_lot(id) ON DELETE RESTRICT,
  reserved_amount numeric(30,12) NOT NULL CHECK (reserved_amount>0),
  consumed_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (consumed_amount>=0),
  released_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (released_amount>=0),
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  CHECK (consumed_amount+released_amount=reserved_amount OR consumed_amount+released_amount=0)
);
CREATE INDEX refund_cash_allocation_lot_idx
  ON refund_cash_allocation(cash_lot_id,created_at,id);

-- Upgrade databases may contain requests admitted by a schema-12 process.
-- Schema 0012 created lots only for the then-current available balance; cash
-- already moved to wallets.reserved_balance therefore has no source lot. Do
-- not subtract the reservation a second time from those available lots.
-- Instead, reconstruct the cash-backed portion of the aggregate hold as a
-- conservative, non-refundable migration lot. The remainder of each hold is
-- credit. When the operation releases, that exact cash portion returns to the
-- migration lot; when it settles, the lot remains consumed.
DO $$
DECLARE wallet_row record; operation_row record; lot_row record;
        outstanding numeric(30,12); take_amount numeric(30,12);
        aggregate_cash_budget numeric(30,12); migration_lot_id uuid; schema_12_applied_at timestamptz;
BEGIN
  SELECT applied_at INTO schema_12_applied_at FROM schema_migrations WHERE version=12;
  IF schema_12_applied_at IS NULL THEN
    RAISE EXCEPTION 'schema migration 12 timestamp is required for schema-13 funding backfill'
      USING ERRCODE='23514';
  END IF;
  FOR wallet_row IN
    SELECT wallet.id,wallet.currency,wallet.available_balance,wallet.reserved_balance,
           COALESCE((SELECT sum(operation.maximum_amount) FROM funding_operation operation
                     WHERE operation.wallet_id=wallet.id AND operation.status IN ('PENDING','RESERVED')
                       AND operation.reserved_at<schema_12_applied_at),0) legacy_active_reservations,
           COALESCE((SELECT sum(operation.maximum_amount) FROM funding_operation operation
                     WHERE operation.wallet_id=wallet.id AND operation.status IN ('PENDING','RESERVED')
                       AND operation.reserved_at>=schema_12_applied_at),0) schema_12_active_reservations
    FROM wallets wallet
    WHERE EXISTS (SELECT 1 FROM funding_operation operation
                  WHERE operation.wallet_id=wallet.id AND operation.status IN ('PENDING','RESERVED'))
    ORDER BY wallet.id FOR UPDATE
  LOOP
    aggregate_cash_budget := GREATEST(LEAST(wallet_row.legacy_active_reservations,
      GREATEST(wallet_row.reserved_balance-wallet_row.schema_12_active_reservations,0),
      GREATEST(wallet_row.available_balance+wallet_row.reserved_balance,0),
      GREATEST(wallet_row.available_balance+wallet_row.reserved_balance-
        COALESCE((SELECT sum(remaining_amount) FROM wallet_cash_lot lot WHERE lot.wallet_id=wallet_row.id),0),0)),0);
    IF aggregate_cash_budget>0 THEN
      migration_lot_id := gen_random_uuid();
      INSERT INTO wallet_cash_lot(id,wallet_id,source_kind,source_reference,original_amount,
        remaining_amount,currency,refundable,created_at)
      VALUES(migration_lot_id,wallet_row.id,'OPENING','migration:0013:reserved:'||wallet_row.id,
        aggregate_cash_budget,0,wallet_row.currency,false,
        COALESCE((SELECT min(reserved_at) FROM funding_operation
                  WHERE wallet_id=wallet_row.id AND status IN ('PENDING','RESERVED')),now()));
    END IF;
    FOR operation_row IN
      SELECT id,maximum_amount FROM funding_operation
      WHERE wallet_id=wallet_row.id AND status IN ('PENDING','RESERVED') AND reserved_at<schema_12_applied_at
      ORDER BY reserved_at,id FOR UPDATE
    LOOP
      EXIT WHEN aggregate_cash_budget<=0;
      outstanding := LEAST(operation_row.maximum_amount,aggregate_cash_budget);
      IF outstanding>0 THEN
        take_amount := outstanding;
        INSERT INTO funding_cash_allocation(operation_id,cash_lot_id,reserved_amount)
        VALUES(operation_row.id,migration_lot_id,take_amount);
        aggregate_cash_budget := aggregate_cash_budget-take_amount;
      END IF;
    END LOOP;

    -- Operations admitted by a schema-12 binary were created after its
    -- migration timestamp. Their maximum amount was removed from an already
    -- source-attributed balance, so freeze the same FIFO lots in place.
    FOR operation_row IN
      SELECT id,maximum_amount FROM funding_operation
      WHERE wallet_id=wallet_row.id AND status IN ('PENDING','RESERVED') AND reserved_at>=schema_12_applied_at
      ORDER BY reserved_at,id FOR UPDATE
    LOOP
      outstanding := operation_row.maximum_amount;
      FOR lot_row IN
        SELECT id,remaining_amount FROM wallet_cash_lot
        WHERE wallet_id=wallet_row.id AND remaining_amount>0
        ORDER BY created_at,id FOR UPDATE
      LOOP
        EXIT WHEN outstanding<=0;
        take_amount := LEAST(outstanding,lot_row.remaining_amount);
        UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-take_amount WHERE id=lot_row.id;
        INSERT INTO funding_cash_allocation(operation_id,cash_lot_id,reserved_amount)
        VALUES(operation_row.id,lot_row.id,take_amount);
        outstanding := outstanding-take_amount;
      END LOOP;
    END LOOP;
  END LOOP;
END;
$$;

-- Schema-12 refund creation did not hold its source lot while the Provider
-- refund was CREATED/PENDING. Freeze those exact still-unused funds before the
-- new binary becomes writable. If the cash is no longer present, abort the
-- migration instead of silently fabricating refundable value or completing an
-- externally unsafe refund.
DO $$
DECLARE refund_row record; lot_id uuid; lot_remaining numeric(30,12);
BEGIN
  FOR refund_row IN
    SELECT refund.id,refund.amount,recharge.id recharge_order_id,recharge.wallet_id
    FROM refund_order refund
    JOIN recharge_order recharge ON recharge.id=refund.recharge_order_id
    WHERE refund.status IN ('CREATED','PENDING')
    ORDER BY recharge.wallet_id,refund.created_at,refund.id
    FOR UPDATE OF refund,recharge
  LOOP
    lot_id := NULL;
    lot_remaining := NULL;
    SELECT id,remaining_amount INTO lot_id,lot_remaining
    FROM wallet_cash_lot
    WHERE wallet_id=refund_row.wallet_id AND recharge_order_id=refund_row.recharge_order_id AND refundable
    FOR UPDATE;
    IF lot_id IS NULL OR lot_remaining<refund_row.amount THEN
      RAISE EXCEPTION 'pending refund % lacks unused cash for schema-13 hold',refund_row.id USING ERRCODE='23514';
    END IF;
    UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-refund_row.amount WHERE id=lot_id;
    INSERT INTO refund_cash_allocation(refund_order_id,cash_lot_id,reserved_amount)
    VALUES(refund_row.id,lot_id,refund_row.amount);
  END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION protect_funding_cash_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'funding cash allocations cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF NEW.id=OLD.id AND NEW.operation_id=OLD.operation_id
     AND NEW.cash_lot_id=OLD.cash_lot_id
     AND NEW.reserved_amount=OLD.reserved_amount AND NEW.created_at=OLD.created_at
     AND NEW.consumed_amount>=OLD.consumed_amount
     AND NEW.released_amount>=OLD.released_amount
     AND NEW.consumed_amount+NEW.released_amount<=NEW.reserved_amount
     AND (NEW.settled_at IS NOT DISTINCT FROM OLD.settled_at
          OR (OLD.settled_at IS NULL AND NEW.settled_at IS NOT NULL)) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'funding cash allocation evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER funding_cash_allocation_protect_trigger
  BEFORE UPDATE OR DELETE ON funding_cash_allocation
  FOR EACH ROW EXECUTE FUNCTION protect_funding_cash_allocation();

CREATE OR REPLACE FUNCTION protect_refund_cash_allocation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'refund cash allocations cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF NEW.id=OLD.id AND NEW.refund_order_id=OLD.refund_order_id
     AND NEW.cash_lot_id=OLD.cash_lot_id
     AND NEW.reserved_amount=OLD.reserved_amount AND NEW.created_at=OLD.created_at
     AND NEW.consumed_amount>=OLD.consumed_amount
     AND NEW.released_amount>=OLD.released_amount
     AND NEW.consumed_amount+NEW.released_amount<=NEW.reserved_amount
     AND (NEW.settled_at IS NOT DISTINCT FROM OLD.settled_at
          OR (OLD.settled_at IS NULL AND NEW.settled_at IS NOT NULL)) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'refund cash allocation evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER refund_cash_allocation_protect_trigger
  BEFORE UPDATE OR DELETE ON refund_cash_allocation
  FOR EACH ROW EXECUTE FUNCTION protect_refund_cash_allocation();

-- RECONCILIATION HARDENING

-- One source journal is an economic event.  Reversing it in two different
-- reconciliation cases would duplicate the correction even though each case
-- and idempotency key is individually unique.
CREATE UNIQUE INDEX ledger_journal_reversal_source_uq
  ON ledger_journal(reversal_of_journal_id)
  WHERE reversal_of_journal_id IS NOT NULL;

-- INVOICE AND PROVIDER ACCOUNTING HARDENING

-- Persist the exact artifact before an invoice export is acknowledged.  A
-- client may retry by batch key and download identical bytes even if its
-- previous HTTP response was interrupted.
CREATE TABLE invoice_export_batch (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_key text NOT NULL UNIQUE,
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
  artifact_sha256 text NOT NULL CHECK (artifact_sha256 ~ '^[0-9a-f]{64}$'),
  artifact bytea NOT NULL,
  row_count integer NOT NULL CHECK (row_count >= 0),
  created_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE invoice_application
  ADD COLUMN invoice_export_batch_id uuid REFERENCES invoice_export_batch(id) ON DELETE RESTRICT;
CREATE INDEX invoice_application_export_batch_idx ON invoice_application(invoice_export_batch_id)
  WHERE invoice_export_batch_id IS NOT NULL;

CREATE OR REPLACE FUNCTION protect_invoice_export_batch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'invoice export artifacts are immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER invoice_export_batch_immutable_trigger BEFORE UPDATE OR DELETE ON invoice_export_batch
  FOR EACH ROW EXECUTE FUNCTION protect_invoice_export_batch();

-- Imported Provider statements are actual incurred cost evidence.  Link a
-- balanced expense/payable journal once per statement so accounting exports
-- contain the Provider side of the close as well as customer revenue.
ALTER TABLE ledger_journal
  ADD COLUMN provider_statement_id uuid REFERENCES provider_statement(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ledger_journal_provider_statement_uq ON ledger_journal(provider_statement_id)
  WHERE provider_statement_id IS NOT NULL;
ALTER TABLE provider_statement
  ADD COLUMN import_fingerprint_sha256 text
  CHECK (import_fingerprint_sha256 IS NULL OR import_fingerprint_sha256 ~ '^[0-9a-f]{64}$');

ALTER TABLE ledger_journal DROP CONSTRAINT ledger_journal_journal_type_check;
ALTER TABLE ledger_journal ADD CONSTRAINT ledger_journal_journal_type_check CHECK (
  journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL',
                   'LATE_USAGE_ADJUSTMENT','PAYMENT_CREDIT','PAYMENT_REFUND',
                   'SUBSCRIPTION_PAYMENT','SUBSCRIPTION_REFUND','RECONCILIATION_REVERSAL',
                   'PROVIDER_STATEMENT')
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
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;

INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:provider-expense:'||currency,'Provider expense','EXPENSE','DEBIT',currency
FROM (SELECT currency FROM (VALUES ('USD'),('CNY')) seed(currency)
      UNION SELECT DISTINCT currency FROM provider_statement) currency_set
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:provider-payable:'||currency,'Provider payable','LIABILITY','CREDIT',currency
FROM (SELECT currency FROM (VALUES ('USD'),('CNY')) seed(currency)
      UNION SELECT DISTINCT currency FROM provider_statement) currency_set
ON CONFLICT(account_key) DO NOTHING;
