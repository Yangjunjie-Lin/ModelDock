CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), email text NOT NULL UNIQUE,
  password_hash text NOT NULL, display_name text NOT NULL DEFAULT '',
  role text NOT NULL CHECK (role IN ('SUPER_ADMIN','ADMIN','USER')),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  monthly_token_limit bigint, monthly_cost_limit numeric(20,8),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), last_login_at timestamptz
);

CREATE TABLE IF NOT EXISTS providers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, slug text NOT NULL UNIQUE,
  provider_type text NOT NULL, base_url text NOT NULL, enabled boolean NOT NULL DEFAULT true,
  config jsonb NOT NULL DEFAULT '{}'::jsonb, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO providers (name,slug,provider_type,base_url) VALUES ('OpenAI','openai','openai','https://api.openai.com/v1') ON CONFLICT (slug) DO NOTHING;

CREATE TABLE IF NOT EXISTS provider_credentials (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  name text NOT NULL, credential_type text NOT NULL DEFAULT 'api_key' CHECK (credential_type IN ('api_key','service_account','workload_identity')),
  encrypted_secret bytea NOT NULL, secret_last4 text NOT NULL, organization_id text, project_id text,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED','RATE_LIMITED','COOLDOWN','AUTH_FAILED','UNHEALTHY','UNKNOWN')),
  priority integer NOT NULL DEFAULT 0, weight integer NOT NULL DEFAULT 100 CHECK (weight > 0),
  max_concurrency integer NOT NULL DEFAULT 10 CHECK (max_concurrency > 0), current_health text NOT NULL DEFAULT 'UNKNOWN',
  last_success_at timestamptz, last_failure_at timestamptz, cooldown_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS provider_credentials_status_idx ON provider_credentials(status);

CREATE TABLE IF NOT EXISTS credential_groups (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  name text NOT NULL, description text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(provider_id,name)
);
CREATE TABLE IF NOT EXISTS credential_group_members (
  group_id uuid NOT NULL REFERENCES credential_groups(id) ON DELETE CASCADE,
  credential_id uuid NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
  weight integer NOT NULL DEFAULT 100 CHECK (weight > 0), priority integer NOT NULL DEFAULT 0,
  PRIMARY KEY(group_id,credential_id)
);
CREATE TABLE IF NOT EXISTS credential_tags (
  credential_id uuid NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE, tag text NOT NULL,
  PRIMARY KEY(credential_id,tag)
);

CREATE TABLE IF NOT EXISTS models (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  provider_model_id text NOT NULL, display_name text NOT NULL, model_type text NOT NULL DEFAULT 'text', enabled boolean NOT NULL DEFAULT true,
  capabilities jsonb NOT NULL DEFAULT '[]'::jsonb, capability_source text NOT NULL DEFAULT 'provider', context_window integer,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider_id,provider_model_id)
);
CREATE TABLE IF NOT EXISTS model_routes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), alias text NOT NULL UNIQUE,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE, upstream_model text NOT NULL,
  credential_group_id uuid NOT NULL REFERENCES credential_groups(id) ON DELETE RESTRICT,
  fallback_group_id uuid REFERENCES credential_groups(id) ON DELETE SET NULL, enabled boolean NOT NULL DEFAULT true,
  routing_policy text NOT NULL DEFAULT 'priority_weighted', fallback_config jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS model_prices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), model_id uuid NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  version integer NOT NULL, effective_from timestamptz NOT NULL, input_price numeric(20,10) NOT NULL DEFAULT 0,
  cached_input_price numeric(20,10) NOT NULL DEFAULT 0, output_price numeric(20,10) NOT NULL DEFAULT 0,
  currency text NOT NULL DEFAULT 'USD', unit integer NOT NULL DEFAULT 1000000, source text NOT NULL DEFAULT 'manual',
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(model_id,version)
);

CREATE TABLE IF NOT EXISTS api_keys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL, environment text NOT NULL DEFAULT 'live' CHECK (environment IN ('live','test')),
  key_prefix text NOT NULL, key_hash bytea NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED','REVOKED')),
  expires_at timestamptz, rate_limit_rpm integer NOT NULL DEFAULT 60, rate_limit_tpm integer NOT NULL DEFAULT 100000,
  monthly_token_limit bigint, monthly_cost_limit numeric(20,8), allowed_models jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), last_used_at timestamptz
);
CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys(user_id);

