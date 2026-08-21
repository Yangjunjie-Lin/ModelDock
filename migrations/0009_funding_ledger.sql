-- Concurrent funding reservations and immutable double-entry ledger.
-- Forward-only migration. See docs/funding-ledger.md for the guarded rollback
-- procedure; posted journals are financial evidence and are never rewritten.

ALTER TABLE wallets
  ADD COLUMN IF NOT EXISTS risk_limit numeric(30,12) NOT NULL DEFAULT 0.010000000000 CHECK (risk_limit >= 0),
  ADD COLUMN IF NOT EXISTS risk_exposure numeric(30,12) NOT NULL DEFAULT 0 CHECK (risk_exposure >= 0),
  ADD COLUMN IF NOT EXISTS credit_enforced boolean NOT NULL DEFAULT true;

-- A zero credit limit historically meant unlimited POSTPAID compatibility.
-- Preserve those existing organizations, while newly created wallets enforce
-- their explicit credit limit (including a zero limit) by default.
UPDATE wallets
SET credit_enforced=false
WHERE billing_mode='POSTPAID' AND credit_limit=0;

CREATE TABLE IF NOT EXISTS ledger_account (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid REFERENCES wallets(id) ON DELETE RESTRICT,
  account_key text NOT NULL UNIQUE,
  name text NOT NULL,
  account_type text NOT NULL CHECK (account_type IN ('ASSET','LIABILITY','EQUITY','REVENUE','EXPENSE')),
  normal_side text NOT NULL CHECK (normal_side IN ('DEBIT','CREDIT')),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','CLOSED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(id,currency)
);
CREATE INDEX IF NOT EXISTS ledger_account_wallet_idx ON ledger_account(wallet_id,account_key);

CREATE TABLE IF NOT EXISTS ledger_journal (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid REFERENCES wallets(id) ON DELETE RESTRICT,
  journal_type text NOT NULL CHECK (journal_type IN ('OPENING','TOPUP','ADJUSTMENT','RESERVATION','SETTLEMENT','RELEASE','REVERSAL','LATE_USAGE_ADJUSTMENT')),
  external_key text NOT NULL UNIQUE,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','POSTED')),
  reference text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  posted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status='DRAFT' AND posted_at IS NULL) OR (status='POSTED' AND posted_at IS NOT NULL)),
  UNIQUE(id,currency)
);
CREATE INDEX IF NOT EXISTS ledger_journal_wallet_created_idx ON ledger_journal(wallet_id,created_at DESC);

