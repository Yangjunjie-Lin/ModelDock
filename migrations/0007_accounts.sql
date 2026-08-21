-- ModelDock SaaS account lifecycle, organization invitations, MFA, and mail outbox.
-- This migration is intentionally forward-only. See docs/account-lifecycle.md
-- for the operational rollback procedure.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
UPDATE users SET status='SUSPENDED' WHERE status='DISABLED';
ALTER TABLE users
  ADD CONSTRAINT users_status_check
  CHECK (status IN ('PENDING_VERIFICATION','ACTIVE','SUSPENDED','CLOSED'));

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS email_verified_at timestamptz,
  ADD COLUMN IF NOT EXISTS session_version bigint NOT NULL DEFAULT 0 CHECK (session_version >= 0),
  ADD COLUMN IF NOT EXISTS totp_secret_encrypted bytea,
  ADD COLUMN IF NOT EXISTS totp_pending_secret_encrypted bytea,
  ADD COLUMN IF NOT EXISTS totp_pending_expires_at timestamptz,
  ADD COLUMN IF NOT EXISTS totp_enrolled_at timestamptz,
  ADD COLUMN IF NOT EXISTS totp_last_used_step bigint;

UPDATE users
SET email_verified_at=COALESCE(email_verified_at,created_at)
WHERE status IN ('ACTIVE','SUSPENDED','CLOSED');

CREATE TABLE IF NOT EXISTS account_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose text NOT NULL CHECK (purpose IN ('EMAIL_VERIFICATION','PASSWORD_RESET')),
  token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest)=32),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS account_tokens_user_purpose_idx
  ON account_tokens(user_id,purpose,created_at DESC);
CREATE INDEX IF NOT EXISTS account_tokens_pending_idx
  ON account_tokens(expires_at) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS registration_invites (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code_digest bytea NOT NULL UNIQUE CHECK (octet_length(code_digest)=32),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','REVOKED','EXHAUSTED')),
  max_uses integer NOT NULL DEFAULT 1 CHECK (max_uses > 0 AND max_uses <= 10000),
  used_count integer NOT NULL DEFAULT 0 CHECK (used_count >= 0 AND used_count <= max_uses),
  expires_at timestamptz NOT NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);
CREATE INDEX IF NOT EXISTS registration_invites_active_idx
  ON registration_invites(expires_at) WHERE status='ACTIVE';

CREATE TABLE IF NOT EXISTS organization_invitations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  email text NOT NULL,
  role text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','ACCEPTED','REJECTED','REVOKED','EXPIRED')),
  token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest)=32),
  expires_at timestamptz NOT NULL,
  invited_by uuid REFERENCES users(id) ON DELETE SET NULL,
  accepted_by uuid REFERENCES users(id) ON DELETE SET NULL,
  responded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS organization_invitations_pending_email_idx
  ON organization_invitations(organization_id,lower(email)) WHERE status='PENDING';
CREATE INDEX IF NOT EXISTS organization_invitations_recipient_idx
  ON organization_invitations(lower(email),created_at DESC);

CREATE TABLE IF NOT EXISTS email_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recipient text NOT NULL,
  template text NOT NULL CHECK (template IN ('VERIFY_EMAIL','PASSWORD_RESET','ORGANIZATION_INVITE')),
  encrypted_message bytea NOT NULL,
  dedupe_key text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PROCESSING','RETRY','DELIVERED','DEAD')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts integer NOT NULL DEFAULT 6 CHECK (max_attempts > 0),
  available_at timestamptz NOT NULL DEFAULT now(),
  locked_at timestamptz,
  locked_until timestamptz,
  locked_by text,
  claim_token uuid,
  delivered_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (attempts <= max_attempts)
);
CREATE INDEX IF NOT EXISTS email_outbox_ready_idx
  ON email_outbox(status,available_at,created_at)
  WHERE status IN ('PENDING','RETRY','PROCESSING');

