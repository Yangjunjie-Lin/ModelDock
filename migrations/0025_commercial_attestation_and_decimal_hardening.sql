-- Commercial Attestation metadata and fail-closed Decimal integrity.
--
-- This migration is additive. It never deletes ledger rows, rewrites a
-- settled amount, or treats a repository edit as commercial approval.
-- External evidence bodies and signing keys remain outside PostgreSQL.

CREATE TABLE commercial_attestation_verification_audit (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  repository text NOT NULL CHECK (repository ~ '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'),
  reviewed_commit text NOT NULL CHECK (reviewed_commit ~ '^[0-9a-f]{40}$'),
  reviewed_tree text NOT NULL CHECK (reviewed_tree ~ '^[0-9a-f]{40}$'),
  version text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$'),
  migration_version text NOT NULL CHECK (migration_version ~ '^[0-9]{4}_[a-z0-9_]+$'),
  release_profile text NOT NULL CHECK (release_profile IN ('COMMERCIAL_BETA','MARKETPLACE_PRODUCTION')),
  gate_id text NOT NULL CHECK (gate_id ~ '^[a-z][a-z0-9_]+$'),
  issuer text NOT NULL,
  issuer_role text NOT NULL CHECK (issuer_role IN ('Owner','Legal','Finance','Commercial','Security','Operations','IndependentTester')),
  evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
  signature_type text NOT NULL CHECK (signature_type='ed25519'),
  certificate_identity text NOT NULL,
  certificate_issuer text NOT NULL,
  verification_status text NOT NULL CHECK (verification_status IN ('VALID','INVALID','EXPIRED','BLOCKED')),
  workflow_run_id bigint NOT NULL CHECK (workflow_run_id>0),
  workflow_run_attempt integer NOT NULL CHECK (workflow_run_attempt>0),
  verified_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail)='object')
);
CREATE INDEX commercial_attestation_verification_identity_idx
  ON commercial_attestation_verification_audit(reviewed_commit,release_profile,gate_id,verified_at DESC);

CREATE OR REPLACE FUNCTION prevent_commercial_attestation_audit_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'commercial Attestation verification audit is append-only' USING ERRCODE='42501';
END;
$$;
DROP TRIGGER IF EXISTS commercial_attestation_verification_immutable
  ON commercial_attestation_verification_audit;
CREATE TRIGGER commercial_attestation_verification_immutable
  BEFORE UPDATE OR DELETE ON commercial_attestation_verification_audit
  FOR EACH ROW EXECUTE FUNCTION prevent_commercial_attestation_audit_mutation();

-- The Runtime Attestation generator applies the exact target customer/data
-- region to these database-derived candidates. A hand-entered Provider count
-- cannot satisfy this view.
CREATE OR REPLACE VIEW commercial_runtime_provider_candidates_v2 AS
SELECT
  provider.id,
  provider.contract_start_at,
  provider.contract_end_at,
  provider.allowed_customer_regions,
  provider.data_processing_regions,
  provider.commercial_status,
  (provider.commercial_resale_status='APPROVED') AS resale_approved,
  false AS kill_switch_enabled,
  true AS current_price_verified
FROM providers provider
WHERE provider.enabled
  AND NOT provider.pricing_disabled
  AND NOT provider.emergency_kill_switch
  AND provider.commercial_status='COMMERCIAL_APPROVED'
  AND provider.commercial_resale_status='APPROVED'
  AND provider.contract_start_at IS NOT NULL
  AND provider.contract_end_at IS NOT NULL
  AND provider.contract_start_at<=now()
  AND provider.contract_end_at>now()
  AND jsonb_array_length(provider.allowed_customer_regions)>0
  AND jsonb_array_length(provider.data_processing_regions)>0
  AND EXISTS (
    SELECT 1
    FROM provider_cost_price_book price
    WHERE price.provider_id=provider.id
      AND price.approval_status IN ('APPROVED','FORCED_APPROVED')
      AND price.effective_at<=now()
      AND (price.expires_at IS NULL OR price.expires_at>now())
  );

