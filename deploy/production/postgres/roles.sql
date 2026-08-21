-- Run as the database owner after creating the database. Login roles and
-- passwords are provisioned by the secret manager/IAM layer; this file grants
-- capabilities without embedding credentials.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='modeldock_runtime') THEN
    CREATE ROLE modeldock_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='modeldock_migrator') THEN
    CREATE ROLE modeldock_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
  END IF;
END $$;

GRANT CONNECT ON DATABASE relaydock TO modeldock_runtime,modeldock_migrator;
GRANT USAGE ON SCHEMA public TO modeldock_runtime;
GRANT USAGE,CREATE ON SCHEMA public TO modeldock_migrator;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO modeldock_runtime;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO modeldock_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE modeldock_migrator IN SCHEMA public
  GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO modeldock_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE modeldock_migrator IN SCHEMA public
  GRANT USAGE,SELECT ON SEQUENCES TO modeldock_runtime;
