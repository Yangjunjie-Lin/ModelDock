-- Marketplace launch acceptance is an additive, forward-only release gate for
-- supplier-backed Providers. Existing /v1 routes and first-party Providers are
-- unchanged. Provider declarations remain display-only and cannot satisfy a
-- platform release gate or authorize production payout.

ALTER TABLE provider_marketplace_listings
  DROP CONSTRAINT IF EXISTS provider_marketplace_listings_status_check;
ALTER TABLE provider_marketplace_listings
  ADD CONSTRAINT provider_marketplace_listings_status_check
  CHECK (status IN ('DRAFT','REVIEW','CANARY','ACTIVE','SUSPENDED','REJECTED','EXITED'));

CREATE OR REPLACE FUNCTION marketplace_listing_release_fingerprint(uuid,text,jsonb,jsonb,jsonb)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT encode(digest($1::text||chr(31)||lower(rtrim($2,'/'))||chr(31)||$3::text||chr(31)||$4::text||chr(31)||$5::text,'sha256'),'hex')
$$;

CREATE TABLE marketplace_launch_review (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  request_fingerprint text NOT NULL CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
  listing_fingerprint_sha256 text NOT NULL CHECK (listing_fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
  listing_id uuid NOT NULL REFERENCES provider_marketplace_listings(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  revision integer NOT NULL CHECK (revision > 0),
  policy_version text NOT NULL,
  status text NOT NULL DEFAULT 'DRAFT'
    CHECK (status IN ('DRAFT','IN_REVIEW','APPROVED','REJECTED','REVOKED')),
  reason text NOT NULL DEFAULT '',
  created_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  approved_by uuid REFERENCES users(id) ON DELETE RESTRICT,
  approved_at timestamptz,
  revoked_by uuid REFERENCES users(id) ON DELETE RESTRICT,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(listing_id,revision),
  CHECK (status<>'APPROVED' OR (approved_at IS NOT NULL AND approved_by IS NOT NULL)),
  CHECK (status<>'REVOKED' OR (revoked_at IS NOT NULL AND revoked_by IS NOT NULL))
);
CREATE UNIQUE INDEX marketplace_launch_review_open_uq ON marketplace_launch_review(listing_id)
  WHERE status IN ('DRAFT','IN_REVIEW');
CREATE UNIQUE INDEX marketplace_launch_review_approved_uq ON marketplace_launch_review(listing_id)
  WHERE status='APPROVED';
CREATE INDEX marketplace_launch_review_queue_idx ON marketplace_launch_review(status,updated_at,id);

CREATE TABLE marketplace_launch_gate (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  review_id uuid NOT NULL REFERENCES marketplace_launch_review(id) ON DELETE RESTRICT,
  gate_code text NOT NULL CHECK (gate_code IN (
    'SUPPLIER_REGISTRATION','QUALIFICATION_REVIEW','ENDPOINT_VERIFICATION','MODEL_PUBLICATION',
    'PRICE_APPROVAL','HEALTH_TEST','CANARY_RAMP','ROUTING_RANKING','USER_INVOCATION','USER_CHARGE',
    'PLATFORM_COMMISSION','SUPPLIER_PAYABLE','REFUND_ALLOCATION','SETTLEMENT','RECONCILIATION','DISPUTE',
    'SUPPLIER_SUSPENSION_DRILL','EMERGENCY_CUTOVER_DRILL','SUPPLIER_EXIT_DRILL',
    'CONTRACT_REVIEW','TAX_REVIEW','PAYMENT_REVIEW','SECURITY_REVIEW'
  )),
  evidence_source text NOT NULL CHECK (evidence_source IN ('PLATFORM_AUTOMATED','ADMIN_ATTESTATION')),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PASSED','FAILED')),
  evidence_reference text NOT NULL DEFAULT '',
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence)='object'),
  evaluated_by uuid REFERENCES users(id) ON DELETE RESTRICT,
  evaluated_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(review_id,gate_code),
  CHECK ((status='PENDING')=(evaluated_at IS NULL)),
  CHECK (status='PENDING' OR evidence_reference<>'')
);
CREATE INDEX marketplace_launch_gate_status_idx ON marketplace_launch_gate(review_id,status,gate_code);

