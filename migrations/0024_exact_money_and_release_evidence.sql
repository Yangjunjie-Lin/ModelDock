-- Exact legacy money compatibility and release-evidence baseline.
--
-- Forward application:
--   1. add NUMERIC(30,12) companions without changing historical columns;
--   2. deterministically backfill the exact companions from stored decimals;
--   3. let the application prefer/write the exact companions while retaining
--      the legacy columns for old binaries and API compatibility.
--
-- Application rollback: deploy the previous binary. The legacy columns remain
-- present and populated. Database rollback is forward-repair only; if a full
-- restore is unavoidable, restore a verified pre-migration backup and retain
-- all ledger/audit evidence created after the backup for reconciliation.

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS monthly_cost_limit_exact numeric(30,12);
UPDATE users SET monthly_cost_limit_exact = monthly_cost_limit::numeric(30,12)
WHERE monthly_cost_limit_exact IS NULL AND monthly_cost_limit IS NOT NULL;

ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS monthly_cost_limit_exact numeric(30,12);
UPDATE api_keys SET monthly_cost_limit_exact = monthly_cost_limit::numeric(30,12)
WHERE monthly_cost_limit_exact IS NULL AND monthly_cost_limit IS NOT NULL;

ALTER TABLE teams
  ADD COLUMN IF NOT EXISTS monthly_cost_limit_exact numeric(30,12);
UPDATE teams SET monthly_cost_limit_exact = monthly_cost_limit::numeric(30,12)
WHERE monthly_cost_limit_exact IS NULL AND monthly_cost_limit IS NOT NULL;

ALTER TABLE model_prices
  ADD COLUMN IF NOT EXISTS input_price_exact numeric(30,12),
  ADD COLUMN IF NOT EXISTS cached_input_price_exact numeric(30,12),
  ADD COLUMN IF NOT EXISTS output_price_exact numeric(30,12);
UPDATE model_prices SET
  input_price_exact = COALESCE(input_price_exact, input_price::numeric(30,12)),
  cached_input_price_exact = COALESCE(cached_input_price_exact, cached_input_price::numeric(30,12)),
  output_price_exact = COALESCE(output_price_exact, output_price::numeric(30,12));
ALTER TABLE model_prices
  ALTER COLUMN input_price_exact SET NOT NULL,
  ALTER COLUMN cached_input_price_exact SET NOT NULL,
  ALTER COLUMN output_price_exact SET NOT NULL;

ALTER TABLE request_logs
  ADD COLUMN IF NOT EXISTS estimated_cost_exact numeric(30,12),
  ADD COLUMN IF NOT EXISTS reference_cost_exact numeric(30,12),
  ADD COLUMN IF NOT EXISTS savings_amount_exact numeric(30,12);
UPDATE request_logs SET
  estimated_cost_exact = COALESCE(estimated_cost_exact, estimated_cost::numeric(30,12)),
  reference_cost_exact = COALESCE(reference_cost_exact, reference_cost::numeric(30,12)),
  savings_amount_exact = COALESCE(savings_amount_exact, savings_amount::numeric(30,12));
ALTER TABLE request_logs
  ALTER COLUMN estimated_cost_exact SET NOT NULL,
  ALTER COLUMN reference_cost_exact SET NOT NULL,
  ALTER COLUMN savings_amount_exact SET NOT NULL;

ALTER TABLE usage_daily ADD COLUMN IF NOT EXISTS cost_exact numeric(30,12);
UPDATE usage_daily SET cost_exact = COALESCE(cost_exact, cost::numeric(30,12));
ALTER TABLE usage_daily ALTER COLUMN cost_exact SET NOT NULL;

ALTER TABLE usage_hourly ADD COLUMN IF NOT EXISTS cost_exact numeric(30,12);
UPDATE usage_hourly SET cost_exact = COALESCE(cost_exact, cost::numeric(30,12));
ALTER TABLE usage_hourly ALTER COLUMN cost_exact SET NOT NULL;

ALTER TABLE project_budget_policies
  ADD COLUMN IF NOT EXISTS cost_limit_exact numeric(30,12);
UPDATE project_budget_policies SET cost_limit_exact = cost_limit::numeric(30,12)
WHERE cost_limit_exact IS NULL AND cost_limit IS NOT NULL;

ALTER TABLE budget_events ADD COLUMN IF NOT EXISTS cost_exact numeric(30,12);
UPDATE budget_events SET cost_exact = COALESCE(cost_exact, cost::numeric(30,12));
ALTER TABLE budget_events ALTER COLUMN cost_exact SET NOT NULL;

