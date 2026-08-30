-- OpenRouter-inspired operating model: workspace policy, request routing
-- controls, ordered BYOK capacity, shadow accounting, free-model admission,
-- provider capability documents, and enterprise identity configuration.
-- The migration is additive and preserves every existing project, credential,
-- price, request, wallet, supplier, and provisioning record.

CREATE TABLE IF NOT EXISTS workspace_settings (
  project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  default_provider_policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(default_provider_policy)='object'),
  privacy_policy jsonb NOT NULL DEFAULT '{"data_collection":"deny","zdr":false}'::jsonb CHECK (jsonb_typeof(privacy_policy)='object'),
  observability_config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(observability_config)='object'),
  include_byok_in_budgets boolean NOT NULL DEFAULT false,
  free_daily_request_limit integer NOT NULL DEFAULT 0 CHECK (free_daily_request_limit >= 0),
  free_daily_token_limit bigint NOT NULL DEFAULT 0 CHECK (free_daily_token_limit >= 0),
  allowed_processing_regions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_processing_regions)='array'),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO workspace_settings(project_id)
SELECT id FROM projects ON CONFLICT(project_id) DO NOTHING;

CREATE OR REPLACE FUNCTION seed_workspace_settings()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO workspace_settings(project_id) VALUES(NEW.id) ON CONFLICT(project_id) DO NOTHING;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS projects_seed_workspace_settings_trigger ON projects;
CREATE TRIGGER projects_seed_workspace_settings_trigger
  AFTER INSERT ON projects FOR EACH ROW EXECUTE FUNCTION seed_workspace_settings();

ALTER TABLE provider_credentials
  ADD COLUMN IF NOT EXISTS byok_priority_section text NOT NULL DEFAULT 'PRIORITIZED',
  ADD COLUMN IF NOT EXISTS shared_capacity_fallback text NOT NULL DEFAULT 'ALWAYS',
  ADD COLUMN IF NOT EXISTS model_filters jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS api_key_filters jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS member_filters jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE provider_credentials DROP CONSTRAINT IF EXISTS provider_credentials_byok_priority_section_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_byok_priority_section_check
  CHECK (byok_priority_section IN ('PRIORITIZED','FALLBACK'));
ALTER TABLE provider_credentials DROP CONSTRAINT IF EXISTS provider_credentials_shared_capacity_fallback_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_shared_capacity_fallback_check
  CHECK (shared_capacity_fallback IN ('ALWAYS','OUTSIDE_FILTERS','NEVER'));
ALTER TABLE provider_credentials DROP CONSTRAINT IF EXISTS provider_credentials_model_filters_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_model_filters_check CHECK (jsonb_typeof(model_filters)='array');
ALTER TABLE provider_credentials DROP CONSTRAINT IF EXISTS provider_credentials_api_key_filters_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_api_key_filters_check CHECK (jsonb_typeof(api_key_filters)='array');
ALTER TABLE provider_credentials DROP CONSTRAINT IF EXISTS provider_credentials_member_filters_check;
ALTER TABLE provider_credentials ADD CONSTRAINT provider_credentials_member_filters_check CHECK (jsonb_typeof(member_filters)='array');

ALTER TABLE byok_service_fee_policies
  ADD COLUMN IF NOT EXISTS service_fee_bps integer NOT NULL DEFAULT 0 CHECK (service_fee_bps BETWEEN 0 AND 10000),
  ADD COLUMN IF NOT EXISTS monthly_free_allowance numeric(30,12) NOT NULL DEFAULT 0 CHECK (monthly_free_allowance >= 0);

ALTER TABLE funding_operation
  ADD COLUMN IF NOT EXISTS byok_service_fee_bps integer NOT NULL DEFAULT 0 CHECK (byok_service_fee_bps BETWEEN 0 AND 10000),
  ADD COLUMN IF NOT EXISTS byok_monthly_free_allowance numeric(30,12) NOT NULL DEFAULT 0 CHECK (byok_monthly_free_allowance >= 0),
  ADD COLUMN IF NOT EXISTS byok_shadow_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (byok_shadow_amount >= 0),
  ADD COLUMN IF NOT EXISTS routing_policy_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(routing_policy_snapshot)='object');