CREATE TABLE marketplace_launch_gate_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  gate_id uuid NOT NULL REFERENCES marketplace_launch_gate(id) ON DELETE RESTRICT,
  from_status text NOT NULL CHECK (from_status IN ('PENDING','PASSED','FAILED')),
  to_status text NOT NULL CHECK (to_status IN ('PENDING','PASSED','FAILED')),
  evidence_reference text NOT NULL,
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(evidence)='object'),
  actor_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX marketplace_launch_gate_event_gate_idx ON marketplace_launch_gate_event(gate_id,created_at,id);

CREATE TABLE supplier_payout_readiness_review (
  supplier_id uuid PRIMARY KEY REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  contract_status text NOT NULL DEFAULT 'PENDING' CHECK (contract_status IN ('PENDING','APPROVED','REJECTED')),
  contract_evidence_reference text NOT NULL DEFAULT '',
  tax_status text NOT NULL DEFAULT 'PENDING' CHECK (tax_status IN ('PENDING','APPROVED','REJECTED')),
  tax_evidence_reference text NOT NULL DEFAULT '',
  payment_status text NOT NULL DEFAULT 'PENDING' CHECK (payment_status IN ('PENDING','APPROVED','REJECTED')),
  payment_evidence_reference text NOT NULL DEFAULT '',
  security_status text NOT NULL DEFAULT 'PENDING' CHECK (security_status IN ('PENDING','APPROVED','REJECTED')),
  security_evidence_reference text NOT NULL DEFAULT '',
  production_payout_enabled boolean GENERATED ALWAYS AS (
    contract_status='APPROVED' AND tax_status='APPROVED' AND payment_status='APPROVED' AND security_status='APPROVED'
  ) STORED,
  review_reason text NOT NULL DEFAULT '',
  reviewed_by uuid REFERENCES users(id) ON DELETE RESTRICT,
  reviewed_at timestamptz,
  version bigint NOT NULL DEFAULT 0 CHECK (version>=0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (contract_status='PENDING' OR contract_evidence_reference<>''),
  CHECK (tax_status='PENDING' OR tax_evidence_reference<>''),
  CHECK (payment_status='PENDING' OR payment_evidence_reference<>''),
  CHECK (security_status='PENDING' OR security_evidence_reference<>''),
  CHECK ((reviewed_at IS NULL)=(reviewed_by IS NULL))
);
INSERT INTO supplier_payout_readiness_review(supplier_id)
SELECT id FROM supplier_organizations ON CONFLICT(supplier_id) DO NOTHING;

CREATE TABLE marketplace_provider_lifecycle_event (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  listing_id uuid NOT NULL REFERENCES provider_marketplace_listings(id) ON DELETE RESTRICT,
  provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  supplier_id uuid NOT NULL REFERENCES supplier_organizations(id) ON DELETE RESTRICT,
  action text NOT NULL CHECK (action IN ('CANARY_START','SUSPEND','RESUME','EMERGENCY_CUTOVER','EXIT')),
  from_listing_status text NOT NULL,
  to_listing_status text NOT NULL,
  reason text NOT NULL,
  actor_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX marketplace_provider_lifecycle_event_idx
  ON marketplace_provider_lifecycle_event(listing_id,created_at DESC,id);

CREATE OR REPLACE FUNCTION seed_supplier_payout_readiness()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO supplier_payout_readiness_review(supplier_id) VALUES(NEW.id)
    ON CONFLICT(supplier_id) DO NOTHING;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS supplier_seed_payout_readiness_trigger ON supplier_organizations;
CREATE TRIGGER supplier_seed_payout_readiness_trigger AFTER INSERT ON supplier_organizations
  FOR EACH ROW EXECUTE FUNCTION seed_supplier_payout_readiness();

CREATE OR REPLACE FUNCTION protect_marketplace_listing_activation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('CANARY','ACTIVE')
     AND EXISTS(SELECT 1 FROM supplier_provider_links link WHERE link.provider_id=NEW.provider_id) THEN
    IF NEW.status='CANARY' AND NOT EXISTS(
      SELECT 1 FROM marketplace_launch_review review
      WHERE review.listing_id=NEW.id AND review.provider_id=NEW.provider_id AND review.status='IN_REVIEW'
        AND review.listing_fingerprint_sha256=marketplace_listing_release_fingerprint(NEW.provider_id,NEW.endpoint,NEW.supported_models,NEW.price,NEW.metadata)
        AND NOT EXISTS(
          SELECT 1 FROM marketplace_launch_gate gate
          WHERE gate.review_id=review.id
            AND gate.gate_code IN ('SUPPLIER_REGISTRATION','QUALIFICATION_REVIEW','ENDPOINT_VERIFICATION',
              'MODEL_PUBLICATION','PRICE_APPROVAL','HEALTH_TEST')
            AND gate.status<>'PASSED'
        )
    ) THEN
      RAISE EXCEPTION 'Marketplace canary requires passed foundation gates' USING ERRCODE='42501';
    END IF;
    IF NEW.status='ACTIVE' AND NOT EXISTS(
      SELECT 1 FROM marketplace_launch_review review
      WHERE review.listing_id=NEW.id AND review.provider_id=NEW.provider_id AND review.status='APPROVED'
        AND review.listing_fingerprint_sha256=marketplace_listing_release_fingerprint(NEW.provider_id,NEW.endpoint,NEW.supported_models,NEW.price,NEW.metadata)
        AND NOT EXISTS(SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id AND gate.status<>'PASSED')
    ) THEN
      RAISE EXCEPTION 'Marketplace activation requires an approved platform release review' USING ERRCODE='42501';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS marketplace_listing_activation_trigger ON provider_marketplace_listings;
CREATE TRIGGER marketplace_listing_activation_trigger
  BEFORE INSERT OR UPDATE OF status,provider_id,endpoint,supported_models,price,metadata ON provider_marketplace_listings
  FOR EACH ROW EXECUTE FUNCTION protect_marketplace_listing_activation();

CREATE OR REPLACE FUNCTION protect_production_supplier_payout()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.payout_adapter<>'sandbox' AND NEW.status IN ('APPROVED','PROCESSING','PAID')
     AND NOT EXISTS(
       SELECT 1 FROM supplier_payout_readiness_review readiness
       JOIN supplier_organizations supplier ON supplier.id=readiness.supplier_id
       WHERE readiness.supplier_id=NEW.supplier_id AND readiness.production_payout_enabled
         AND supplier.contract_status='ACTIVE' AND supplier.kyb_status='VERIFIED'
         AND supplier.contract_start_at IS NOT NULL AND supplier.contract_start_at<=now()
         AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
         AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>''
         AND supplier.payout_account_encrypted IS NOT NULL
         AND EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
           WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')
     ) THEN
    RAISE EXCEPTION 'production payout requires approved contract, tax, payment, and security reviews' USING ERRCODE='42501';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS supplier_production_payout_readiness_trigger ON supplier_settlement_batch;
CREATE TRIGGER supplier_production_payout_readiness_trigger
  BEFORE INSERT OR UPDATE OF status,payout_adapter,supplier_id ON supplier_settlement_batch
  FOR EACH ROW EXECUTE FUNCTION protect_production_supplier_payout();

CREATE OR REPLACE FUNCTION prevent_marketplace_acceptance_evidence_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Marketplace acceptance evidence is append-only' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS marketplace_launch_gate_event_immutable_trigger ON marketplace_launch_gate_event;
CREATE TRIGGER marketplace_launch_gate_event_immutable_trigger
  BEFORE UPDATE OR DELETE ON marketplace_launch_gate_event
  FOR EACH ROW EXECUTE FUNCTION prevent_marketplace_acceptance_evidence_delete();
DROP TRIGGER IF EXISTS marketplace_provider_lifecycle_event_immutable_trigger ON marketplace_provider_lifecycle_event;
CREATE TRIGGER marketplace_provider_lifecycle_event_immutable_trigger
  BEFORE UPDATE OR DELETE ON marketplace_provider_lifecycle_event
  FOR EACH ROW EXECUTE FUNCTION prevent_marketplace_acceptance_evidence_delete();