CREATE TABLE IF NOT EXISTS ledger_journal_entry (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  journal_id uuid NOT NULL,
  account_id uuid NOT NULL,
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  entry_side text NOT NULL CHECK (entry_side IN ('DEBIT','CREDIT')),
  amount numeric(30,12) NOT NULL CHECK (amount > 0),
  description text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY(journal_id,currency) REFERENCES ledger_journal(id,currency) ON DELETE RESTRICT,
  FOREIGN KEY(account_id,currency) REFERENCES ledger_account(id,currency) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS ledger_journal_entry_journal_idx ON ledger_journal_entry(journal_id,id);
CREATE INDEX IF NOT EXISTS ledger_journal_entry_account_idx ON ledger_journal_entry(account_id,created_at,id);

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
     AND NEW.created_at=OLD.created_at AND NEW.posted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'posted ledger journals are immutable' USING ERRCODE='55000';
END;
$$;

CREATE OR REPLACE FUNCTION protect_ledger_journal_entry()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE journal_status text;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    RAISE EXCEPTION 'ledger journal entries are immutable' USING ERRCODE='55000';
  END IF;
  SELECT status INTO journal_status FROM ledger_journal WHERE id=NEW.journal_id;
  IF journal_status <> 'DRAFT' THEN
    RAISE EXCEPTION 'entries can only be added to a draft journal' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_balanced_posted_journal()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE debit_total numeric(30,12); credit_total numeric(30,12);
BEGIN
  IF NEW.status='POSTED' THEN
    SELECT COALESCE(sum(amount) FILTER (WHERE entry_side='DEBIT'),0),
           COALESCE(sum(amount) FILTER (WHERE entry_side='CREDIT'),0)
      INTO debit_total,credit_total
      FROM ledger_journal_entry WHERE journal_id=NEW.id;
    IF debit_total=0 OR debit_total<>credit_total THEN
      RAISE EXCEPTION 'posted journal % is not balanced',NEW.id USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS ledger_journal_protect_trigger ON ledger_journal;
CREATE TRIGGER ledger_journal_protect_trigger BEFORE UPDATE OR DELETE ON ledger_journal
  FOR EACH ROW EXECUTE FUNCTION protect_posted_ledger_journal();
DROP TRIGGER IF EXISTS ledger_journal_entry_protect_trigger ON ledger_journal_entry;
CREATE TRIGGER ledger_journal_entry_protect_trigger BEFORE INSERT OR UPDATE OR DELETE ON ledger_journal_entry
  FOR EACH ROW EXECUTE FUNCTION protect_ledger_journal_entry();
DROP TRIGGER IF EXISTS ledger_journal_balance_trigger ON ledger_journal;
CREATE TRIGGER ledger_journal_balance_trigger BEFORE UPDATE OF status ON ledger_journal
  FOR EACH ROW EXECUTE FUNCTION enforce_balanced_posted_journal();
DROP TRIGGER IF EXISTS ledger_journal_insert_balance_trigger ON ledger_journal;
CREATE TRIGGER ledger_journal_insert_balance_trigger BEFORE INSERT ON ledger_journal
  FOR EACH ROW WHEN (NEW.status='POSTED') EXECUTE FUNCTION enforce_balanced_posted_journal();

CREATE OR REPLACE FUNCTION reject_ledger_account_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'ledger accounts are immutable' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS ledger_account_immutable_trigger ON ledger_account;
CREATE TRIGGER ledger_account_immutable_trigger BEFORE UPDATE OR DELETE ON ledger_account
  FOR EACH ROW EXECUTE FUNCTION reject_ledger_account_mutation();

CREATE OR REPLACE FUNCTION reject_immutable_wallet_transaction()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'wallet transactions are immutable; post a reversal instead' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS wallet_transactions_immutable_trigger ON wallet_transactions;
CREATE TRIGGER wallet_transactions_immutable_trigger BEFORE UPDATE OR DELETE ON wallet_transactions
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_wallet_transaction();

CREATE TABLE IF NOT EXISTS funding_operation (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
  request_id text NOT NULL UNIQUE,
  idempotency_key text NOT NULL,
  request_fingerprint text NOT NULL,
  pricing_version_id uuid REFERENCES model_price_version(id) ON DELETE RESTRICT,
  status text NOT NULL CHECK (status IN ('PENDING','RESERVED','SETTLED','PARTIALLY_SETTLED','RELEASED','FAILED','REVERSED')),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  maximum_amount numeric(30,12) NOT NULL CHECK (maximum_amount >= 0),
  promotion_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (promotion_amount >= 0),
  tax_rate numeric(12,8) NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
  exchange_rate numeric(30,12) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
  settled_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (settled_amount >= 0),
  released_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (released_amount >= 0),
  estimated_input_tokens bigint NOT NULL CHECK (estimated_input_tokens >= 0),
  max_output_tokens bigint NOT NULL CHECK (max_output_tokens >= 0),
  actual_input_tokens bigint CHECK (actual_input_tokens IS NULL OR actual_input_tokens >= 0),
  actual_cached_input_tokens bigint CHECK (actual_cached_input_tokens IS NULL OR actual_cached_input_tokens >= 0),
  actual_output_tokens bigint CHECK (actual_output_tokens IS NULL OR actual_output_tokens >= 0),
  usage_source text,
  observed_output_bytes bigint NOT NULL DEFAULT 0 CHECK (observed_output_bytes >= 0),
  failure_code text,
  reserved_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  released_at timestamptz,
  heartbeat_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,idempotency_key),
  UNIQUE(id,wallet_id)
);
CREATE INDEX IF NOT EXISTS funding_operation_wallet_created_idx ON funding_operation(wallet_id,created_at DESC);
CREATE INDEX IF NOT EXISTS funding_operation_recovery_idx ON funding_operation(status,heartbeat_at)
  WHERE status IN ('PENDING','RESERVED');

CREATE TABLE IF NOT EXISTS funding_operation_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  status text NOT NULL CHECK (status IN ('PENDING','RESERVED','SETTLED','PARTIALLY_SETTLED','RELEASED','FAILED','REVERSED')),
  amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (amount >= 0),
  source text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS funding_operation_event_operation_idx ON funding_operation_event(operation_id,created_at,id);

