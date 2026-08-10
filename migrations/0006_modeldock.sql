-- ModelDock V3 is append-only. Existing RelayDock aliases, credentials,
-- project grants, usage aggregates, and request logs remain authoritative.

ALTER TABLE models ADD COLUMN IF NOT EXISTS latency_score numeric(5,2) NOT NULL DEFAULT 50;
ALTER TABLE models ADD COLUMN IF NOT EXISTS quality_score numeric(5,2) NOT NULL DEFAULT 50;
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS reference_cost numeric(20,8) NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS savings_amount numeric(20,8) NOT NULL DEFAULT 0;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='models_latency_score_check') THEN
    ALTER TABLE models ADD CONSTRAINT models_latency_score_check CHECK (latency_score BETWEEN 0 AND 100);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='models_quality_score_check') THEN
    ALTER TABLE models ADD CONSTRAINT models_quality_score_check CHECK (quality_score BETWEEN 0 AND 100);
  END IF;
END;
$$;

CREATE TABLE IF NOT EXISTS routing_rules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL,
  name text NOT NULL,
  alias text NOT NULL,
  strategy text NOT NULL DEFAULT 'balanced'
    CHECK (strategy IN ('cost_optimized','quality_optimized','balanced')),
  quality_weight numeric(8,4) NOT NULL DEFAULT 0.50 CHECK (quality_weight >= 0),
  price_weight numeric(8,4) NOT NULL DEFAULT 0.30 CHECK (price_weight >= 0),
  latency_weight numeric(8,4) NOT NULL DEFAULT 0.20 CHECK (latency_weight >= 0),
  enabled boolean NOT NULL DEFAULT true,
  config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id,alias),
  UNIQUE(id,project_id,organization_id),
  CONSTRAINT routing_rules_project_organization_fk
    FOREIGN KEY(project_id,organization_id) REFERENCES projects(id,organization_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS routing_rules_project_enabled_idx ON routing_rules(project_id,enabled,alias);

CREATE TABLE IF NOT EXISTS provider_marketplace_listings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  endpoint text NOT NULL,
  supported_models jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(supported_models)='array'),
  price jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(price)='object'),
  status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','REVIEW','ACTIVE','SUSPENDED','REJECTED')),
  uptime numeric(7,4) NOT NULL DEFAULT 0 CHECK (uptime BETWEEN 0 AND 100),
  verified boolean NOT NULL DEFAULT false,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider_id,endpoint)
);
CREATE INDEX IF NOT EXISTS provider_marketplace_status_idx ON provider_marketplace_listings(status,uptime DESC);

CREATE TABLE IF NOT EXISTS teams (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  name text NOT NULL,
  slug text NOT NULL,
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED','ARCHIVED')),
  monthly_token_limit bigint CHECK (monthly_token_limit IS NULL OR monthly_token_limit >= 0),
  monthly_cost_limit numeric(20,8) CHECK (monthly_cost_limit IS NULL OR monthly_cost_limit >= 0),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(organization_id,slug),
  UNIQUE(id,organization_id)
);

CREATE TABLE IF NOT EXISTS team_memberships (
  team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  organization_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  role text NOT NULL DEFAULT 'MEMBER' CHECK (role IN ('OWNER','ADMIN','MEMBER','VIEWER')),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(team_id,user_id),
  FOREIGN KEY(team_id,organization_id) REFERENCES teams(id,organization_id) ON DELETE CASCADE,
  FOREIGN KEY(organization_id,user_id) REFERENCES organization_memberships(organization_id,user_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS team_memberships_user_idx ON team_memberships(user_id,status);

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS team_id uuid;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_team_organization_fk') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_team_organization_fk
      FOREIGN KEY(team_id,organization_id) REFERENCES teams(id,organization_id) ON DELETE RESTRICT;
  END IF;
END;
$$;
CREATE INDEX IF NOT EXISTS api_keys_team_idx ON api_keys(team_id,status) WHERE team_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS wallets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE RESTRICT,
  currency text NOT NULL DEFAULT 'USD',
  billing_mode text NOT NULL DEFAULT 'POSTPAID' CHECK (billing_mode IN ('PREPAID','POSTPAID')),
  available_balance numeric(20,8) NOT NULL DEFAULT 0,
  reserved_balance numeric(20,8) NOT NULL DEFAULT 0 CHECK (reserved_balance >= 0),
  credit_limit numeric(20,8) NOT NULL DEFAULT 0 CHECK (credit_limit >= 0),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','FROZEN','CLOSED')),
  version bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (reserved_balance >= 0)
);

CREATE TABLE IF NOT EXISTS billing_usage_records (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id text NOT NULL UNIQUE,
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
  provider_id uuid REFERENCES providers(id) ON DELETE SET NULL,
  model text NOT NULL,
  input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
  output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  amount numeric(20,8) NOT NULL DEFAULT 0 CHECK (amount >= 0),
  currency text NOT NULL DEFAULT 'USD',
  status text NOT NULL DEFAULT 'RECORDED' CHECK (status IN ('RECORDED','CHARGED','WAIVED','REFUNDED')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS billing_usage_org_created_idx ON billing_usage_records(organization_id,created_at DESC);
CREATE INDEX IF NOT EXISTS billing_usage_project_created_idx ON billing_usage_records(project_id,created_at DESC);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id uuid NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
  usage_record_id uuid REFERENCES billing_usage_records(id) ON DELETE RESTRICT,
  transaction_type text NOT NULL CHECK (transaction_type IN ('TOPUP','CHARGE','REFUND','ADJUSTMENT','CREDIT')),
  amount numeric(20,8) NOT NULL CHECK (amount <> 0),
  balance_after numeric(20,8) NOT NULL,
  idempotency_key text,
  reference text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(wallet_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS wallet_transactions_wallet_created_idx ON wallet_transactions(wallet_id,created_at DESC);

INSERT INTO wallets(organization_id)
SELECT id FROM organizations ON CONFLICT(organization_id) DO NOTHING;

CREATE OR REPLACE FUNCTION create_organization_wallet()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO wallets(organization_id) VALUES(NEW.id) ON CONFLICT(organization_id) DO NOTHING;
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname='organizations_create_wallet_trigger') THEN
    CREATE TRIGGER organizations_create_wallet_trigger AFTER INSERT ON organizations
      FOR EACH ROW EXECUTE FUNCTION create_organization_wallet();
  END IF;
END;
$$;

-- Official OpenAI-compatible endpoints. Provider rows remain operator-owned;
-- the seed never overwrites an existing endpoint or configuration.
INSERT INTO providers(name,slug,provider_type,base_url,enabled,config)
VALUES
  ('Anthropic','anthropic','anthropic','https://api.anthropic.com/v1',true,'{"compatibility":"openai","chat_completions":true}'::jsonb),
  ('Google Gemini','gemini','gemini','https://generativelanguage.googleapis.com/v1beta/openai',true,'{"compatibility":"openai","chat_completions":true}'::jsonb),
  ('Qwen','qwen','qwen','https://dashscope.aliyuncs.com/compatible-mode/v1',true,'{"compatibility":"openai","chat_completions":true,"region":"cn"}'::jsonb),
  ('Kimi','kimi','kimi','https://api.moonshot.cn/v1',true,'{"compatibility":"openai","chat_completions":true,"region":"cn"}'::jsonb),
  ('GLM','glm','glm','https://open.bigmodel.cn/api/paas/v4',true,'{"compatibility":"openai","chat_completions":true,"region":"cn"}'::jsonb)
ON CONFLICT(slug) DO NOTHING;
