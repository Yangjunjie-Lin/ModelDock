# Public Beta Production Operations

This step adds the public-beta deployment boundary while preserving the
existing `/v1` contract, `rdk_*` keys, and `RELAYDOCK_*` names. The Go process
is stateless: PostgreSQL is the source of truth, Redis holds reconstructable
counters/leases, and no request state is written to the container filesystem.
Use the Kubernetes manifest in `deploy/kubernetes/public-beta.yaml` or the
production Compose model with an external managed PostgreSQL/Redis service.

## A. Current state and gaps addressed

The previous startup path ran migrations in every application replica and
exposed a single local database/Redis pair. It already had a PostgreSQL
advisory lock, bounded pools, Redis AOF/noeviction, readiness checks, CSRF,
webhook SSRF protection, and graceful `http.Server.Shutdown`. This step adds:

- a secret-manager interface with environment compatibility and a one-shot
  `relaydock migrate` command;
- migration `0018_beta_runtime_hardening` with immutable audit hash evidence;
- startup/readiness/liveness probes and an explicit draining state;
- Provider HTTPS/allowlist/private-IP enforcement at the dial boundary;
- bounded upstream stream responses, JWT previous-key rotation, rolling/canary
  deployment manifests, and a rollback command;
- encrypted/object-storage backup guidance, PITR requirements, and an isolated
  restore drill script.

## B. Database, Redis, and recovery design

Production PostgreSQL and Redis are independent managed services. Set
`DATABASE_URL` and `REDIS_URL` to private service endpoints; do not mount their
data directories into application pods. PostgreSQL uses the configured
`POSTGRES_MAX_CONNS`/`POSTGRES_MIN_CONNS` pool per replica. Size the managed
database `max_connections` for `replicas * max_conns + migration + monitoring`
and enable `log_min_duration_statement` (start at 500ms) plus `pg_stat_statements`
in the provider. Scrape pool saturation and slow-query logs into the existing
Prometheus/OTel pipeline.

Use separate database roles: `MIGRATION_DATABASE_URL` belongs only to the
one-shot DDL job, while `DATABASE_URL` uses the non-owner runtime role. The
grant template is `deploy/production/postgres/roles.sql`; login roles and
passwords are provisioned through database IAM/secret management, not SQL files.

Enable PostgreSQL continuous archiving (`wal_level=replica`, `archive_mode=on`,
encrypted WAL destination, retention at least 14 days) or the managed
provider's equivalent. A daily custom-format logical backup is created by
`deploy/production/scripts/backup-pitr.sh`; it is checksummed and optionally
uploaded to `BACKUP_OBJECT_URI=s3://...` using workload credentials and
server-side AES-256 encryption (or `BACKUP_SSE_KMS_KEY_ID` for KMS). The
provider's PITR restore is the authoritative recovery path; the logical dump
is a second recovery path for object-level mistakes.

Run the recovery drill at least monthly and after migration changes:

```bash
sudo bash deploy/production/scripts/backup-pitr.sh /opt/relaydock
sudo bash deploy/production/scripts/restore-drill.sh data/backups/modeldock-<timestamp>.dump
```

The drill starts a disposable PostgreSQL container on `--network none`, restores
the dump, checks the migration ledger and audit hash columns, then removes only
the generated container. For PITR drills, restore a managed snapshot to an
isolated project/VPC, apply WAL to a stated timestamp, run the same checks, and
record RPO/RTO evidence without pointing production at the clone.

Redis uses AOF with `appendfsync everysec` and `noeviction` when self-hosted;
managed Redis should use multi-AZ replicas, encrypted persistence, and an
automated failover policy. Redis state is intentionally reconstructable. If
Redis is unavailable, rate limiting, credential concurrency, and routing fail
closed with a documented 503/429; PostgreSQL billing and audit writes remain
authoritative. Redis locks/counters use atomic Lua operations with expirations;
no lock is treated as an authorization decision, and stale leases are bounded
by the provider timeout.

## C. Release and rollback

Run the migration Job once, wait for completion, then roll the stateless
Deployment with `maxUnavailable: 0`, `maxSurge: 1`, a PodDisruptionBudget, and
immutable image digests. `startupz` gates slow initialization, `readyz` removes
an unhealthy or draining replica from the load balancer, and `healthz` is only
process liveness. A pre-stop delay allows endpoint removal to propagate;
`RELAYDOCK_DRAIN_DELAY` defaults to 10-15 seconds and `SHUTDOWN_TIMEOUT` defaults
to 30-90 seconds. `http.Server.Shutdown` waits for active streaming handlers;
the gateway does not use a write deadline that would truncate an SSE response.
The gateway caps upstream response bytes with `RELAYDOCK_STREAM_MAX_BYTES`.

For Compose/Swarm, `deploy_config` declares start-first rolling updates and
automatic rollback. Use the canary helper to verify one candidate replica and
`rollback.sh` with the last immutable tag for a one-command application
rollback. Database migrations are forward-only: rollback is safe only when the
previous binary understands the new schema, otherwise restore an isolated
database snapshot and complete a controlled data migration.

## D. Security controls

Provider URLs must be HTTPS and match `RELAYDOCK_PROVIDER_ALLOWED_HOSTS`; the
runtime dialer rejects loopback, link-local, multicast, unspecified, and private
IPs and never inherits `HTTP_PROXY`. Webhooks retain their exact-body HMAC,
timestamp replay window, redirect rejection, DNS-rebinding-safe dialer, and
private-network block. NGINX/Kubernetes limits request bodies to 10 MiB and the
application enforces `MAX_REQUEST_BODY_BYTES` plus the stream cap. CORS remains
an explicit origin list, cookie sessions are Secure/HttpOnly/SameSite=Strict in
production, and mutating cookie requests require the double-submit CSRF token.

JWT rotation sets `RELAYDOCK_JWT_SECRET` to the new key and temporarily keeps
the old key in `RELAYDOCK_JWT_PREVIOUS_SECRET`; newly issued tokens always use
the new key. API key rotation remains versioned and display-once. Admin MFA is
required in production and all administrative actions are audited. Migration
0018 seals new audit rows with a serialized SHA-256 chain and rejects mutation.

CI already runs dependency audit, `govulncheck`, container scanning, SBOM
generation, gitleaks, and Compose validation. Add SAST results to the same
required checks before enabling public signups. Never put real Provider keys,
payment secrets, or user prompts in code, fixtures, logs, or screenshots.

## E. Beta acceptance evidence

Record the commands and outputs for: `docker compose ... config --quiet`,
`gofmt -l`, `go vet ./...`, `go test ./...`, both frontend `npm ci` and checks,
empty and populated migrations, a restore drill, and high-risk security tests.
Stop one application replica and verify `/v1/models` and an active SSE request
continue through the other replica. A release is blocked by any unexplained
failure in authorization, tenant isolation, key enumeration/collision,
webhook replay, wallet double-spend, SSRF, open redirect, malicious Provider
header, oversized stream, or admin-session-hijack tests.