CREATE TABLE IF NOT EXISTS funding_provider_attempt (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  attempt_no integer NOT NULL CHECK (attempt_no > 0),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  credential_id uuid REFERENCES provider_credentials(id) ON DELETE SET NULL,
  credential_group_id uuid REFERENCES credential_groups(id) ON DELETE SET NULL,
  is_fallback boolean NOT NULL DEFAULT false,
  status text NOT NULL CHECK (status IN ('STARTED','SUCCEEDED','FAILED','CANCELLED','TIMED_OUT')),
  http_status integer,
  upstream_request_id text,
  error_code text,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  UNIQUE(operation_id,attempt_no)
);

CREATE TABLE IF NOT EXISTS funding_usage_adjustment (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  operation_id uuid NOT NULL REFERENCES funding_operation(id) ON DELETE RESTRICT,
  idempotency_key text NOT NULL,
  input_tokens bigint NOT NULL CHECK (input_tokens >= 0),
  cached_input_tokens bigint NOT NULL CHECK (cached_input_tokens >= 0),
  output_tokens bigint NOT NULL CHECK (output_tokens >= 0),
  previous_amount numeric(30,12) NOT NULL CHECK (previous_amount >= 0),
  corrected_amount numeric(30,12) NOT NULL CHECK (corrected_amount >= 0),
  difference_amount numeric(30,12) NOT NULL,
  usage_source text NOT NULL,
  journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(operation_id,idempotency_key)
);

CREATE OR REPLACE FUNCTION protect_terminal_funding_attempt()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR OLD.status<>'STARTED' THEN
    RAISE EXCEPTION 'terminal funding provider attempts are immutable' USING ERRCODE='55000';
  END IF;
  IF NEW.id<>OLD.id OR NEW.operation_id<>OLD.operation_id OR NEW.attempt_no<>OLD.attempt_no
     OR NEW.provider_id<>OLD.provider_id OR NEW.credential_id IS DISTINCT FROM OLD.credential_id
     OR NEW.credential_group_id IS DISTINCT FROM OLD.credential_group_id OR NEW.is_fallback<>OLD.is_fallback
     OR NEW.started_at<>OLD.started_at OR NEW.status='STARTED' OR NEW.finished_at IS NULL THEN
    RAISE EXCEPTION 'invalid funding provider attempt transition' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS funding_provider_attempt_protect_trigger ON funding_provider_attempt;
CREATE TRIGGER funding_provider_attempt_protect_trigger BEFORE UPDATE OR DELETE ON funding_provider_attempt
  FOR EACH ROW EXECUTE FUNCTION protect_terminal_funding_attempt();

