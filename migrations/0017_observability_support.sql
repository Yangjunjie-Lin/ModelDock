-- Observability, public status history, and support workflow.  This migration
-- is additive: existing request, audit, alert, payment, and ledger rows remain
-- authoritative and no credential or prompt content is copied.

ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS trace_id text
  CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$');
CREATE INDEX IF NOT EXISTS request_logs_trace_idx ON request_logs(trace_id, created_at DESC)
  WHERE trace_id IS NOT NULL;

ALTER TABLE alerts ADD COLUMN IF NOT EXISTS dedupe_key text;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS details jsonb NOT NULL DEFAULT '{}'::jsonb
  CHECK (jsonb_typeof(details)='object');
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_at timestamptz;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
CREATE UNIQUE INDEX IF NOT EXISTS alerts_open_dedupe_idx
  ON alerts(dedupe_key) WHERE dedupe_key IS NOT NULL AND resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS alerts_open_created_idx ON alerts(created_at DESC)
  WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS status_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  component text NOT NULL CHECK (component IN ('GATEWAY','DASHBOARD','BILLING','PROVIDER','DATABASE','REDIS','PAYMENTS','LEDGER')),
  status text NOT NULL CHECK (status IN ('OPERATIONAL','DEGRADED','PARTIAL_OUTAGE','MAJOR_OUTAGE','MAINTENANCE')),
  summary text NOT NULL,
  public_message text NOT NULL,
  dedupe_key text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  started_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS status_events_component_started_idx ON status_events(component, started_at DESC);
CREATE INDEX IF NOT EXISTS status_events_open_idx ON status_events(resolved_at, started_at DESC);

CREATE TABLE IF NOT EXISTS observability_slos (
  name text PRIMARY KEY,
  target_percent numeric(7,4) NOT NULL CHECK (target_percent > 0 AND target_percent <= 100),
  window_minutes integer NOT NULL CHECK (window_minutes > 0),
  description text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO observability_slos(name,target_percent,window_minutes,description) VALUES
  ('gateway_availability',99.90,30,'Gateway responses excluding client errors remain available.'),
  ('control_plane_availability',99.90,30,'Control-plane requests excluding client errors remain available.'),
  ('payment_webhook_processing',99.50,60,'Verified payment webhooks are durably processed.'),
  ('ledger_settlement_latency',99.00,60,'Funding reservations settle within the configured processing window.'),
  ('provider_routing_success',99.00,30,'Eligible provider routes return a response or a documented fallback.')
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS support_tickets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_number text NOT NULL UNIQUE,
  subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 200),
  status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','IN_PROGRESS','WAITING_FOR_USER','RESOLVED','CLOSED')),
  priority text NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW','NORMAL','HIGH','URGENT')),
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  request_id text,
  order_id uuid REFERENCES recharge_order(id) ON DELETE SET NULL,
  ledger_journal_id uuid REFERENCES ledger_journal(id) ON DELETE SET NULL,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  assigned_to uuid REFERENCES users(id) ON DELETE SET NULL,
  redacted_context jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(redacted_context)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);
CREATE INDEX IF NOT EXISTS support_tickets_user_idx ON support_tickets(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS support_tickets_org_idx ON support_tickets(organization_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS support_tickets_request_idx ON support_tickets(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS support_tickets_status_idx ON support_tickets(status, priority, updated_at DESC);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_id uuid NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
  author_id uuid REFERENCES users(id) ON DELETE SET NULL,
  visibility text NOT NULL DEFAULT 'PUBLIC' CHECK (visibility IN ('PUBLIC','INTERNAL')),
  body text NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS support_ticket_messages_ticket_idx ON support_ticket_messages(ticket_id, created_at, id);