-- During the compatibility window, exact writes also maintain the published
-- NUMERIC(20,8/10) columns. These explicit upper bounds are the largest policy
-- values that can be mirrored without overflow or a rounding carry.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='users_monthly_cost_limit_exact_nonnegative') THEN
    ALTER TABLE users ADD CONSTRAINT users_monthly_cost_limit_exact_nonnegative
      CHECK (monthly_cost_limit_exact IS NULL OR monthly_cost_limit_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='api_keys_monthly_cost_limit_exact_nonnegative') THEN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_monthly_cost_limit_exact_nonnegative
      CHECK (monthly_cost_limit_exact IS NULL OR monthly_cost_limit_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='teams_monthly_cost_limit_exact_nonnegative') THEN
    ALTER TABLE teams ADD CONSTRAINT teams_monthly_cost_limit_exact_nonnegative
      CHECK (monthly_cost_limit_exact IS NULL OR monthly_cost_limit_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='model_prices_exact_nonnegative') THEN
    ALTER TABLE model_prices ADD CONSTRAINT model_prices_exact_nonnegative CHECK (
      input_price_exact BETWEEN 0 AND 9999999999.999999999900
      AND cached_input_price_exact BETWEEN 0 AND 9999999999.999999999900
      AND output_price_exact BETWEEN 0 AND 9999999999.999999999900
    );
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='request_logs_exact_nonnegative') THEN
    ALTER TABLE request_logs ADD CONSTRAINT request_logs_exact_nonnegative CHECK (
      estimated_cost_exact BETWEEN 0 AND 999999999999.999999990000
      AND reference_cost_exact BETWEEN 0 AND 999999999999.999999990000
      AND savings_amount_exact BETWEEN 0 AND 999999999999.999999990000
    );
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_daily_cost_exact_nonnegative') THEN
    ALTER TABLE usage_daily ADD CONSTRAINT usage_daily_cost_exact_nonnegative
      CHECK (cost_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='usage_hourly_cost_exact_nonnegative') THEN
    ALTER TABLE usage_hourly ADD CONSTRAINT usage_hourly_cost_exact_nonnegative
      CHECK (cost_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='budget_policy_cost_limit_exact_nonnegative') THEN
    ALTER TABLE project_budget_policies ADD CONSTRAINT budget_policy_cost_limit_exact_nonnegative
      CHECK (cost_limit_exact IS NULL OR cost_limit_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='budget_events_cost_exact_nonnegative') THEN
    ALTER TABLE budget_events ADD CONSTRAINT budget_events_cost_exact_nonnegative
      CHECK (cost_exact BETWEEN 0 AND 999999999999.999999990000);
  END IF;
END $$;

-- Bidirectional compatibility triggers let a previous binary continue to write
-- the historical columns during an application rollback. Exact-column changes
-- win when both representations are supplied; legacy-only updates are promoted
-- deterministically to scale 12.
CREATE OR REPLACE FUNCTION sync_user_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.monthly_cost_limit_exact IS NOT DISTINCT FROM OLD.monthly_cost_limit_exact
     AND NEW.monthly_cost_limit IS DISTINCT FROM OLD.monthly_cost_limit THEN
    NEW.monthly_cost_limit_exact := NEW.monthly_cost_limit::numeric(30,12);
  ELSE
    NEW.monthly_cost_limit_exact := COALESCE(NEW.monthly_cost_limit_exact,NEW.monthly_cost_limit::numeric(30,12));
    NEW.monthly_cost_limit := CASE WHEN NEW.monthly_cost_limit_exact IS NULL THEN NULL ELSE round(NEW.monthly_cost_limit_exact,8) END;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS users_exact_money_sync ON users;
CREATE TRIGGER users_exact_money_sync BEFORE INSERT OR UPDATE OF monthly_cost_limit,monthly_cost_limit_exact ON users
FOR EACH ROW EXECUTE FUNCTION sync_user_exact_money();

CREATE OR REPLACE FUNCTION sync_api_key_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.monthly_cost_limit_exact IS NOT DISTINCT FROM OLD.monthly_cost_limit_exact
     AND NEW.monthly_cost_limit IS DISTINCT FROM OLD.monthly_cost_limit THEN
    NEW.monthly_cost_limit_exact := NEW.monthly_cost_limit::numeric(30,12);
  ELSE
    NEW.monthly_cost_limit_exact := COALESCE(NEW.monthly_cost_limit_exact,NEW.monthly_cost_limit::numeric(30,12));
    NEW.monthly_cost_limit := CASE WHEN NEW.monthly_cost_limit_exact IS NULL THEN NULL ELSE round(NEW.monthly_cost_limit_exact,8) END;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS api_keys_exact_money_sync ON api_keys;
CREATE TRIGGER api_keys_exact_money_sync BEFORE INSERT OR UPDATE OF monthly_cost_limit,monthly_cost_limit_exact ON api_keys
FOR EACH ROW EXECUTE FUNCTION sync_api_key_exact_money();

