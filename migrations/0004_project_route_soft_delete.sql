-- Preserve immutable request/usage history when an administrator removes a
-- project route grant. Historical ledgers retain their composite foreign-key
-- reference while active catalogs exclude the tombstoned grant.

ALTER TABLE project_model_routes
  ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

CREATE INDEX IF NOT EXISTS project_model_routes_active_project_idx
  ON project_model_routes(project_id,alias)
  WHERE deleted_at IS NULL;
