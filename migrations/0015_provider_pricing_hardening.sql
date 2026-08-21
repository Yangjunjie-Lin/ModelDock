-- Harden append-only Provider pricing evidence and retain the exact BYOK fee
-- policy used by each usage snapshot. This is a forward-only migration; see
-- docs/provider-governance.md for the operational rollback procedure.

ALTER TABLE usage_price_snapshot
  ADD COLUMN IF NOT EXISTS byok_service_fee_policy_id uuid
  REFERENCES byok_service_fee_policies(id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION reject_provider_cost_price_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'provider cost price records are immutable' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS provider_cost_price_book_immutable_trigger ON provider_cost_price_book;
CREATE TRIGGER provider_cost_price_book_immutable_trigger
  BEFORE UPDATE OR DELETE ON provider_cost_price_book
  FOR EACH ROW EXECUTE FUNCTION reject_provider_cost_price_mutation();

CREATE OR REPLACE FUNCTION protect_byok_service_fee_policy()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'BYOK service fee policies cannot be deleted' USING ERRCODE='55000';
  END IF;
  IF NEW.organization_id IS DISTINCT FROM OLD.organization_id OR
     NEW.provider_id IS DISTINCT FROM OLD.provider_id OR
     NEW.fixed_fee IS DISTINCT FROM OLD.fixed_fee OR
     NEW.input_token_fee IS DISTINCT FROM OLD.input_token_fee OR
     NEW.cached_input_token_fee IS DISTINCT FROM OLD.cached_input_token_fee OR
     NEW.output_token_fee IS DISTINCT FROM OLD.output_token_fee OR
     NEW.currency IS DISTINCT FROM OLD.currency OR
     NEW.unit IS DISTINCT FROM OLD.unit OR
     NEW.effective_at IS DISTINCT FROM OLD.effective_at OR
     NEW.expires_at IS DISTINCT FROM OLD.expires_at OR
     NEW.created_by IS DISTINCT FROM OLD.created_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at OR
     (OLD.enabled=false AND NEW.enabled=true) THEN
    RAISE EXCEPTION 'BYOK service fee policy terms are immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS byok_service_fee_policy_immutable_trigger ON byok_service_fee_policies;
CREATE TRIGGER byok_service_fee_policy_immutable_trigger
  BEFORE UPDATE OR DELETE ON byok_service_fee_policies
  FOR EACH ROW EXECUTE FUNCTION protect_byok_service_fee_policy();

COMMENT ON COLUMN usage_price_snapshot.byok_service_fee_policy_id IS
  'Immutable BYOK fee policy version applied to this usage record.';
