# Deployment

The provided Compose topology is intended for a single-node V2.0 deployment and
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
- Git; Go 1.24+ and Node.js 22+ only for host-side development
- an administrator-authorized OpenAI API/project credential for real traffic

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

PostgreSQL and Redis host ports are present for local diagnostics only. Remove
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
does not rotate the stored password. V2.0 has no self-service password-change
endpoint, so document and restrict an explicit database recovery procedure.

## Add the first OpenAI credential

1. Create an API/project credential in the official OpenAI platform dashboard.
2. In Admin → Providers, keep the OpenAI base URL at
   `https://api.openai.com/v1`.
3. In Admin → Credentials, choose **Add credential**, enter the official key
   and optional organization/project metadata, then select **Validate & save**.
4. Put the credential in a credential group, synchronize/configure model
   metadata, and create a route such as `gpt-default`.
5. In the user console, create a RelayDock API key. Copy the full key at the
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

V2.0 stores request/audit retention values but does not run an automatic cleanup
worker. Apply those values with an operator-managed, tested PostgreSQL retention
job and backup policy.

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
3. Apply forward-compatible migrations with a bounded maintenance plan.
4. Replace application/web containers and monitor readiness/error rate.
5. Roll back application images only when the database schema remains
   compatible; database rollback requires an explicit tested restore plan.

V2.0 uses embedded migrations `1:core`, `2:v2`, `3:v2_statuses`,
`4:project_route_soft_delete`, and `5:openai_compatible_providers`. Do not edit
an already-applied migration: add a new version instead. After an upgrade,
verify the migration ledger and exercise the V2 input-to-output suite from the
repository root against an isolated test stack:

```powershell
.\tests\integration\verify-migrations.ps1 -ConfirmIsolatedTestDatabase
.\tests\integration\verify-v2.ps1 -ConfirmIsolatedTestDatabase
```

The migration contract runner requires the local `relaydock-postgres-1`
Compose service, the internal `relaydock_relaydock-internal` bridge, `.env`, and
the already-built `relaydock/server:local` image. It creates and drops one
randomly named database, starts test containers without published ports, and
checks fresh application, idempotence, unknown-version rejection, and checksum
tamper rejection. It refuses remote Docker endpoints and will not run without
the explicit isolated-test confirmation switch.

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
| SDK test skips | Export the four `RELAYDOCK_*` test variables and create matching routes first. |

Stop without deleting data:

```powershell
docker compose --env-file .env down --remove-orphans
```

Deleting `data/postgres` is destructive and is intentionally not part of the
Makefile or routine deployment scripts.
