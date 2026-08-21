-- Public Beta runtime hardening. This migration is additive and forward-only.
-- Existing audit rows are preserved; newly inserted rows form an immutable
-- hash chain so deletion or mutation is detectable and rejected at the
-- database boundary.

ALTER TABLE audit_logs
  ADD COLUMN IF NOT EXISTS previous_hash bytea,
  ADD COLUMN IF NOT EXISTS entry_hash bytea,
  ADD COLUMN IF NOT EXISTS hash_version smallint;

CREATE UNIQUE INDEX IF NOT EXISTS audit_logs_entry_hash_idx
  ON audit_logs(entry_hash) WHERE entry_hash IS NOT NULL;

CREATE TABLE IF NOT EXISTS audit_log_chain_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  tail_hash bytea,
  sealed_entries bigint NOT NULL DEFAULT 0 CHECK (sealed_entries >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO audit_log_chain_state(singleton) VALUES(true) ON CONFLICT(singleton) DO NOTHING;

CREATE OR REPLACE FUNCTION seal_audit_log_entry()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  prior bytea;
  canonical text;
BEGIN
  -- Lock one tail row so concurrent replicas cannot choose the same
  -- predecessor or fork the chain.
  SELECT tail_hash INTO prior FROM audit_log_chain_state
    WHERE singleton=true FOR UPDATE;

  NEW.previous_hash := COALESCE(prior, ''::bytea);
  NEW.hash_version := 1;
  canonical := concat_ws('|',
    encode(NEW.previous_hash,'hex'), NEW.id::text,
    COALESCE(NEW.actor_id::text,''), NEW.action, NEW.resource_type,
    COALESCE(NEW.resource_id,''), COALESCE(NEW.before_state::text,'null'),
    COALESCE(NEW.after_state::text,'null'), COALESCE(host(NEW.ip),''),
    extract(epoch FROM NEW.created_at)::text
  );
  NEW.entry_hash := digest(convert_to(canonical,'UTF8'),'sha256');
  UPDATE audit_log_chain_state
    SET tail_hash=NEW.entry_hash,sealed_entries=sealed_entries+1,updated_at=now()
    WHERE singleton=true;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS audit_logs_seal_trigger ON audit_logs;
CREATE TRIGGER audit_logs_seal_trigger
  BEFORE INSERT ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION seal_audit_log_entry();

CREATE OR REPLACE FUNCTION reject_audit_log_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit log entries are immutable' USING ERRCODE='55000';
END;
$$;

DROP TRIGGER IF EXISTS audit_logs_immutable_trigger ON audit_logs;
CREATE TRIGGER audit_logs_immutable_trigger
  BEFORE UPDATE OR DELETE ON audit_logs
  FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation();

COMMENT ON COLUMN audit_logs.entry_hash IS
  'SHA-256 tamper-evidence hash for audit rows inserted after migration 0018.';
