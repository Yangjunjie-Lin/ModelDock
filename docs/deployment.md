# Deployment

Supplier settlement workers are disabled economically until an administrator enables an approved supplier policy and a configured payout adapter. Keep `RELAYDOCK_PAYOUT_SANDBOX_ENABLED=false` in production. Configure the poll interval, bounded batch size, and explicit ISO region list as documented in [supplier-settlement.md](supplier-settlement.md#operations), and alert on every payout completion/failure log class without logging payout destinations.

For the public Beta multi-replica architecture, external PostgreSQL/Redis,
PITR and restore evidence, see [public-beta-operations.md](public-beta-operations.md)
and the Kubernetes manifest under `deploy/kubernetes/`.

Provider commercial governance is additive migrations 14 and 15. After upgrade,
review every Provider before setting `COMMERCIAL_APPROVED`; unreviewed legacy
rows fail closed as `CONTRACT_PENDING`. Exercise the emergency kill switch and
verify that a new request creates no Provider attempt. Configure regions,
organization policy, data-processing regions, exact-decimal limits, current
margin, and BYOK service fees before production use. See
[provider-governance.md](provider-governance.md) for rollback evidence.

The provided Compose topology is intended for a single-node ModelDock V3 deployment and
Windows-friendly local development. If the repository lives at
`D:\RelayDock`, its durable bind mounts resolve to:

```text
D:\RelayDock\data\postgres
D:\RelayDock\data\redis
D:\RelayDock\logs
```

No Docker-managed named volume is used for project data.

## Prerequisites

- Docker Engine/Desktop 24 or newer
- Docker Compose v2
- 4 GB available memory for the full stack
- Git; Go 1.26.6 and Node.js 22.22.3 only for host-side development
- at least one administrator-authorized provider API credential for real traffic

## First start

From PowerShell:

```powershell
Set-Location D:\RelayDock
Copy-Item .env.example .env
.\deploy\scripts\prepare-data.ps1
```

Generate independent secrets as described in [security.md](security.md), edit
`.env`, and replace every `CHANGE_ME` value. In particular, make the password
embedded in `DATABASE_URL` match `POSTGRES_PASSWORD`, and make the password
embedded in `REDIS_URL` match `REDIS_PASSWORD` (URL-encode special characters).

Validate before building:

```powershell
docker compose --env-file .env config --quiet
docker compose --env-file .env build
docker compose --env-file .env up -d
docker compose --env-file .env ps
```

Default loopback URLs:

| Service | URL |
| --- | --- |
| Gateway | `http://127.0.0.1:8080` |
| Control-plane API | `http://127.0.0.1:8081` |
| Admin web | `http://127.0.0.1:3000` |
| User console | `http://127.0.0.1:3001` |
| PostgreSQL | `127.0.0.1:5433` (container `5432`) |
| Redis | `127.0.0.1:6379` |

PostgreSQL and Redis host ports are present for local diagnostics only. Public
Beta/Kubernetes deployments use external managed services and no application
host-port bindings. Remove
their `ports` entries or block them with the host firewall in production.
Compose attaches those services to a separate `relaydock-diagnostics` bridge
so Docker Desktop can publish the loopback ports while the application traffic
continues over the egress-restricted `relaydock-internal` network.

## Initial administrator

`RELAYDOCK_ADMIN_EMAIL`, `RELAYDOCK_ADMIN_PASSWORD`, and
`RELAYDOCK_ADMIN_DISPLAY_NAME` provide the initial administrator identity.
There is no compiled-in or Compose default password.
At first successful database initialization, open the admin web application and
sign in with the values you set. After the first administrator exists, the
bootstrap password may be removed from the runtime environment; changing it
does not rotate the stored password. Enroll TOTP from Admin Settings, verify a
second administrator can sign in, and then enforce
`RELAYDOCK_ADMIN_MFA_REQUIRED=true`. Password changes and resets revoke prior
refresh sessions.

## Add the first provider credential

1. Create an API/project credential in the provider's official platform.
2. In Admin → Providers, keep the OpenAI base URL at
   `https://api.openai.com/v1`.
3. In Admin → Credentials, choose **Add credential**, enter the official key
   and optional organization/project metadata, then select **Validate & save**.
4. Put the credential in a credential group, synchronize/configure model
   metadata and pricing, then create a physical route such as `gpt-default`.
5. Optionally create a Routing Rule or use `auto:cost`, `auto:quality`, or
   `auto:balanced` after granting candidate routes to the project.
6. In the user console, create a ModelDock API key. Copy the full key at the
   one-time display and store it in the client secret manager.

Never paste a consumer ChatGPT cookie/session, account password, or a credential
obtained by automated registration. RelayDock accepts official API credentials
only.

## Health and logs

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz
Invoke-RestMethod http://127.0.0.1:8081/healthz
docker compose --env-file .env logs -f --tail 200 relaydock
```

`healthz` reports process liveness. `readyz` should fail when required database
or coordination dependencies are unavailable. Compose waits for PostgreSQL,
Redis, and both RelayDock listeners before starting the web services.

Application logs are written to `D:\RelayDock\logs` and Docker's container log
stream. Configure rotation for both. Logs are structured and secret-redacted;
do not enable prompt content logging in normal operation.

The governance worker processes privacy lifecycle jobs and redacts expired
report/risk signal data on every replica. It is idempotent and uses row locks,
but operators must monitor `governance_lifecycle_failed` and
`governance_cleanup_failed`, keep `RELAYDOCK_GOVERNANCE_CLEANUP_INTERVAL` at a
reviewed value, and retain tested backups. Legal holds block destructive jobs
and cleanup. Do not replace these controls with an unreviewed database cron job.

## Linux bind-mount permissions

The containers use non-root service users. On Linux, create the bind directories
before startup and ensure the PostgreSQL, Redis, and RelayDock container users
can write only their respective paths. Determine image UIDs for the exact image
tags in your deployment rather than assuming a UID, then apply narrowly scoped
ownership/ACLs. Do not make runtime directories world-writable.

## Production reverse proxy

Terminate TLS at a trusted reverse proxy/load balancer and route separately:

- public API hostname → gateway `8080`;
- control-plane API hostname → `8081`, restricted by identity-aware access or
  an administrative network where appropriate;
- admin hostname → `3000`;
- console hostname → `3001`.

Set real HTTPS origins in `ALLOWED_ORIGINS` and set `COOKIE_SECURE=true`.
The bundled web Nginx keeps `/api/*` and `/v1/*` same-origin and proxies them to
the two RelayDock listeners, so its CSP can retain `connect-src 'self'`.
Keep PostgreSQL,
Redis, the mock profile, and Docker's control socket off public networks.

The production package supports a separate public website hostname through
`RELAYDOCK_PUBLIC_SITE_DOMAIN`. Keep the existing `API_DOMAIN` for backward-
compatible `/v1`, health, and signed payment Webhook ingress. The public-site
hostname allowlists only:

```text
Console static assets and SPA routes
/api/public/*
/api/console/*
/v1/*
```

All other `/api/*`, including `/api/admin/*` and the shared authentication
realm, return 404 at the outer Nginx and cannot fall through to the Console
container's development proxy. The Console container is a default production
service but remains loopback-bound; only the edge Nginx is public. Set
`RELAYDOCK_PUBLIC_CONSOLE_URL=https://<public-site-host>` and include that exact
Origin in `ALLOWED_ORIGINS`. The API and public-site names must be distinct and
both must resolve before the dual-name certificate is requested.

Set `RELAYDOCK_PUBLIC_SUPPORT_EMAIL` and
`RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL` to monitored public role mailboxes. They are
returned by `GET /api/public/config` and are therefore not secrets. The bundled
`support@example.invalid` and `enterprise@example.invalid` defaults cannot
receive mail and are launch-blocking placeholders, not working contact paths.

Forward the original client address only from explicitly trusted proxies; do
not trust arbitrary `X-Forwarded-For`. Configure request and stream timeouts so
the proxy does not buffer SSE (`proxy_buffering off` in Nginx-style proxies).

## Backup and restore

Do not copy a live PostgreSQL directory as the only backup strategy. Use
`pg_dump`/`pg_dumpall` or a physical backup tool with a consistency guarantee.
Back up:

- PostgreSQL logical/physical backup;
- the separate `RELAYDOCK_MASTER_KEY` escrow;
- deployment configuration without exposing secrets in ordinary archives;
- versioned migrations and the image/version identifier.

Redis data is operational state and is not authoritative, though retaining its
AOF can reduce cold-start disruption. Logs have their own retention policy.

Practice restoration into an isolated environment. Verify credential
decryption, login, route resolution, key revocation, and a mock-provider request
before declaring the backup usable.

## Upgrade and rollback

1. Read release notes and back up PostgreSQL plus master-key escrow.
2. Pull/build the target images and run `docker compose config --quiet`.
   Production Compose keeps the legacy `relaydock/*` image defaults; for a
   ModelDock registry release set `MODELDOCK_SERVER_IMAGE`,
   `MODELDOCK_ADMIN_WEB_IMAGE`, and `MODELDOCK_CONSOLE_WEB_IMAGE` to the
   canonical repositories and set `RELAYDOCK_IMAGE_TAG` to the verified
   semantic tag. Prefer the digest recorded in the GitHub Release for the
   actual deployment.
3. Apply forward-compatible migrations with a bounded maintenance plan.
4. Replace application/web containers and monitor readiness/error rate.
5. Roll back application images only when the database schema remains
   compatible; database rollback requires an explicit tested restore plan.

ModelDock uses embedded migrations `1:core`, `2:v2`, `3:v2_statuses`,
`4:project_route_soft_delete`, `5:openai_compatible_providers`, and
`6:modeldock`, `7:accounts`, `8:pricing`, and later forward migrations through
`19:public_commercial_onboarding`. Do not edit
an already-applied migration: add a new version instead. After an upgrade,
verify the migration ledger and exercise the V2 input-to-output suite from the
repository root against an isolated test stack:

```powershell
.\tests\integration\verify-migrations.ps1 -ConfirmIsolatedTestDatabase
.\tests\integration\verify-pricing.ps1 -ConfirmIsolatedTestDatabase
.\tests\integration\verify-payments.ps1
.\tests\integration\verify-v2.ps1 -ConfirmIsolatedTestDatabase
.\tests\integration\verify-marketplace-launch.ps1 -ConfirmIsolatedTestDatabase
```

The migration contract runner requires the local `relaydock-postgres-1`
Compose service, the internal `relaydock_relaydock-internal` bridge, `.env`, and
the already-built `relaydock/server:local` image. It creates and drops one
randomly named database, starts test containers without published ports, and
checks fresh application, idempotence, unknown-version rejection, and checksum
tamper rejection. It refuses remote Docker endpoints and will not run without
the explicit isolated-test confirmation switch.

Migration `8:pricing` is forward-only. After upgrade, verify the seven required
pricing tables, provider contract defaults, immutable-table triggers, and the
legacy `model_prices` seed copies. Replace the provider region upgrade default
`*` with reviewed contract regions before production pricing. A routine image
rollback must preserve ledger version 8 and its snapshots; see
[pricing.md](pricing.md) for the destructive rollback order. Migration 8 also
widens existing wallet and billing ledger amounts to `NUMERIC(30,12)`; its
`ALTER COLUMN TYPE` statements take table locks, so rehearse on a
production-sized copy and schedule a bounded writer maintenance window.

The suite creates disposable organizations/projects and verifies route
isolation, tag selection, budget admission/events, versioned key rotation,
signed Webhook delivery/dead-letter retry, CSV scope, status invalidation,
alert acknowledgement, and secret absence. It must use the deterministic mock
provider and a test database; it is not intended to mutate production tenants.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `config` reports a missing variable | Copy `.env.example`, replace all `CHANGE_ME` values, and verify URL-encoded database/Redis passwords. |
| RelayDock exits immediately | Confirm the master key decodes to exactly 32 bytes and HMAC/JWT secrets contain at least 32 bytes. |
| PostgreSQL/Redis is unhealthy | Inspect service logs and bind-directory permissions; ensure passwords in URLs match service passwords. |
| Web UI loads but calls fail | Check build-time API URLs, exact CORS origins, browser mixed-content rules, and control-plane health. |
| Backend build cannot reach the Go module proxy | Set `GO_MODULE_PROXY` to an organization-approved mirror, then rebuild; do not disable checksum verification. |
| SSE arrives all at once | Disable reverse-proxy buffering and compression that coalesces events. |
| Credential validation returns `401` | Verify/reissue it in the official provider dashboard; do not rotate accounts/proxies around the failure. |
| Upstream `429` | Observe cooldown/reset and reduce/admit traffic within limits; do not attempt limit evasion. |
| Provider quality probes do not run | Set `RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION` to this replica's real egress region, enable the admin policy, and verify contract/region plus a platform-owned credential. |
| Provider quality circuit is open | Inspect immutable observations and SLA events; fix the cause, wait for half-open recovery, or use an audited half-open reset. Never force-close without measured recovery. |
| SDK test skips | Export the four `RELAYDOCK_*` test variables and create matching routes first. |

Stop without deleting data:

```powershell
docker compose --env-file .env down --remove-orphans
```

Deleting `data/postgres` is destructive and is intentionally not part of the
Makefile or routine deployment scripts.
### Funding reservation operations

Before admitting production traffic after migration 0009, run the isolated
empty/upgrade migration verification and `tests/integration/verify-funding.ps1
-ConfirmIsolatedTestDatabase`. Ensure `RELAYDOCK_FUNDING_STALE_AFTER` is longer
than `RELAYDOCK_PROVIDER_TIMEOUT`. Monitor stale RESERVED/PENDING operations,
wallets at their risk threshold, and the funding recovery error log. Detailed
replay, rollback, and incident procedures are in
[funding-ledger.md](funding-ledger.md).

### Provider quality worker

Scheduled probes are disabled when `RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION` is
empty. Set it only to the actual egress location and deploy one worker in every
policy-required region. Enabling a policy can create small billable synthetic
requests; approve their model, interval, contract permission, and budget first.
See [provider-quality.md](provider-quality.md) for monitoring, incidents, and
rollback.

### Marketplace launch control

After migration V23, inspect all pre-existing `ACTIVE` listings that are linked
to suppliers. Their declaration is preserved for compatibility, but the
Gateway excludes them until a versioned canary or approved release exists. Keep
the related Provider disabled while collecting acceptance evidence.

Use the admin Marketplace page to open a review, evaluate the six foundation
gates, start a 1–2,000 bps canary, run the financial/lifecycle rehearsal,
attach drill reports, and obtain second-administrator approval. Alert on stale
reviews, emergency cutovers, open quality circuits, expired price/contract
evidence, and payout-readiness changes. Never enable a real payout adapter from
environment configuration alone; the supplier's four database reviews must
also be approved. Detailed commands and rollback are in
[marketplace/launch-acceptance.md](marketplace/launch-acceptance.md).

### Payment worker and secrets

Payment adapters are off by default. Enable only `sandbox` in isolated test
environments or `manual_transfer` after establishing an administrator evidence
review policy. Inject `RELAYDOCK_PAYMENT_SANDBOX_SECRET` through the deployment
secret manager or process environment; it is intentionally absent from the
database. Configure explicit allowed regions and maintain synchronized system
time because signed webhooks are rejected outside the configured skew.

Before enabling payment ingress, verify HTTPS termination, proxy body limits,
and that `/api/payments/webhooks/*` reaches the control listener without any
body transformation. Alert on payment recovery errors and reconcile provider
statements independently. See [payments.md](payments.md) for order recovery,
secret rotation, migration, and rollback.

### Subscription lifecycle worker

`RELAYDOCK_SUBSCRIPTION_POLL_INTERVAL` defaults to `1m`. The worker uses
database row locks and immutable idempotent events, so it may run on every
application replica. Monitor `subscription_lifecycle_failed`; do not shorten
the interval to compensate for a database incident. Subscription fee journals
are intentionally separate from recharge/wallet and Token usage journals. See
[subscriptions.md](subscriptions.md) for entitlement behavior, reconciliation,
migration 0011, and rollback.

### Observability profile

Create runtime directories, validate configuration, and start internal
monitoring with:

```powershell
.\deploy\scripts\prepare-data.ps1
docker compose --env-file .env --profile observability config --quiet
docker compose --env-file .env --profile observability up -d prometheus alertmanager otel-collector
```

Prometheus and Alertmanager bind to loopback. OTLP is internal-only. Set
`RELAYDOCK_OTEL_EXPORTER_OTLP_ENDPOINT` to the Collector service URL and enable
insecure transport only on that private Compose network. The bundled
Alertmanager has no external destination; mount an approved configuration with
notification credentials as a secret before treating alerts as paged. Never
commit webhook URLs or paging tokens. Setup, retention, rollback, and incident
rehearsals are in [observability-operations.md](observability-operations.md).

### Financial reconciliation worker

`RELAYDOCK_RECONCILIATION_INTERVAL` defaults to `15m` and
`RELAYDOCK_RECONCILIATION_RUN_AT` defaults to `02:00` UTC. Every replica may
wake the worker; the PostgreSQL daily run key admits only one run for the
previous UTC business date. UTC half-open timestamp bounds make the archive
independent of the database session time zone, and a failed check persists a
terminal `FAILED` run for alerting. Alert on `financial_reconciliation_failed`, open
critical/high cases, old unhandled cases, and wallet attribution gaps. Import
Provider statements only from contracted, enabled Providers and verify the
file SHA-256 out of band. See [financial-close.md](financial-close.md) for the
six checks, operator resolution rules, migrations 0012–0013, and rollback boundary.
