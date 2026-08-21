-- Verified recharge orders, payment attempts, webhook evidence, refunds, and
-- reconciliation records. Forward-only migration; see docs/payments.md for
-- guarded rollback guidance. No payment credential or webhook secret is
-- stored in PostgreSQL.

ALTER TABLE ledger_journal DROP CONSTRAINT IF EXISTS ledger_journal_journal_type_check;
ALTER TABLE ledger_journal ADD CONSTRAINT ledger_journal_journal_type_check CHECK (
  journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL',
                   'LATE_USAGE_ADJUSTMENT','PAYMENT_CREDIT','PAYMENT_REFUND')
);

CREATE TABLE IF NOT EXISTS recharge_order (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  platform_order_no text NOT NULL UNIQUE,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  payment_provider text NOT NULL CHECK (payment_provider ~ '^[a-z][a-z0-9_-]{1,31}$'),
  provider_order_no text,
  status text NOT NULL DEFAULT 'CREATED' CHECK (status IN (
    'CREATED','PENDING','PAID','CREDITED','FAILED','EXPIRED',
    'REFUND_PENDING','REFUNDED','CHARGEBACK'
  )),
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  region text NOT NULL CHECK (region ~ '^[A-Z]{2}$'),
  idempotency_key text NOT NULL,
  request_fingerprint text NOT NULL,
  wallet_transaction_id uuid REFERENCES wallet_transactions(id) ON DELETE RESTRICT,
  ledger_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  expires_at timestamptz NOT NULL,
  paid_at timestamptz,
  credited_at timestamptz,
  provider_closed_at timestamptz,
  failure_code text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key),
  UNIQUE(payment_provider,provider_order_no),
  UNIQUE(wallet_transaction_id),
  UNIQUE(ledger_journal_id),
  UNIQUE(id,wallet_id),
  CHECK ((status IN ('PAID','CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')) = (paid_at IS NOT NULL)),
  CHECK ((status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')) = (credited_at IS NOT NULL)),
  CHECK ((wallet_transaction_id IS NULL) = (ledger_journal_id IS NULL)),
  CHECK (status NOT IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK') OR wallet_transaction_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS recharge_order_org_created_idx ON recharge_order(organization_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS recharge_order_recovery_idx ON recharge_order(status,updated_at)
  WHERE status IN ('PAID','PENDING','REFUND_PENDING');
CREATE INDEX IF NOT EXISTS recharge_order_expiry_idx ON recharge_order(expires_at)
  WHERE status IN ('CREATED','PENDING');

CREATE TABLE IF NOT EXISTS payment_attempt (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recharge_order_id uuid NOT NULL REFERENCES recharge_order(id) ON DELETE RESTRICT,
  attempt_no integer NOT NULL CHECK (attempt_no > 0),
  operation text NOT NULL CHECK (operation IN ('CREATE','QUERY','VERIFY_WEBHOOK','REFUND','CLOSE','RECONCILE','MANUAL_REVIEW')),
  status text NOT NULL CHECK (status IN ('STARTED','SUCCEEDED','FAILED','PENDING')),
  provider_order_no text,
  request_hash text,
  response_code text,
  error_code text,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  UNIQUE(recharge_order_id,attempt_no),
  CHECK ((status='STARTED') = (finished_at IS NULL))
);
CREATE INDEX IF NOT EXISTS payment_attempt_order_idx ON payment_attempt(recharge_order_id,attempt_no DESC);

CREATE TABLE IF NOT EXISTS payment_webhook_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_provider text NOT NULL,
  provider_event_id text NOT NULL,
  replay_key text NOT NULL,
  provider_order_no text NOT NULL,
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  event_type text NOT NULL,
  payment_status text NOT NULL,
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  provider_timestamp timestamptz NOT NULL,
  raw_body_sha256 text NOT NULL CHECK (raw_body_sha256 ~ '^[0-9a-f]{64}$'),
  signature_valid boolean NOT NULL CHECK (signature_valid),
  timestamp_valid boolean NOT NULL CHECK (timestamp_valid),
  processing_status text NOT NULL DEFAULT 'RECEIVED' CHECK (processing_status IN ('RECEIVED','PROCESSED','REJECTED')),
  error_code text,
  normalized_payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(normalized_payload)='object'),
  received_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  UNIQUE(payment_provider,provider_event_id),
  UNIQUE(payment_provider,replay_key)
);
CREATE INDEX IF NOT EXISTS payment_webhook_recovery_idx ON payment_webhook_event(processing_status,received_at)
  WHERE processing_status='RECEIVED';
CREATE INDEX IF NOT EXISTS payment_webhook_order_idx ON payment_webhook_event(recharge_order_id,received_at DESC);

CREATE TABLE IF NOT EXISTS refund_order (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  platform_refund_no text NOT NULL UNIQUE,
  recharge_order_id uuid NOT NULL REFERENCES recharge_order(id) ON DELETE RESTRICT,
  payment_provider text NOT NULL,
  provider_refund_no text,
  status text NOT NULL CHECK (status IN ('CREATED','PENDING','SUCCEEDED','FAILED','CLOSED')),
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  reason text NOT NULL,
  idempotency_key text NOT NULL,
  wallet_transaction_id uuid REFERENCES wallet_transactions(id) ON DELETE RESTRICT,
  ledger_journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  failure_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE(recharge_order_id,idempotency_key),
  UNIQUE(payment_provider,provider_refund_no),
  UNIQUE(wallet_transaction_id),
  UNIQUE(ledger_journal_id),
  CHECK ((wallet_transaction_id IS NULL) = (ledger_journal_id IS NULL)),
  CHECK (status<>'SUCCEEDED' OR wallet_transaction_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS refund_order_recharge_idx ON refund_order(recharge_order_id,created_at DESC);

CREATE TABLE IF NOT EXISTS payment_reconciliation_record (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  refund_order_id uuid REFERENCES refund_order(id) ON DELETE RESTRICT,
  payment_provider text NOT NULL,
  provider_order_no text,
  reconciliation_key text NOT NULL,
  provider_status text NOT NULL,
  local_status text NOT NULL,
  provider_amount numeric(30,12) CHECK (provider_amount IS NULL OR provider_amount >= 0),
  local_amount numeric(30,12) CHECK (local_amount IS NULL OR local_amount >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  result text NOT NULL CHECK (result IN ('MATCHED','MISMATCH','MISSING_LOCAL','MISSING_PROVIDER','ERROR')),
  details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
  reconciled_by uuid REFERENCES users(id) ON DELETE SET NULL,
  reconciled_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(payment_provider,reconciliation_key),
  CHECK (recharge_order_id IS NOT NULL OR refund_order_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS payment_reconciliation_order_idx ON payment_reconciliation_record(recharge_order_id,reconciled_at DESC);

ALTER TABLE wallet_transactions
  ADD COLUMN IF NOT EXISTS recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS refund_order_id uuid REFERENCES refund_order(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX IF NOT EXISTS wallet_transactions_recharge_unique_idx ON wallet_transactions(recharge_order_id) WHERE recharge_order_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS wallet_transactions_refund_unique_idx ON wallet_transactions(refund_order_id) WHERE refund_order_id IS NOT NULL;

ALTER TABLE ledger_journal
  ADD COLUMN IF NOT EXISTS recharge_order_id uuid REFERENCES recharge_order(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS refund_order_id uuid REFERENCES refund_order(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX IF NOT EXISTS ledger_journal_recharge_unique_idx ON ledger_journal(recharge_order_id) WHERE recharge_order_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ledger_journal_refund_unique_idx ON ledger_journal(refund_order_id) WHERE refund_order_id IS NOT NULL;

-- Migration 0009 predates the payment linkage columns. Keep those columns
-- immutable when a draft journal is posted as well.
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
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;

CREATE OR REPLACE FUNCTION reject_immutable_payment_evidence()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable',TG_TABLE_NAME USING ERRCODE='55000';
END;
$$;
CREATE OR REPLACE FUNCTION protect_payment_webhook_event()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'payment webhook events cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF OLD.processing_status='RECEIVED'
     AND NEW.id=OLD.id AND NEW.payment_provider=OLD.payment_provider
     AND NEW.provider_event_id=OLD.provider_event_id AND NEW.replay_key=OLD.replay_key
     AND NEW.provider_order_no=OLD.provider_order_no AND NEW.recharge_order_id IS NOT DISTINCT FROM OLD.recharge_order_id
     AND NEW.event_type=OLD.event_type AND NEW.payment_status=OLD.payment_status
     AND NEW.amount=OLD.amount AND NEW.currency=OLD.currency
     AND NEW.provider_timestamp=OLD.provider_timestamp AND NEW.raw_body_sha256=OLD.raw_body_sha256
     AND NEW.signature_valid=OLD.signature_valid AND NEW.timestamp_valid=OLD.timestamp_valid
     AND NEW.normalized_payload=OLD.normalized_payload AND NEW.received_at=OLD.received_at
     AND NEW.processing_status IN ('PROCESSED','REJECTED') AND NEW.processed_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'terminal payment webhook events are immutable' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS payment_webhook_event_protect_trigger ON payment_webhook_event;
CREATE TRIGGER payment_webhook_event_protect_trigger BEFORE UPDATE OR DELETE ON payment_webhook_event
  FOR EACH ROW EXECUTE FUNCTION protect_payment_webhook_event();
DROP TRIGGER IF EXISTS payment_attempt_immutable_trigger ON payment_attempt;
CREATE TRIGGER payment_attempt_immutable_trigger BEFORE DELETE ON payment_attempt
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_payment_evidence();
CREATE TRIGGER payment_attempt_terminal_immutable_trigger BEFORE UPDATE ON payment_attempt
  FOR EACH ROW WHEN (OLD.status<>'STARTED') EXECUTE FUNCTION reject_immutable_payment_evidence();
DROP TRIGGER IF EXISTS payment_reconciliation_immutable_trigger ON payment_reconciliation_record;
CREATE TRIGGER payment_reconciliation_immutable_trigger BEFORE UPDATE OR DELETE ON payment_reconciliation_record
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_payment_evidence();
