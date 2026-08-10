-- RelayDock V2 tenant, budget, key-version, and webhook data model.
--
-- The two fixed UUIDs below are deliberately stable.  A V1 installation did
-- not have tenant ownership, so all pre-V2 rows are assigned to one explicit
-- Legacy organization/project instead of being left ambiguously unscoped.

CREATE TABLE IF NOT EXISTS organizations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  slug text NOT NULL,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','ARCHIVED')),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS organizations_slug_unique_idx ON organizations(lower(slug));

CREATE TABLE IF NOT EXISTS projects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  name text NOT NULL,
  slug text NOT NULL,
  description text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','ARCHIVED')),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,slug),
  UNIQUE(id,organization_id)
);
CREATE INDEX IF NOT EXISTS projects_organization_idx ON projects(organization_id,created_at DESC);

CREATE TABLE IF NOT EXISTS organization_memberships (
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(organization_id,user_id)
);
CREATE INDEX IF NOT EXISTS organization_memberships_user_idx ON organization_memberships(user_id,organization_id);

CREATE TABLE IF NOT EXISTS project_memberships (
  organization_id uuid NOT NULL,
  project_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('ADMIN','DEVELOPER','VIEWER')),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(project_id,user_id),
  UNIQUE(project_id,organization_id,user_id),
  FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE CASCADE,
  FOREIGN KEY(organization_id,user_id) REFERENCES organization_memberships(organization_id,user_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS project_memberships_user_idx ON project_memberships(user_id,project_id);

INSERT INTO organizations(id,name,slug,status,metadata)
VALUES ('00000000-0000-4000-8000-000000000001','Legacy','legacy','ACTIVE','{"migration_source":"v1"}'::jsonb)
ON CONFLICT (id) DO NOTHING;

INSERT INTO projects(id,organization_id,name,slug,description,status,metadata)
VALUES (
  '00000000-0000-4000-8000-000000000002',
  '00000000-0000-4000-8000-000000000001',
  'Legacy','legacy','Resources migrated from RelayDock V1','ACTIVE',
  '{"migration_source":"v1"}'::jsonb
)
ON CONFLICT (id) DO NOTHING;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM organizations
    WHERE id='00000000-0000-4000-8000-000000000001' AND lower(slug)='legacy'
  ) THEN
    RAISE EXCEPTION 'the reserved Legacy organization UUID is already used by another resource';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM projects
    WHERE id='00000000-0000-4000-8000-000000000002'
      AND organization_id='00000000-0000-4000-8000-000000000001' AND slug='legacy'
  ) THEN
    RAISE EXCEPTION 'the reserved Legacy project UUID is already used by another resource';
  END IF;
END;
$$;

INSERT INTO organization_memberships(organization_id,user_id,role,status)
SELECT '00000000-0000-4000-8000-000000000001',u.id,
       CASE WHEN u.role='SUPER_ADMIN' THEN 'OWNER'
            WHEN u.role='ADMIN' THEN 'ADMIN' ELSE 'MEMBER' END,
       CASE WHEN u.status='ACTIVE' THEN 'ACTIVE' ELSE 'DISABLED' END
FROM users u
ON CONFLICT (organization_id,user_id) DO NOTHING;

INSERT INTO project_memberships(organization_id,project_id,user_id,role,status)
SELECT '00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002',u.id,
       CASE WHEN u.role IN ('SUPER_ADMIN','ADMIN') THEN 'ADMIN' ELSE 'DEVELOPER' END,
       CASE WHEN u.status='ACTIVE' THEN 'ACTIVE' ELSE 'DISABLED' END
FROM users u
ON CONFLICT (project_id,user_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS project_model_routes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  model_route_id uuid NOT NULL REFERENCES model_routes(id) ON DELETE RESTRICT,
  alias text NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  routing_config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(routing_config) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id,alias),
  UNIQUE(project_id,model_route_id)
);
CREATE INDEX IF NOT EXISTS project_model_routes_route_idx ON project_model_routes(model_route_id,project_id);