-- Every returned supplier satisfies the concrete KYB, contract, tax, invoice,
-- payout, and security predicates. The output contains IDs only so the
-- generator can salt/hash them before creating an Artifact.
CREATE OR REPLACE VIEW commercial_runtime_supplier_candidates_v2 AS
SELECT
  supplier.id,
  true AS kyb_approved,
  true AS contract_active,
  true AS tax_ready,
  true AS invoice_ready,
  true AS payout_ready,
  true AS security_review_approved
FROM supplier_organizations supplier
JOIN supplier_payout_readiness_review readiness ON readiness.supplier_id=supplier.id
WHERE supplier.status='APPROVED'
  AND supplier.kyb_status='VERIFIED'
  AND supplier.contract_status='ACTIVE'
  AND supplier.contract_start_at IS NOT NULL
  AND supplier.contract_start_at<=now()
  AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
  AND supplier.tax_id<>''
  AND supplier.tax_country<>''
  AND supplier.tax_form_type<>''
  AND supplier.payout_account_encrypted IS NOT NULL
  AND readiness.production_payout_enabled
  AND EXISTS (
    SELECT 1
    FROM supplier_security_questionnaires questionnaire
    WHERE questionnaire.supplier_id=supplier.id
      AND questionnaire.status='APPROVED'
  )
  AND EXISTS (
    SELECT 1
    FROM supplier_settlement_batch batch
    WHERE batch.supplier_id=supplier.id
      AND batch.tax_status='APPROVED'
      AND batch.invoice_status='APPROVED'
  );

-- Scan every commercial-looking NUMERIC column without changing its value.
-- PostgreSQL NUMERIC supports NaN/Infinity on some versions; scale/precision
-- declarations alone therefore do not prove a valid application Decimal.
CREATE OR REPLACE FUNCTION commercial_decimal_integrity_v2()
RETURNS TABLE(source_table text, source_column text, invalid_rows bigint)
LANGUAGE plpgsql STABLE AS $$
DECLARE
  item record;
  found_invalid bigint;
BEGIN
  FOR item IN
    SELECT col.table_schema, col.table_name, col.column_name
    FROM information_schema.columns col
    WHERE col.table_schema=current_schema()
      AND col.data_type='numeric'
      AND col.column_name ~ '(amount|cost|price|balance|budget|fee|margin|savings|refund|payable|invoice|tax|rate|credit|debit)'
    ORDER BY col.table_name,col.ordinal_position
  LOOP
    EXECUTE format(
      'SELECT count(*) FROM %I.%I WHERE CASE WHEN %I::text IN (''NaN'',''Infinity'',''-Infinity'') THEN true ELSE scale(%I)>12 OR abs(%I)>=1000000000000000000 END',
      item.table_schema,item.table_name,item.column_name,item.column_name,item.column_name
    ) INTO found_invalid;
    source_table := item.table_name;
    source_column := item.column_name;
    invalid_rows := found_invalid;
    RETURN NEXT;
  END LOOP;
END;
$$;

DO $$
DECLARE
  invalid_total bigint;
BEGIN
  SELECT COALESCE(sum(invalid_rows),0)
  INTO invalid_total
  FROM commercial_decimal_integrity_v2();
  IF invalid_total<>0 THEN
    RAISE EXCEPTION 'commercial Decimal integrity scan failed: % invalid row(s)',invalid_total
      USING ERRCODE='22003';
  END IF;
END;
$$;

COMMENT ON TABLE commercial_attestation_verification_audit IS
  'Append-only verification metadata; never a substitute for the external signed evidence Artifact.';
COMMENT ON VIEW commercial_runtime_provider_candidates_v2 IS
  'Database-derived Provider candidates for target-environment Runtime Attestation V2.';
COMMENT ON VIEW commercial_runtime_supplier_candidates_v2 IS
  'Database-derived supplier candidates for target-environment Runtime Attestation V2.';
COMMENT ON FUNCTION commercial_decimal_integrity_v2() IS
  'Fail-closed scan for non-finite, over-scale, or out-of-range commercial NUMERIC values.';
