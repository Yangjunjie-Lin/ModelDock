-- Supplier onboarding and review evidence.  This migration is additive and
-- forward-only; it never changes the existing /v1 contract or provider rows.

CREATE TABLE IF NOT EXISTS supplier_organizations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE RESTRICT,
  owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  legal_name text NOT NULL DEFAULT '',
  display_name text NOT NULL DEFAULT '',
  registration_number text NOT NULL DEFAULT '',
  incorporation_country text NOT NULL DEFAULT '',
  website text NOT NULL DEFAULT '',
  kyb_status text NOT NULL DEFAULT 'NOT_STARTED'
    CHECK (kyb_status IN ('NOT_STARTED','PENDING','VERIFIED','REJECTED','EXPIRED')),
  contract_status text NOT NULL DEFAULT 'NOT_STARTED'
    CHECK (contract_status IN ('NOT_STARTED','PENDING','ACTIVE','EXPIRED','TERMINATED')),
  contract_version text NOT NULL DEFAULT '',
  contract_start_at timestamptz,
  contract_end_at timestamptz,
  status text NOT NULL DEFAULT 'DRAFT'
    CHECK (status IN ('DRAFT','SUBMITTED','IN_REVIEW','APPROVED','REJECTED','SUSPENDED','EXIT_REQUESTED','EXITED')),
  payout_account_encrypted bytea,
  payout_account_last4 text NOT NULL DEFAULT '',
  payout_currency text NOT NULL DEFAULT 'USD' CHECK (payout_currency ~ '^[A-Z]{3}$'),
  tax_id text NOT NULL DEFAULT '',
  tax_country text NOT NULL DEFAULT '',
  tax_residency text NOT NULL DEFAULT '',
  tax_form_type text NOT NULL DEFAULT '',
  version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (contract_end_at IS NULL OR contract_start_at IS NULL OR contract_end_at > contract_start_at)
);
CREATE INDEX IF NOT EXISTS supplier_organizations_status_idx ON supplier_organizations(status,created_at DESC);
CREATE INDEX IF NOT EXISTS supplier_organizations_owner_idx ON supplier_organizations(owner_user_id,status);

CREATE TABLE IF NOT EXISTS supplier_contacts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  contact_type text NOT NULL CHECK (contact_type IN ('LEGAL','PRIMARY','BILLING','SECURITY')),
  full_name text NOT NULL,
  title text NOT NULL DEFAULT '',
  email text NOT NULL,
  phone text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(supplier_id,contact_type)
);

CREATE TABLE IF NOT EXISTS supplier_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  endpoint_url text NOT NULL,
  challenge_hash bytea NOT NULL,
  verification_status text NOT NULL DEFAULT 'PENDING'
    CHECK (verification_status IN ('PENDING','VERIFIED','FAILED')),
  isolation_status text NOT NULL DEFAULT 'PENDING'
    CHECK (isolation_status IN ('PENDING','PASSED','FAILED')),
  verified_at timestamptz,
  last_checked_ip inet,
  last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(supplier_id,endpoint_url)
);
CREATE INDEX IF NOT EXISTS supplier_endpoints_status_idx ON supplier_endpoints(supplier_id,verification_status,isolation_status);

CREATE TABLE IF NOT EXISTS supplier_credentials (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  provider_id uuid REFERENCES providers(id) ON DELETE RESTRICT,
  name text NOT NULL,
  credential_type text NOT NULL DEFAULT 'api_key' CHECK (credential_type IN ('api_key','service_account','workload_identity')),
  encrypted_secret bytea NOT NULL,
  secret_last4 text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','ACTIVE','DISABLED','REVOKED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS supplier_credentials_status_idx ON supplier_credentials(supplier_id,status);

CREATE TABLE IF NOT EXISTS supplier_data_residency_declarations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  endpoint_id uuid REFERENCES supplier_endpoints(id) ON DELETE RESTRICT,
  processing_regions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(processing_regions)='array'),
  storage_regions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(storage_regions)='array'),
  cross_border_transfer boolean NOT NULL DEFAULT false,
  retention_days integer CHECK (retention_days IS NULL OR retention_days > 0),
  subprocessors jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(subprocessors)='array'),
  attestation text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS supplier_security_questionnaires (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  version text NOT NULL,
  answers jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(answers)='object'),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED')),
  submitted_at timestamptz,
  reviewed_at timestamptz,
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(supplier_id,version)
);

CREATE TABLE IF NOT EXISTS supplier_model_applications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  endpoint_id uuid NOT NULL REFERENCES supplier_endpoints(id) ON DELETE RESTRICT,
  model_name text NOT NULL,
  model_type text NOT NULL DEFAULT 'text',
  capabilities jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(capabilities)='array'),
  data_residency_declaration_id uuid REFERENCES supplier_data_residency_declarations(id) ON DELETE RESTRICT,
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','IN_REVIEW','APPROVED','REJECTED','WITHDRAWN')),
  review_reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS supplier_model_applications_status_idx ON supplier_model_applications(supplier_id,status,created_at DESC);

CREATE TABLE IF NOT EXISTS supplier_price_applications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  model_application_id uuid NOT NULL REFERENCES supplier_model_applications(id) ON DELETE RESTRICT,
  input_token_price numeric(30,12) NOT NULL CHECK (input_token_price >= 0),
  cached_input_token_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (cached_input_token_price >= 0),
  output_token_price numeric(30,12) NOT NULL CHECK (output_token_price >= 0),
  request_fixed_price numeric(30,12) NOT NULL DEFAULT 0 CHECK (request_fixed_price >= 0),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  unit bigint NOT NULL CHECK (unit > 0),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','IN_REVIEW','APPROVED','REJECTED','WITHDRAWN')),
  review_reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS supplier_reviews (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  reviewer_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  decision text NOT NULL CHECK (decision IN ('APPROVED','REJECTED','REQUESTED_CHANGES','SUSPENDED','EXITED')),
  reason text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS supplier_reviews_supplier_idx ON supplier_reviews(supplier_id,created_at DESC);

CREATE TABLE IF NOT EXISTS supplier_status_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE CASCADE,
  from_status text NOT NULL,
  to_status text NOT NULL,
  actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION protect_supplier_status()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF COALESCE(current_setting('relaydock.supplier_admin_action', true),'') <> 'true' THEN
    IF TG_OP='INSERT' AND NEW.status='APPROVED' THEN
      RAISE EXCEPTION 'supplier cannot self approve' USING ERRCODE='42501';
    END IF;
    IF TG_OP='UPDATE' AND NEW.status IS DISTINCT FROM OLD.status THEN
      IF NEW.status='APPROVED' THEN
        RAISE EXCEPTION 'supplier cannot self approve' USING ERRCODE='42501';
      END IF;
      IF NEW.status NOT IN ('DRAFT','SUBMITTED','EXIT_REQUESTED') THEN
        RAISE EXCEPTION 'supplier status can only be changed by an administrator' USING ERRCODE='42501';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS supplier_status_protection_trigger ON supplier_organizations;
CREATE TRIGGER supplier_status_protection_trigger
  BEFORE INSERT OR UPDATE OF status ON supplier_organizations
  FOR EACH ROW EXECUTE FUNCTION protect_supplier_status();

CREATE OR REPLACE FUNCTION prevent_supplier_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'supplier review evidence is append-only' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS supplier_review_append_only_trigger ON supplier_reviews;
CREATE TRIGGER supplier_review_append_only_trigger BEFORE UPDATE OR DELETE ON supplier_reviews
  FOR EACH ROW EXECUTE FUNCTION prevent_supplier_evidence_mutation();