CREATE OR REPLACE FUNCTION sync_team_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.monthly_cost_limit_exact IS NOT DISTINCT FROM OLD.monthly_cost_limit_exact
     AND NEW.monthly_cost_limit IS DISTINCT FROM OLD.monthly_cost_limit THEN
    NEW.monthly_cost_limit_exact := NEW.monthly_cost_limit::numeric(30,12);
  ELSE
    NEW.monthly_cost_limit_exact := COALESCE(NEW.monthly_cost_limit_exact,NEW.monthly_cost_limit::numeric(30,12));
    NEW.monthly_cost_limit := CASE WHEN NEW.monthly_cost_limit_exact IS NULL THEN NULL ELSE round(NEW.monthly_cost_limit_exact,8) END;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS teams_exact_money_sync ON teams;
CREATE TRIGGER teams_exact_money_sync BEFORE INSERT OR UPDATE OF monthly_cost_limit,monthly_cost_limit_exact ON teams
FOR EACH ROW EXECUTE FUNCTION sync_team_exact_money();

CREATE OR REPLACE FUNCTION sync_model_price_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.input_price_exact IS NOT DISTINCT FROM OLD.input_price_exact
     AND NEW.cached_input_price_exact IS NOT DISTINCT FROM OLD.cached_input_price_exact
     AND NEW.output_price_exact IS NOT DISTINCT FROM OLD.output_price_exact THEN
    NEW.input_price_exact := NEW.input_price::numeric(30,12);
    NEW.cached_input_price_exact := NEW.cached_input_price::numeric(30,12);
    NEW.output_price_exact := NEW.output_price::numeric(30,12);
  ELSE
    NEW.input_price_exact := COALESCE(NEW.input_price_exact,NEW.input_price::numeric(30,12));
    NEW.cached_input_price_exact := COALESCE(NEW.cached_input_price_exact,NEW.cached_input_price::numeric(30,12));
    NEW.output_price_exact := COALESCE(NEW.output_price_exact,NEW.output_price::numeric(30,12));
    NEW.input_price := round(NEW.input_price_exact,10);
    NEW.cached_input_price := round(NEW.cached_input_price_exact,10);
    NEW.output_price := round(NEW.output_price_exact,10);
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS model_prices_exact_money_sync ON model_prices;
CREATE TRIGGER model_prices_exact_money_sync BEFORE INSERT OR UPDATE OF input_price,cached_input_price,output_price,input_price_exact,cached_input_price_exact,output_price_exact ON model_prices
FOR EACH ROW EXECUTE FUNCTION sync_model_price_exact_money();

CREATE OR REPLACE FUNCTION sync_request_log_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.estimated_cost_exact IS NOT DISTINCT FROM OLD.estimated_cost_exact
     AND NEW.reference_cost_exact IS NOT DISTINCT FROM OLD.reference_cost_exact
     AND NEW.savings_amount_exact IS NOT DISTINCT FROM OLD.savings_amount_exact THEN
    NEW.estimated_cost_exact := NEW.estimated_cost::numeric(30,12);
    NEW.reference_cost_exact := NEW.reference_cost::numeric(30,12);
    NEW.savings_amount_exact := NEW.savings_amount::numeric(30,12);
  ELSE
    NEW.estimated_cost_exact := COALESCE(NEW.estimated_cost_exact,NEW.estimated_cost::numeric(30,12));
    NEW.reference_cost_exact := COALESCE(NEW.reference_cost_exact,NEW.reference_cost::numeric(30,12));
    NEW.savings_amount_exact := COALESCE(NEW.savings_amount_exact,NEW.savings_amount::numeric(30,12));
    NEW.estimated_cost := round(NEW.estimated_cost_exact,8);
    NEW.reference_cost := round(NEW.reference_cost_exact,8);
    NEW.savings_amount := round(NEW.savings_amount_exact,8);
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS request_logs_exact_money_sync ON request_logs;
CREATE TRIGGER request_logs_exact_money_sync BEFORE INSERT OR UPDATE OF estimated_cost,reference_cost,savings_amount,estimated_cost_exact,reference_cost_exact,savings_amount_exact ON request_logs
FOR EACH ROW EXECUTE FUNCTION sync_request_log_exact_money();

CREATE OR REPLACE FUNCTION sync_usage_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.cost_exact IS NOT DISTINCT FROM OLD.cost_exact AND NEW.cost IS DISTINCT FROM OLD.cost THEN
    NEW.cost_exact := NEW.cost::numeric(30,12);
  ELSE
    NEW.cost_exact := COALESCE(NEW.cost_exact,NEW.cost::numeric(30,12));
    NEW.cost := round(NEW.cost_exact,8);
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS usage_daily_exact_money_sync ON usage_daily;
CREATE TRIGGER usage_daily_exact_money_sync BEFORE INSERT OR UPDATE OF cost,cost_exact ON usage_daily
FOR EACH ROW EXECUTE FUNCTION sync_usage_exact_money();
DROP TRIGGER IF EXISTS usage_hourly_exact_money_sync ON usage_hourly;
CREATE TRIGGER usage_hourly_exact_money_sync BEFORE INSERT OR UPDATE OF cost,cost_exact ON usage_hourly
FOR EACH ROW EXECUTE FUNCTION sync_usage_exact_money();