CREATE TABLE IF NOT EXISTS request_logs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), request_id text NOT NULL UNIQUE,
  user_id uuid REFERENCES users(id) ON DELETE SET NULL, api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
  provider_id uuid REFERENCES providers(id) ON DELETE SET NULL, credential_id uuid REFERENCES provider_credentials(id) ON DELETE SET NULL,
  requested_model text, resolved_model text, endpoint text NOT NULL, status_code integer NOT NULL,
  streaming boolean NOT NULL DEFAULT false, input_tokens bigint NOT NULL DEFAULT 0, cached_input_tokens bigint NOT NULL DEFAULT 0,
  output_tokens bigint NOT NULL DEFAULT 0, total_tokens bigint NOT NULL DEFAULT 0,
  estimated_cost numeric(20,8) NOT NULL DEFAULT 0, latency_ms bigint NOT NULL DEFAULT 0, ttft_ms bigint,
  upstream_request_id text, error_code text, scheduler_reason jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS request_logs_created_idx ON request_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_user_idx ON request_logs(user_id,created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_api_key_idx ON request_logs(api_key_id,created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_model_idx ON request_logs(requested_model,created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_credential_idx ON request_logs(credential_id,created_at DESC);

CREATE TABLE IF NOT EXISTS usage_daily (
  date date NOT NULL, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, model text NOT NULL,
  requests bigint NOT NULL DEFAULT 0, input_tokens bigint NOT NULL DEFAULT 0, cached_input_tokens bigint NOT NULL DEFAULT 0,
  output_tokens bigint NOT NULL DEFAULT 0, cost numeric(20,8) NOT NULL DEFAULT 0, errors bigint NOT NULL DEFAULT 0,
  PRIMARY KEY(date,user_id,api_key_id,model)
);
CREATE INDEX IF NOT EXISTS usage_daily_date_idx ON usage_daily(date DESC);
CREATE TABLE IF NOT EXISTS usage_hourly (
  hour timestamptz NOT NULL, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, model text NOT NULL,
  requests bigint NOT NULL DEFAULT 0, input_tokens bigint NOT NULL DEFAULT 0, cached_input_tokens bigint NOT NULL DEFAULT 0,
  output_tokens bigint NOT NULL DEFAULT 0, cost numeric(20,8) NOT NULL DEFAULT 0, errors bigint NOT NULL DEFAULT 0,
  PRIMARY KEY(hour,user_id,api_key_id,model)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  action text NOT NULL, resource_type text NOT NULL, resource_id text, before_state jsonb, after_state jsonb,
  ip inet, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_created_idx ON audit_logs(created_at DESC);
CREATE TABLE IF NOT EXISTS alerts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), kind text NOT NULL, severity text NOT NULL, message text NOT NULL,
  resource_type text, resource_id text, acknowledged_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS system_settings (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Provider ownership is part of the identity of credential groups. These
-- triggers keep the invariant intact even for writes that bypass RelayDock's
-- store package (for example, an operator running SQL directly).
CREATE OR REPLACE FUNCTION enforce_credential_group_member_provider_match()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  group_provider_id uuid;
  credential_provider_id uuid;
BEGIN
  SELECT provider_id INTO group_provider_id FROM credential_groups WHERE id = NEW.group_id;
  SELECT provider_id INTO credential_provider_id FROM provider_credentials WHERE id = NEW.credential_id;

  IF group_provider_id IS NOT NULL
     AND credential_provider_id IS NOT NULL
     AND group_provider_id <> credential_provider_id THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      CONSTRAINT = 'credential_group_member_provider_match',
      MESSAGE = 'credential and credential group must belong to the same provider';
  END IF;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgname = 'credential_group_member_provider_match_trigger'
      AND tgrelid = 'credential_group_members'::regclass
      AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER credential_group_member_provider_match_trigger
      BEFORE INSERT OR UPDATE OF group_id, credential_id ON credential_group_members
      FOR EACH ROW EXECUTE FUNCTION enforce_credential_group_member_provider_match();
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_model_route_provider_match()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  primary_provider_id uuid;
  fallback_provider_id uuid;
BEGIN
  SELECT provider_id INTO primary_provider_id FROM credential_groups WHERE id = NEW.credential_group_id;
  IF primary_provider_id IS NOT NULL AND primary_provider_id <> NEW.provider_id THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      CONSTRAINT = 'model_route_primary_group_provider_match',
      MESSAGE = 'model route and primary credential group must belong to the same provider';
  END IF;

  IF NEW.fallback_group_id IS NOT NULL THEN
    SELECT provider_id INTO fallback_provider_id FROM credential_groups WHERE id = NEW.fallback_group_id;
    IF fallback_provider_id IS NOT NULL AND fallback_provider_id <> NEW.provider_id THEN
      RAISE EXCEPTION USING
        ERRCODE = '23514',
        CONSTRAINT = 'model_route_fallback_group_provider_match',
        MESSAGE = 'model route and fallback credential group must belong to the same provider';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgname = 'model_route_provider_match_trigger'
      AND tgrelid = 'model_routes'::regclass
      AND NOT tgisinternal
  ) THEN
    CREATE TRIGGER model_route_provider_match_trigger
      BEFORE INSERT OR UPDATE OF provider_id, credential_group_id, fallback_group_id ON model_routes
      FOR EACH ROW EXECUTE FUNCTION enforce_model_route_provider_match();
  END IF;
END;
$$;

-- Refuse to declare a legacy database migrated while inconsistent rows still
-- exist. RelayDock does not silently delete or rewrite operator-owned data.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM credential_group_members member
    JOIN credential_groups credential_group ON credential_group.id = member.group_id
    JOIN provider_credentials credential ON credential.id = member.credential_id
    WHERE credential_group.provider_id <> credential.provider_id
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      CONSTRAINT = 'credential_group_member_provider_match',
      MESSAGE = 'existing credential group membership crosses provider boundaries';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM model_routes route
    JOIN credential_groups credential_group ON credential_group.id = route.credential_group_id
    WHERE route.provider_id <> credential_group.provider_id
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      CONSTRAINT = 'model_route_primary_group_provider_match',
      MESSAGE = 'existing model route primary group crosses provider boundaries';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM model_routes route
    JOIN credential_groups credential_group ON credential_group.id = route.fallback_group_id
    WHERE route.provider_id <> credential_group.provider_id
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      CONSTRAINT = 'model_route_fallback_group_provider_match',
      MESSAGE = 'existing model route fallback group crosses provider boundaries';
  END IF;
END;
$$;