INSERT INTO project_model_routes(project_id,model_route_id,alias,enabled)
SELECT '00000000-0000-4000-8000-000000000002',r.id,r.alias,r.enabled
FROM model_routes r
ON CONFLICT (project_id,model_route_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS project_budget_policies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  name text NOT NULL,
  period text NOT NULL DEFAULT 'MONTHLY' CHECK (period IN ('DAILY','MONTHLY')),
  token_limit bigint CHECK (token_limit IS NULL OR token_limit >= 0),
  cost_limit numeric(20,8) CHECK (cost_limit IS NULL OR cost_limit >= 0),
  alert_threshold numeric(5,4) NOT NULL DEFAULT 0.8000 CHECK (alert_threshold >= 0 AND alert_threshold <= 1),
  enforce_hard_limit boolean NOT NULL DEFAULT true,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id,name),
  UNIQUE(id,project_id),
  CHECK (token_limit IS NOT NULL OR cost_limit IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS project_budget_policies_project_idx ON project_budget_policies(project_id,status);

CREATE TABLE IF NOT EXISTS budget_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL,
  policy_id uuid,
  user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  api_key_id uuid,
  request_id text,
  event_type text NOT NULL CHECK (event_type IN ('RESERVE','COMMIT','RELEASE','REJECT','ADJUSTMENT','THRESHOLD')),
  tokens bigint NOT NULL DEFAULT 0,
  cost numeric(20,8) NOT NULL DEFAULT 0,
  idempotency_key text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT,
  FOREIGN KEY(policy_id,project_id) REFERENCES project_budget_policies(id,project_id) ON DELETE RESTRICT,
  CHECK (api_key_id IS NULL OR user_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS budget_events_project_created_idx ON budget_events(project_id,created_at DESC);
CREATE INDEX IF NOT EXISTS budget_events_request_idx ON budget_events(request_id) WHERE request_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS budget_events_idempotency_idx
  ON budget_events(project_id,idempotency_key,event_type)
  WHERE idempotency_key IS NOT NULL;

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS project_id uuid;
UPDATE api_keys
SET organization_id=COALESCE(organization_id,'00000000-0000-4000-8000-000000000001'),
    project_id=COALESCE(project_id,'00000000-0000-4000-8000-000000000002');
ALTER TABLE api_keys ALTER COLUMN organization_id SET DEFAULT '00000000-0000-4000-8000-000000000001';
ALTER TABLE api_keys ALTER COLUMN project_id SET DEFAULT '00000000-0000-4000-8000-000000000002';
ALTER TABLE api_keys ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE api_keys ALTER COLUMN project_id SET NOT NULL;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_organization_fk') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_organization_fk
      FOREIGN KEY(organization_id) REFERENCES organizations(id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_project_organization_fk') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_project_organization_fk
      FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_scope_identity_uq') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_identity_uq UNIQUE(id,project_id,organization_id,user_id);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_project_membership_fk') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_project_membership_fk
      FOREIGN KEY(project_id,organization_id,user_id)
      REFERENCES project_memberships(project_id,organization_id,user_id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='budget_events_api_key_scope_fk') THEN
    ALTER TABLE budget_events ADD CONSTRAINT budget_events_api_key_scope_fk
      FOREIGN KEY(api_key_id,project_id,organization_id,user_id)
      REFERENCES api_keys(id,project_id,organization_id,user_id) ON DELETE RESTRICT;
  END IF;
END;
$$;
CREATE INDEX IF NOT EXISTS api_keys_project_idx ON api_keys(project_id,created_at DESC);

CREATE TABLE IF NOT EXISTS api_key_versions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
  version integer NOT NULL CHECK (version > 0),
  key_prefix text NOT NULL,
  key_hash bytea NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','GRACE','REVOKED')),
  valid_from timestamptz NOT NULL DEFAULT now(),
  grace_expires_at timestamptz,
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  UNIQUE(api_key_id,version),
  CHECK (status <> 'GRACE' OR grace_expires_at IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS api_key_versions_one_active_idx
  ON api_key_versions(api_key_id) WHERE status='ACTIVE';
CREATE INDEX IF NOT EXISTS api_key_versions_auth_idx
  ON api_key_versions(key_hash,status,grace_expires_at,expires_at);

INSERT INTO api_key_versions(api_key_id,version,key_prefix,key_hash,status,valid_from,expires_at,created_at,last_used_at)
SELECT k.id,1,k.key_prefix,k.key_hash,
       CASE WHEN k.status='ACTIVE' THEN 'ACTIVE' ELSE 'REVOKED' END,
       k.created_at,k.expires_at,k.created_at,k.last_used_at
FROM api_keys k
ON CONFLICT (api_key_id,version) DO NOTHING;

CREATE OR REPLACE FUNCTION seed_api_key_version()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO api_key_versions(api_key_id,version,key_prefix,key_hash,status,valid_from,expires_at,created_at)
  VALUES (NEW.id,1,NEW.key_prefix,NEW.key_hash,
          CASE WHEN NEW.status='ACTIVE' THEN 'ACTIVE' ELSE 'REVOKED' END,
          NEW.created_at,NEW.expires_at,NEW.created_at)
  ON CONFLICT (api_key_id,version) DO NOTHING;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgname='api_keys_seed_version_trigger'
      AND tgrelid='api_keys'::regclass AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER api_keys_seed_version_trigger
      AFTER INSERT ON api_keys FOR EACH ROW EXECUTE FUNCTION seed_api_key_version();
  END IF;
END;
$$;

ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS project_id uuid;
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS route_id uuid;
UPDATE request_logs l
SET organization_id=COALESCE(k.organization_id,'00000000-0000-4000-8000-000000000001'),
    project_id=COALESCE(k.project_id,'00000000-0000-4000-8000-000000000002')
FROM api_keys k
WHERE l.api_key_id=k.id;
UPDATE request_logs SET
  organization_id=COALESCE(organization_id,'00000000-0000-4000-8000-000000000001'),
  project_id=COALESCE(project_id,'00000000-0000-4000-8000-000000000002');
UPDATE request_logs l SET route_id=r.id
FROM model_routes r
WHERE l.route_id IS NULL AND r.alias=l.requested_model;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_logs_project_organization_fk') THEN
    ALTER TABLE request_logs ADD CONSTRAINT request_logs_project_organization_fk
      FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_logs_project_route_fk') THEN
    ALTER TABLE request_logs ADD CONSTRAINT request_logs_project_route_fk
      FOREIGN KEY(project_id,route_id) REFERENCES project_model_routes(project_id,model_route_id) ON DELETE RESTRICT;
  END IF;
END;
$$;
ALTER TABLE request_logs ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE request_logs ALTER COLUMN project_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS request_logs_project_created_idx ON request_logs(project_id,created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_org_created_idx ON request_logs(organization_id,created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_route_created_idx ON request_logs(route_id,created_at DESC);

ALTER TABLE usage_daily ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE usage_daily ADD COLUMN IF NOT EXISTS project_id uuid;
ALTER TABLE usage_daily ADD COLUMN IF NOT EXISTS route_id uuid;
UPDATE usage_daily u
SET organization_id=k.organization_id,project_id=k.project_id
FROM api_keys k WHERE u.api_key_id=k.id;
UPDATE usage_daily SET
  organization_id=COALESCE(organization_id,'00000000-0000-4000-8000-000000000001'),
  project_id=COALESCE(project_id,'00000000-0000-4000-8000-000000000002');
UPDATE usage_daily u SET route_id=r.id
FROM model_routes r
WHERE u.route_id IS NULL AND r.alias=u.model;
ALTER TABLE usage_daily ALTER COLUMN organization_id SET DEFAULT '00000000-0000-4000-8000-000000000001';
ALTER TABLE usage_daily ALTER COLUMN project_id SET DEFAULT '00000000-0000-4000-8000-000000000002';
ALTER TABLE usage_daily ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE usage_daily ALTER COLUMN project_id SET NOT NULL;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_project_organization_fk') THEN
    ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_project_organization_fk
      FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_project_route_fk') THEN
    ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_project_route_fk
      FOREIGN KEY(project_id,route_id) REFERENCES project_model_routes(project_id,model_route_id) ON DELETE RESTRICT;
  END IF;
END;
$$;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_v1_identity_uq') THEN
    ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_v1_identity_uq UNIQUE(date,user_id,api_key_id,model);
  END IF;
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_pkey' AND contype='p') THEN
    ALTER TABLE usage_daily DROP CONSTRAINT usage_daily_pkey;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_scope_pkey') THEN
    ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_scope_pkey
      PRIMARY KEY(organization_id,project_id,date,user_id,api_key_id,model);
  END IF;
END;
$$;
ALTER TABLE usage_daily DROP CONSTRAINT IF EXISTS usage_daily_user_id_fkey;
ALTER TABLE usage_daily DROP CONSTRAINT IF EXISTS usage_daily_api_key_id_fkey;
ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_api_key_id_fkey FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS usage_daily_project_date_idx ON usage_daily(project_id,date DESC);
CREATE INDEX IF NOT EXISTS usage_daily_org_date_idx ON usage_daily(organization_id,date DESC);

ALTER TABLE usage_hourly ADD COLUMN IF NOT EXISTS organization_id uuid;
ALTER TABLE usage_hourly ADD COLUMN IF NOT EXISTS project_id uuid;
ALTER TABLE usage_hourly ADD COLUMN IF NOT EXISTS route_id uuid;
UPDATE usage_hourly u
SET organization_id=k.organization_id,project_id=k.project_id
FROM api_keys k WHERE u.api_key_id=k.id;
UPDATE usage_hourly SET
  organization_id=COALESCE(organization_id,'00000000-0000-4000-8000-000000000001'),
  project_id=COALESCE(project_id,'00000000-0000-4000-8000-000000000002');
UPDATE usage_hourly u SET route_id=r.id
FROM model_routes r
WHERE u.route_id IS NULL AND r.alias=u.model;
ALTER TABLE usage_hourly ALTER COLUMN organization_id SET DEFAULT '00000000-0000-4000-8000-000000000001';
ALTER TABLE usage_hourly ALTER COLUMN project_id SET DEFAULT '00000000-0000-4000-8000-000000000002';
ALTER TABLE usage_hourly ALTER COLUMN organization_id SET NOT NULL;
ALTER TABLE usage_hourly ALTER COLUMN project_id SET NOT NULL;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_project_organization_fk') THEN
    ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_project_organization_fk
      FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_project_route_fk') THEN
    ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_project_route_fk
      FOREIGN KEY(project_id,route_id) REFERENCES project_model_routes(project_id,model_route_id) ON DELETE RESTRICT;
  END IF;
END;
$$;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_v1_identity_uq') THEN
    ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_v1_identity_uq UNIQUE(hour,user_id,api_key_id,model);
  END IF;
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_pkey' AND contype='p') THEN
    ALTER TABLE usage_hourly DROP CONSTRAINT usage_hourly_pkey;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_scope_pkey') THEN
    ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_scope_pkey
      PRIMARY KEY(organization_id,project_id,hour,user_id,api_key_id,model);
  END IF;
END;
$$;
ALTER TABLE usage_hourly DROP CONSTRAINT IF EXISTS usage_hourly_user_id_fkey;
ALTER TABLE usage_hourly DROP CONSTRAINT IF EXISTS usage_hourly_api_key_id_fkey;
ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_api_key_id_fkey FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE RESTRICT;
CREATE INDEX IF NOT EXISTS usage_hourly_project_hour_idx ON usage_hourly(project_id,hour DESC);
CREATE INDEX IF NOT EXISTS usage_hourly_org_hour_idx ON usage_hourly(organization_id,hour DESC);

CREATE OR REPLACE FUNCTION set_request_log_scope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  key_org uuid;
  key_project uuid;
BEGIN
  IF NEW.api_key_id IS NOT NULL THEN
    SELECT organization_id,project_id INTO key_org,key_project FROM api_keys WHERE id=NEW.api_key_id;
    IF key_org IS NOT NULL THEN NEW.organization_id=key_org; END IF;
    IF key_project IS NOT NULL THEN NEW.project_id=key_project; END IF;
  END IF;
  IF NEW.organization_id IS NULL THEN NEW.organization_id='00000000-0000-4000-8000-000000000001'; END IF;
  IF NEW.project_id IS NULL THEN NEW.project_id='00000000-0000-4000-8000-000000000002'; END IF;
  IF NEW.route_id IS NULL THEN
    SELECT pmr.model_route_id INTO NEW.route_id
    FROM project_model_routes pmr JOIN model_routes r ON r.id=pmr.model_route_id
    WHERE pmr.project_id=NEW.project_id AND pmr.enabled
      AND (pmr.alias=NEW.requested_model OR r.alias=NEW.requested_model)
    ORDER BY pmr.created_at LIMIT 1;
  END IF;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger WHERE tgname='request_logs_scope_trigger'
      AND tgrelid='request_logs'::regclass AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER request_logs_scope_trigger BEFORE INSERT ON request_logs
      FOR EACH ROW EXECUTE FUNCTION set_request_log_scope();
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION set_usage_scope()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  key_org uuid;
  key_project uuid;
BEGIN
  SELECT organization_id,project_id INTO key_org,key_project FROM api_keys WHERE id=NEW.api_key_id;
  IF key_org IS NOT NULL THEN NEW.organization_id=key_org; END IF;
  IF key_project IS NOT NULL THEN NEW.project_id=key_project; END IF;
  IF NEW.organization_id IS NULL THEN NEW.organization_id='00000000-0000-4000-8000-000000000001'; END IF;
  IF NEW.project_id IS NULL THEN NEW.project_id='00000000-0000-4000-8000-000000000002'; END IF;
  IF NEW.route_id IS NULL THEN
    SELECT pmr.model_route_id INTO NEW.route_id
    FROM project_model_routes pmr JOIN model_routes r ON r.id=pmr.model_route_id
    WHERE pmr.project_id=NEW.project_id AND pmr.enabled
      AND (pmr.alias=NEW.model OR r.alias=NEW.model)
    ORDER BY pmr.created_at LIMIT 1;
  END IF;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger WHERE tgname='usage_daily_scope_trigger'
      AND tgrelid='usage_daily'::regclass AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER usage_daily_scope_trigger BEFORE INSERT ON usage_daily
      FOR EACH ROW EXECUTE FUNCTION set_usage_scope();
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger WHERE tgname='usage_hourly_scope_trigger'
      AND tgrelid='usage_hourly'::regclass AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER usage_hourly_scope_trigger BEFORE INSERT ON usage_hourly
      FOR EACH ROW EXECUTE FUNCTION set_usage_scope();
  END IF;
END;
$$;

CREATE TABLE IF NOT EXISTS webhook_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL,
  name text NOT NULL,
  url text NOT NULL,
  encrypted_secret bytea NOT NULL,
  secret_last4 text NOT NULL,
  event_types jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(event_types)='array'),
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  last_delivery_at timestamptz,
  UNIQUE(project_id,name),
  UNIQUE(id,project_id,organization_id),
  CONSTRAINT webhook_endpoints_project_organization_fk
    FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS webhook_endpoints_project_idx ON webhook_endpoints(project_id,enabled);

CREATE TABLE IF NOT EXISTS webhook_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  endpoint_id uuid NOT NULL,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  event_id text NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PROCESSING','RETRY','DELIVERED','DEAD')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts integer NOT NULL DEFAULT 6 CHECK (max_attempts > 0),
  available_at timestamptz NOT NULL DEFAULT now(),
  locked_at timestamptz,
  locked_until timestamptz,
  locked_by text,
  claim_token uuid,
  delivered_at timestamptz,
  last_http_status integer,
  last_response text,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(endpoint_id,event_id),
  FOREIGN KEY(endpoint_id,project_id,organization_id)
    REFERENCES webhook_endpoints(id,project_id,organization_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS webhook_outbox_claim_idx
  ON webhook_outbox(status,available_at,created_at)
  WHERE status IN ('PENDING','RETRY','PROCESSING');
CREATE INDEX IF NOT EXISTS webhook_outbox_processing_lease_idx
  ON webhook_outbox(locked_until) WHERE status='PROCESSING';
CREATE INDEX IF NOT EXISTS webhook_outbox_project_idx ON webhook_outbox(project_id,created_at DESC);

ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS alerts_acknowledged_idx ON alerts(acknowledged_at,created_at DESC);