CREATE OR REPLACE FUNCTION sync_budget_policy_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.cost_limit_exact IS NOT DISTINCT FROM OLD.cost_limit_exact
     AND NEW.cost_limit IS DISTINCT FROM OLD.cost_limit THEN
    NEW.cost_limit_exact := NEW.cost_limit::numeric(30,12);
  ELSE
    NEW.cost_limit_exact := COALESCE(NEW.cost_limit_exact,NEW.cost_limit::numeric(30,12));
    NEW.cost_limit := CASE WHEN NEW.cost_limit_exact IS NULL THEN NULL ELSE round(NEW.cost_limit_exact,8) END;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS project_budget_policies_exact_money_sync ON project_budget_policies;
CREATE TRIGGER project_budget_policies_exact_money_sync BEFORE INSERT OR UPDATE OF cost_limit,cost_limit_exact ON project_budget_policies
FOR EACH ROW EXECUTE FUNCTION sync_budget_policy_exact_money();

CREATE OR REPLACE FUNCTION sync_budget_event_exact_money() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.cost_exact IS NOT DISTINCT FROM OLD.cost_exact AND NEW.cost IS DISTINCT FROM OLD.cost THEN
    NEW.cost_exact := NEW.cost::numeric(30,12);
  ELSE
    NEW.cost_exact := COALESCE(NEW.cost_exact,NEW.cost::numeric(30,12));
    NEW.cost := round(NEW.cost_exact,8);
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS budget_events_exact_money_sync ON budget_events;
CREATE TRIGGER budget_events_exact_money_sync BEFORE INSERT OR UPDATE OF cost,cost_exact ON budget_events
FOR EACH ROW EXECUTE FUNCTION sync_budget_event_exact_money();

-- Pre/post migration reconciliation. A non-zero row count is a release blocker.
CREATE OR REPLACE VIEW exact_money_migration_differences AS
SELECT 'users.monthly_cost_limit' AS path, count(*)::bigint AS differences
FROM users WHERE (monthly_cost_limit IS NULL) <> (monthly_cost_limit_exact IS NULL)
  OR (monthly_cost_limit IS NOT NULL AND monthly_cost_limit <> round(monthly_cost_limit_exact,8))
UNION ALL
SELECT 'api_keys.monthly_cost_limit', count(*)::bigint FROM api_keys
WHERE (monthly_cost_limit IS NULL) <> (monthly_cost_limit_exact IS NULL)
  OR (monthly_cost_limit IS NOT NULL AND monthly_cost_limit <> round(monthly_cost_limit_exact,8))
UNION ALL
SELECT 'teams.monthly_cost_limit', count(*)::bigint FROM teams
WHERE (monthly_cost_limit IS NULL) <> (monthly_cost_limit_exact IS NULL)
  OR (monthly_cost_limit IS NOT NULL AND monthly_cost_limit <> round(monthly_cost_limit_exact,8))
UNION ALL
SELECT 'model_prices', count(*)::bigint FROM model_prices
WHERE input_price <> round(input_price_exact,10)
   OR cached_input_price <> round(cached_input_price_exact,10)
   OR output_price <> round(output_price_exact,10)
UNION ALL
SELECT 'request_logs', count(*)::bigint FROM request_logs
WHERE estimated_cost <> round(estimated_cost_exact,8)
   OR reference_cost <> round(reference_cost_exact,8)
   OR savings_amount <> round(savings_amount_exact,8)
UNION ALL
SELECT 'usage_daily.cost', count(*)::bigint FROM usage_daily
WHERE cost <> round(cost_exact,8)
UNION ALL
SELECT 'usage_hourly.cost', count(*)::bigint FROM usage_hourly
WHERE cost <> round(cost_exact,8)
UNION ALL
SELECT 'project_budget_policies.cost_limit', count(*)::bigint FROM project_budget_policies
WHERE (cost_limit IS NULL) <> (cost_limit_exact IS NULL)
   OR (cost_limit IS NOT NULL AND cost_limit <> round(cost_limit_exact,8))
UNION ALL
SELECT 'budget_events.cost', count(*)::bigint FROM budget_events
WHERE cost <> round(cost_exact,8);

COMMENT ON VIEW exact_money_migration_differences IS
  'All rows must report zero before switching writers to exact-only mode.';