CREATE TABLE IF NOT EXISTS byok_shadow_spend_monthly (
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  credential_id uuid NOT NULL REFERENCES provider_credentials(id) ON DELETE RESTRICT,
  period_month date NOT NULL,
  currency text NOT NULL,
  shadow_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (shadow_amount >= 0),
  service_fee_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (service_fee_amount >= 0),
  request_count bigint NOT NULL DEFAULT 0 CHECK (request_count >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(project_id,credential_id,period_month)
);

CREATE TABLE IF NOT EXISTS free_model_usage_daily (
  usage_date date NOT NULL,
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
  requests bigint NOT NULL DEFAULT 0 CHECK (requests >= 0),
  reserved_tokens bigint NOT NULL DEFAULT 0 CHECK (reserved_tokens >= 0),
  settled_tokens bigint NOT NULL DEFAULT 0 CHECK (settled_tokens >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(usage_date,project_id,api_key_id)
);

CREATE TABLE IF NOT EXISTS provider_capability_documents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  schema_version text NOT NULL,
  document jsonb NOT NULL CHECK (jsonb_typeof(document)='object'),
  source_url text,
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','SUPERSEDED','REJECTED')),
  fetched_at timestamptz NOT NULL DEFAULT now(),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider_id,source_sha256)
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_capability_documents_active_idx
  ON provider_capability_documents(provider_id) WHERE status='ACTIVE';

CREATE OR REPLACE FUNCTION prevent_provider_capability_document_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR NEW.provider_id<>OLD.provider_id OR NEW.schema_version<>OLD.schema_version OR
     NEW.document<>OLD.document OR NEW.source_url IS DISTINCT FROM OLD.source_url OR
     NEW.source_sha256<>OLD.source_sha256 OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
     NEW.created_at<>OLD.created_at THEN
    RAISE EXCEPTION 'provider capability documents are append-only' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS provider_capability_documents_immutable_trigger ON provider_capability_documents;
CREATE TRIGGER provider_capability_documents_immutable_trigger
  BEFORE UPDATE OR DELETE ON provider_capability_documents
  FOR EACH ROW EXECUTE FUNCTION prevent_provider_capability_document_mutation();

CREATE TABLE IF NOT EXISTS enterprise_identity_connections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE RESTRICT,
  issuer_url text NOT NULL DEFAULT '',
  client_id text NOT NULL DEFAULT '',
  client_secret_encrypted bytea,
  scim_token_hash bytea,
  allowed_domains jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_domains)='array'),
  sso_enabled boolean NOT NULL DEFAULT false,
  scim_enabled boolean NOT NULL DEFAULT false,
  enforce_sso boolean NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'DISABLED' CHECK (status IN ('ACTIVE','DISABLED')),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (NOT enforce_sso OR (sso_enabled AND status='ACTIVE')),
  CHECK (NOT scim_enabled OR (scim_token_hash IS NOT NULL AND status='ACTIVE'))
);

CREATE INDEX IF NOT EXISTS enterprise_identity_connections_status_idx
  ON enterprise_identity_connections(status,organization_id);

-- Stable SCIM identifiers are kept separately from users and teams so an
-- identity provider can manage one organization without taking ownership of
-- the same RelayDock user in another organization.  Resource IDs are checked
-- transactionally by the SCIM store before a link is written.
CREATE TABLE IF NOT EXISTS scim_resource_links (
  organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
  resource_type text NOT NULL CHECK (resource_type IN ('USER','GROUP')),
  resource_id uuid NOT NULL,
  external_id text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id,resource_type,resource_id),
  CHECK (external_id='' OR length(external_id)<=255)
);

CREATE UNIQUE INDEX IF NOT EXISTS scim_resource_links_external_idx
  ON scim_resource_links(organization_id,resource_type,external_id)
  WHERE external_id<>'';
