-- V2 status expansion is an append-only follow-up to the frozen tenant
-- migration. DISABLED is reversible and immediately fails closed in Gateway;
-- ARCHIVED remains the terminal soft-delete state used for retained history.

ALTER TABLE organizations DROP CONSTRAINT organizations_status_check;
ALTER TABLE organizations ADD CONSTRAINT organizations_status_check
  CHECK (status IN ('ACTIVE','DISABLED','ARCHIVED'));

ALTER TABLE projects DROP CONSTRAINT projects_status_check;
ALTER TABLE projects ADD CONSTRAINT projects_status_check
  CHECK (status IN ('ACTIVE','DISABLED','ARCHIVED'));