ALTER TABLE request_logs
  ADD COLUMN IF NOT EXISTS funding_operation_id uuid REFERENCES funding_operation(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS usage_source text;
ALTER TABLE billing_usage_records
  ADD COLUMN IF NOT EXISTS funding_operation_id uuid REFERENCES funding_operation(id) ON DELETE RESTRICT;
ALTER TABLE wallet_transactions
  ADD COLUMN IF NOT EXISTS journal_id uuid REFERENCES ledger_journal(id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION reject_immutable_funding_evidence()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable',TG_TABLE_NAME USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS funding_operation_event_immutable_trigger ON funding_operation_event;
CREATE TRIGGER funding_operation_event_immutable_trigger BEFORE UPDATE OR DELETE ON funding_operation_event
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_funding_evidence();
DROP TRIGGER IF EXISTS funding_usage_adjustment_immutable_trigger ON funding_usage_adjustment;
CREATE TRIGGER funding_usage_adjustment_immutable_trigger BEFORE UPDATE OR DELETE ON funding_usage_adjustment
  FOR EACH ROW EXECUTE FUNCTION reject_immutable_funding_evidence();

-- Global counterpart accounts and per-wallet liability accounts.
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:cash:'||currency,'Platform cash','ASSET','DEBIT',currency FROM (SELECT DISTINCT currency FROM wallets) c
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:revenue:'||currency,'Usage revenue','REVENUE','CREDIT',currency FROM (SELECT DISTINCT currency FROM wallets) c
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:migration-equity:'||currency,'Migration opening equity','EQUITY','CREDIT',currency FROM (SELECT DISTINCT currency FROM wallets) c
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
SELECT 'system:adjustment-equity:'||currency,'Wallet adjustment equity','EQUITY','CREDIT',currency FROM (SELECT DISTINCT currency FROM wallets) c
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(wallet_id,account_key,name,account_type,normal_side,currency)
SELECT id,'wallet:'||id||':available','Wallet available balance','LIABILITY','CREDIT',currency FROM wallets
ON CONFLICT(account_key) DO NOTHING;
INSERT INTO ledger_account(wallet_id,account_key,name,account_type,normal_side,currency)
SELECT id,'wallet:'||id||':reserved','Wallet reserved balance','LIABILITY','CREDIT',currency FROM wallets
ON CONFLICT(account_key) DO NOTHING;

CREATE OR REPLACE FUNCTION create_wallet_ledger_accounts()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency)
  VALUES
    ('system:cash:'||NEW.currency,'Platform cash','ASSET','DEBIT',NEW.currency),
    ('system:revenue:'||NEW.currency,'Usage revenue','REVENUE','CREDIT',NEW.currency),
    ('system:migration-equity:'||NEW.currency,'Migration opening equity','EQUITY','CREDIT',NEW.currency),
    ('system:adjustment-equity:'||NEW.currency,'Wallet adjustment equity','EQUITY','CREDIT',NEW.currency)
  ON CONFLICT(account_key) DO NOTHING;
  INSERT INTO ledger_account(wallet_id,account_key,name,account_type,normal_side,currency)
  VALUES
    (NEW.id,'wallet:'||NEW.id||':available','Wallet available balance','LIABILITY','CREDIT',NEW.currency),
    (NEW.id,'wallet:'||NEW.id||':reserved','Wallet reserved balance','LIABILITY','CREDIT',NEW.currency)
  ON CONFLICT(account_key) DO NOTHING;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS wallets_create_ledger_accounts_trigger ON wallets;
CREATE TRIGGER wallets_create_ledger_accounts_trigger AFTER INSERT ON wallets
  FOR EACH ROW EXECUTE FUNCTION create_wallet_ledger_accounts();

-- Balance-preserving opening journals make every upgraded wallet replayable
-- without changing any legacy wallet value.
DO $$
DECLARE w record; j uuid; available_account uuid; reserved_account uuid; equity_account uuid;
BEGIN
  FOR w IN SELECT * FROM wallets WHERE available_balance<>0 OR reserved_balance<>0 LOOP
    INSERT INTO ledger_journal(wallet_id,journal_type,external_key,currency,metadata)
    VALUES(w.id,'OPENING','migration:0009:wallet:'||w.id,w.currency,'{"migration":"0009_funding_ledger"}'::jsonb)
    ON CONFLICT(external_key) DO NOTHING RETURNING id INTO j;
    IF j IS NULL THEN CONTINUE; END IF;
    SELECT id INTO available_account FROM ledger_account WHERE account_key='wallet:'||w.id||':available';
    SELECT id INTO reserved_account FROM ledger_account WHERE account_key='wallet:'||w.id||':reserved';
    SELECT id INTO equity_account FROM ledger_account WHERE account_key='system:migration-equity:'||w.currency;
    IF w.available_balance>0 THEN
      INSERT INTO ledger_journal_entry(journal_id,account_id,currency,entry_side,amount,description) VALUES
        (j,equity_account,w.currency,'DEBIT',w.available_balance,'Opening counterpart'),
        (j,available_account,w.currency,'CREDIT',w.available_balance,'Legacy available balance');
    ELSIF w.available_balance<0 THEN
      INSERT INTO ledger_journal_entry(journal_id,account_id,currency,entry_side,amount,description) VALUES
        (j,available_account,w.currency,'DEBIT',abs(w.available_balance),'Legacy negative available balance'),
        (j,equity_account,w.currency,'CREDIT',abs(w.available_balance),'Opening counterpart');
    END IF;
    IF w.reserved_balance>0 THEN
      INSERT INTO ledger_journal_entry(journal_id,account_id,currency,entry_side,amount,description) VALUES
        (j,equity_account,w.currency,'DEBIT',w.reserved_balance,'Opening counterpart'),
        (j,reserved_account,w.currency,'CREDIT',w.reserved_balance,'Legacy reserved balance');
    END IF;
    UPDATE ledger_journal SET status='POSTED',posted_at=now() WHERE id=j;
  END LOOP;
END;
$$;
