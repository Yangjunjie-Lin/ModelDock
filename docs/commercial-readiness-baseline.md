# ModelDock commercial-readiness baseline

Audit date: 2026-08-10 (Asia/Shanghai)  
Audited revision before the baseline-only changes: `8bf353f` (`main`)  
Scope: reproducible commercial baseline audit only; no business feature was added.

## A. 当前状态与缺口

### 结论

ModelDock is a working single-node gateway/control-plane baseline. The local,
deterministic provider stack verified authentication, tenant provisioning,
project API keys, normal and streaming OpenAI-compatible requests, pre-dispatch
credential-group fallback, scoped usage, wallet charges, Webhooks, rotation,
and the official Python SDK. It is **not commercially ready** because the
required request-to-audit trace is absent, money is converted to `float64` in
the Go/API layer, provider contract/region governance is absent, the
machine-readable API contract omits 27 live operations, and production frontend
dependencies have known vulnerabilities.

### API endpoint inventory

Gin runtime introspection found **178 route registrations**: 8 on the gateway
listener and 170 on the control listener. Because four operational routes are
registered on both listeners, there are **174 unique method/path operations**.
The grouping below is exhaustive; `{admin|console}` means that both concrete
paths are registered.

Operational routes on both listeners:

```text
GET /healthz
GET /readyz
GET /metrics
GET /api/version
```

Gateway/data plane:

```text
GET  /v1/models
POST /v1/responses
POST /v1/chat/completions
POST /v1/embeddings
```

Control-plane authentication:

```text
POST /api/auth/login
POST /api/admin/auth/login
POST /api/console/auth/login
POST /api/auth/refresh
POST /api/admin/auth/refresh
POST /api/console/auth/refresh
GET  /api/auth/me
POST /api/auth/logout
POST /api/admin/auth/logout
POST /api/console/auth/logout
```

Administrator-only control plane:

```text
GET  /api/admin/dashboard

GET  /api/admin/cockpit/accounts
POST /api/admin/cockpit/refresh
POST /api/admin/cockpit/test

GET  /api/admin/providers
POST /api/admin/providers
PUT  /api/admin/providers/:id
DELETE /api/admin/providers/:id
POST /api/admin/providers/:id/sync-models

GET  /api/admin/marketplace/providers
POST /api/admin/marketplace/providers
PUT  /api/admin/marketplace/providers/:id
DELETE /api/admin/marketplace/providers/:id

GET  /api/admin/credentials
POST /api/admin/credentials
POST /api/admin/credentials/import
POST /api/admin/credentials/bulk
PUT  /api/admin/credentials/:id
DELETE /api/admin/credentials/:id
POST /api/admin/credentials/:id/test
PATCH /api/admin/credentials/:id/status
GET  /api/admin/credentials/:id/tags
PUT  /api/admin/credentials/:id/tags

GET  /api/admin/credential-groups
POST /api/admin/credential-groups
DELETE /api/admin/credential-groups/:id
PUT  /api/admin/credential-groups/:id/members/:credentialId
DELETE /api/admin/credential-groups/:id/members/:credentialId

GET  /api/admin/models
POST /api/admin/models
PUT  /api/admin/models/:id
DELETE /api/admin/models/:id
GET  /api/admin/models/:id/prices
POST /api/admin/models/:id/prices

GET  /api/admin/model-routes
POST /api/admin/model-routes
PUT  /api/admin/model-routes/:id
DELETE /api/admin/model-routes/:id
GET  /api/admin/routes
POST /api/admin/routes

GET  /api/admin/routing-rules
POST /api/admin/routing-rules
PUT  /api/admin/routing-rules/:id
DELETE /api/admin/routing-rules/:id

GET  /api/admin/teams
POST /api/admin/teams
PUT  /api/admin/teams/:id
DELETE /api/admin/teams/:id
GET  /api/admin/teams/:id/members
PUT  /api/admin/teams/:id/members/:userID
DELETE /api/admin/teams/:id/members/:userID

GET  /api/admin/wallets
PUT  /api/admin/wallets/:id
GET  /api/admin/wallets/:id/transactions
POST /api/admin/wallets/:id/topups
POST /api/admin/wallets/:id/adjustments

GET  /api/admin/api-keys
POST /api/admin/api-keys
PATCH /api/admin/api-keys/:id/status
DELETE /api/admin/api-keys/:id
POST /api/admin/api-keys/:keyID/rotate
POST /api/admin/api-keys/:keyID/finalize

GET  /api/admin/users
POST /api/admin/users
PATCH /api/admin/users/:id/status

GET  /api/admin/request-logs
GET  /api/admin/usage
GET  /api/admin/audit-logs
GET  /api/admin/alerts
POST /api/admin/alerts/:alertID/acknowledge
GET  /api/admin/settings
PUT  /api/admin/settings
```

Tenant/project suffixes registered under **both** `/api/admin` and
`/api/console`:

```text
GET  /organizations
POST /organizations
GET  /organizations/:organizationID
PUT  /organizations/:organizationID
DELETE /organizations/:organizationID
GET  /organizations/:organizationID/members
POST /organizations/:organizationID/members
PUT  /organizations/:organizationID/members/:userID
DELETE /organizations/:organizationID/members/:userID
GET  /organizations/:organizationID/projects
POST /organizations/:organizationID/projects

GET  /projects/:projectID
PUT  /projects/:projectID
DELETE /projects/:projectID
GET  /projects/:projectID/members
POST /projects/:projectID/members
PUT  /projects/:projectID/members/:userID
DELETE /projects/:projectID/members/:userID
GET  /projects/:projectID/routes
POST /projects/:projectID/routes
PUT  /projects/:projectID/routes/:routeID
DELETE /projects/:projectID/routes/:routeID
GET  /projects/:projectID/budgets
POST /projects/:projectID/budgets
PUT  /projects/:projectID/budgets/:policyID
DELETE /projects/:projectID/budgets/:policyID
GET  /projects/:projectID/budget-usage
GET  /projects/:projectID/budget-events
GET  /projects/:projectID/webhooks
POST /projects/:projectID/webhooks
PUT  /projects/:projectID/webhooks/:webhookID
DELETE /projects/:projectID/webhooks/:webhookID
POST /projects/:projectID/webhooks/:webhookID/test
GET  /projects/:projectID/webhook-deliveries
POST /projects/:projectID/webhook-deliveries/:deliveryID/retry
GET  /projects/:projectID/usage/export

POST /api-keys/:keyID/rotate
POST /api-keys/:keyID/finalize
```

Console-specific control plane:

```text
GET  /api/console/projects
GET  /api/console/overview
GET  /api/console/dashboard
GET  /api/console/api-keys
POST /api/console/api-keys
DELETE /api/console/api-keys/:id
GET  /api/console/models
GET  /api/console/usage
GET  /api/console/request-logs
GET  /api/console/logs
```

The current `docs/openapi.yaml` resolves to 167 operations over 180 path entries, including Provider commercial governance, bounded manual/API/CSV cost synchronization, kill switch, BYOK credentials, and append-only BYOK service-fee policy operations. The earlier baseline live
operations missing from OpenAPI are:

```text
GET /api/version
GET /api/admin/cockpit/accounts
POST /api/admin/cockpit/refresh
POST /api/admin/cockpit/test
GET|POST /api/admin/marketplace/providers
PUT|DELETE /api/admin/marketplace/providers/:id
POST /api/admin/models
PUT|DELETE /api/admin/models/:id
GET|POST /api/admin/routing-rules
PUT|DELETE /api/admin/routing-rules/:id
GET|POST /api/admin/teams
PUT|DELETE /api/admin/teams/:id
GET /api/admin/teams/:id/members
PUT|DELETE /api/admin/teams/:id/members/:userID
GET /api/admin/wallets
PUT /api/admin/wallets/:id
GET /api/admin/wallets/:id/transactions
POST /api/admin/wallets/:id/topups
POST /api/admin/wallets/:id/adjustments
```

### Database table inventory

The live empty-to-current schema contains 34 public tables:

```text
alerts                         api_key_versions
api_keys                       audit_logs
billing_usage_records          budget_events
credential_group_members       credential_groups
credential_tags                model_prices
model_routes                   models
organization_memberships       organizations
project_budget_policies        project_memberships
project_model_routes           projects
provider_credentials           provider_marketplace_listings
providers                      request_logs
routing_rules                  schema_migrations
system_settings                team_memberships
teams                          usage_daily
usage_hourly                   users
wallet_transactions            wallets
webhook_endpoints              webhook_outbox
```

### Background task inventory

- In-process Webhook worker: polls `webhook_outbox`, claims rows with a lease and
  claim token, signs deliveries, retries with bounded exponential backoff,
  dead-letters exhausted work, and expires abandoned leases.
- Startup migration runner: advisory-locks PostgreSQL and applies embedded
  migrations transactionally; this is startup work, not a continuous worker.
- Redis scripts: rate-limit counters, concurrency leases, cooldown coordination,
  and weighted-round-robin state are request-driven, not scheduled jobs.
- Production Nginx container: reload loop every six hours.
- Production Certbot container: renewal loop every twelve hours.
- Optional operator cron: `deploy/production/scripts/backup.sh`; it is not
  installed by the application itself.
- Missing: native request/usage/audit retention worker. README explicitly tells
  operators to provide an external scheduled job.

### Administrator page inventory

`/login`, `/` Dashboard, `/organizations`, `/projects`, `/teams`, `/providers`,
`/marketplace`, `/credentials`, `/groups`, `/models`, `/routes`,
`/routing-rules`, `/billing`, `/api-keys`, `/users`, `/usage`,
`/request-logs`, `/audit-logs`, `/alerts`, `/webhooks`, and `/settings`.

### User page inventory

`/login`, `/` Overview, `/api-keys`, `/models`, `/usage`, `/logs`,
`/playground`, and `/docs`.

### Provider adapter inventory

- `openai`: full shared OpenAI-compatible HTTP adapter.
- `openrouter`, `qwen`, `kimi`, `glm`: registry aliases to the same adapter.
- `deepseek`: distinct runtime type embedding the shared adapter.
- `anthropic`: distinct runtime type embedding the shared adapter and targeting
  Anthropic's OpenAI-compatible surface.
- `gemini`: distinct runtime type embedding the shared adapter and targeting
  Gemini's OpenAI-compatible surface.
- Seeded provider rows: OpenAI, Anthropic, Gemini, DeepSeek, Qwen, Kimi, GLM,
  and OpenRouter.

No real external Provider credential was used in this audit. Therefore the
external contracts, region availability, and each provider's endpoint-specific
behavior remain unverified.

### Route strategy inventory

- Exact/manual alias: project grant resolves directly to one physical route.
- Built-ins: `auto`, `auto:balanced`, `auto:cost`, `auto:quality`.
- Persisted intelligent rules: `balanced`, `cost_optimized`, and
  `quality_optimized`, with model type/provider/capability/price filters.
- Credential selection inside a group: `priority_weighted` (weighted
  least-load), `least_loaded`, and `weighted_round_robin` at top priority.
- Fallback: fallback group is used only when the primary group has no eligible
  credential and before any upstream attempt. There is no replay or provider
  switch after an attempt starts or after stream bytes are emitted.

### Wallet and ledger paths

```text
organization INSERT
  -> organizations_create_wallet_trigger
  -> wallets (POSTPAID migration default)

admin top-up/adjustment
  -> Idempotency-Key/body idempotency_key
  -> transaction + SELECT wallet FOR UPDATE
  -> wallet balance/version update
  -> wallet_transactions
  -> best-effort audit_logs write after commit

successful priced inference
  -> deferred InsertScopedRequestLog
  -> one PostgreSQL transaction containing:
       request_logs
       usage_daily
       usage_hourly
       billing_usage_records
       wallet balance/version update
       wallet_transactions(CHARGE, idempotency_key=usage:<request_id>)
       budget event and Webhook outbox when applicable
```

The commercial smoke proved the first three inference-ledger links for two
requests. It found no `audit_logs` row carrying either inference request ID.

### Test inventory

There are 53 Go `Test*` functions:

- `internal/auth/auth_test.go`: password round trip; legacy bcrypt.
- `internal/cockpit/client_test.go`: sanitized snapshot; secret-field rejection;
  sidecar bearer/no-secret; missing-config fail closed.
- `internal/providers/provider_test.go`: case-insensitive registry.
- `internal/server/gateway_test.go`: Responses SSE usage; Chat usage; bounded
  tail capture; OpenAI error envelope; client request ID.
- `internal/server/middleware_test.go`: allowed/rejected CORS; three cookie/CSRF
  cases; bearer CSRF exemption.
- `internal/server/provider_test.go`: provider-type normalization.
- `internal/server/v2_console_test.go`: UTC date range, inclusive counts,
  defaults/clamps, 24 hourly buckets, log redaction.
- `internal/server/v2_test.go`: frontend route contract; admin-only credential
  tags; stable fail-closed tag merging; budget boundaries; query-range bounds;
  tenant slug validation.
- `internal/store/intelligent_router_test.go`: all selection strategies;
  constraints; cost strategy requires price.
- `internal/store/v2_usage_test.go`: CSV formula neutralization.
- `internal/webhook/dispatcher_test.go`: exact-body signature/no redirects;
  unsafe-target rejection.
- `internal/webhook/worker_test.go`: successful delivery identity; bounded retry;
  bounded delay.
- `tests/backend/crypto_key_test.go`: vault binding; serialization redaction;
  one-way/environment-scoped API keys; JWT/password lifecycle; bcrypt upgrade.
- `tests/backend/openai_adapter_test.go`: constructed credential headers;
  model/stream buffering behavior; cancellation.
- `tests/backend/scheduler_test.go`: deterministic explainable capacity safety;
  least-load; Redis fail-closed; capacity skip; tag constraints; HTTP lifecycle.

Executable integration suites:

- `verify-migrations.ps1`: empty schema, idempotent second startup, unknown
  version rejection, checksum tamper rejection.
- `verify.ps1`: login, authorized credential import/validation, model sync,
  ordinary Responses call, permissions, limits, provider invariants, 401
  credential lifecycle, log/cost write, secret scan.
- `verify-v2.ps1`: tenants, memberships/RBAC, project isolation, route/tags,
  budgets, API-key rotation/grace, Webhooks/retry/dead letter, CSV, status
  invalidation, alert acknowledgement, secret scan.
- `commercial-baseline-smoke.ps1`: full provisioning, ordinary/SSE calls,
  empty-primary fallback, wallet sequential/concurrent idempotency, direct
  request/billing/wallet/audit trace, and optional SDK suite.
- Python SDK: models, Responses normal/SSE, Chat normal/SSE, embeddings (6 tests).

There are no frontend unit, component, accessibility, or browser E2E tests, and
neither frontend defines a lint script.

### Deployment component inventory

Development Compose:

- PostgreSQL 17 with bind persistence and loopback diagnostic port.
- Redis 7.4 with AOF, password, bind persistence, internal network.
- RelayDock Go binary with gateway/control listeners, read-only root, non-root
  UID, dropped capabilities, log and sanitized Cockpit mounts.
- Admin Nginx SPA and Console Nginx SPA, both non-root/read-only.
- Optional `mock-openai` profile, loopback-only, non-root/read-only, deterministic
  inference/fault/Webhook test server.

Production Compose:

- PostgreSQL, Redis, RelayDock, edge Nginx, Certbot, and optional `control-ui`
  Admin/Console services; resource limits and bounded Docker logs are set.
- Deployment scripts cover Ubuntu bootstrap, host preparation, environment
  generation, deploy, HTTPS enablement, and backup.
- A systemd compose service is supplied as a recovery wrapper.
- No HA, orchestration, managed secret-store integration, or Kubernetes assets.

### 已实现且通过验证

- Empty database migrations 1–6 and exact migration checksums.
- V1 schema plus legacy user upgraded to V6; user, Legacy memberships, and
  wallet were preserved/created.
- Current and truly fresh six-service Compose builds and starts.
- Admin and console login, admin user creation, organization/project creation,
  memberships, project routes, and project-scoped one-time `rdk_test_*` key.
- Provider/credential/model/price setup against the isolated mock.
- `/v1/models`, Responses normal/SSE, Chat normal/SSE, and embeddings through
  the official OpenAI Python SDK.
- Usage/log write, billing usage row, wallet charge, sequential idempotency, and
  eight concurrent top-ups sharing one idempotency key producing one ledger row.
- Primary group empty -> fallback-group credential selected before dispatch.
- V2 tenant isolation, budget, rotation, Webhook, CSV, and status controls.
- Go formatting, vet, unit/backend tests; both frontend typechecks and builds.
- Development/mock and production Compose configuration rendering.

### 已实现但存在缺陷

1. Inference request IDs do not appear in `audit_logs`; only control-plane
   mutations are audited.
2. `Store.Audit` ignores database errors, runs after the business commit, and is
   not transactional with wallet top-ups or other mutations.
3. Monetary database columns use `numeric(20,8)`, but Go structs, JSON inputs,
   route scoring/costs, SQL scans, wallet arithmetic, and quota comparisons use
   `float64`/`::float8`. This violates the no-floating-money requirement.
4. Request/billing settlement is deferred until after upstream response bytes
   are sent. A ledger failure is logged but cannot revoke an already successful
   client response.
5. Prepaid admission checks only whether balance plus credit is positive; there
   is no reservation for estimated/final cost. Concurrent requests and streams
   can overdraw.
6. Some mutations are not audited (for example group-member removal and model
   synchronization), so the documented “all mutations audited” boundary is not
   complete.
7. The first fresh Compose run exposed a PostgreSQL health race. The baseline
   fix now checks TCP `127.0.0.1:5432`; the fresh rerun exited 0.
8. OpenAPI omits 27 live V3/operational operations.
9. Both frontend production dependency graphs have two moderate React Router
   vulnerabilities; the full development graph additionally has three
   moderate and one high total findings including Vite/esbuild.

### README 声明但代码未闭环

- “complete endpoint inventory” / machine-readable contract: 27 runtime
  operations are absent from OpenAPI.
- “admin and user mutations are audited”: audit coverage is incomplete and
  writes are best-effort rather than transactional.
- Security guide says login and key-creation endpoints require dedicated rate
  limits; no dedicated control-plane limiter is wired.
- Security guide says upstream redirects must not be followed to arbitrary
  hosts with authorization; the shared `http.Client` does not set a
  `CheckRedirect` policy.
- Refresh is described as rotation, but refresh JWTs are stateless and there is
  no server-side reuse/revocation record; a copied prior refresh token remains
  usable until expiry.
- `LOG_PROMPT_CONTENT` is documented and forced false in Compose, but is not
  loaded by `internal/config` and has no code path. Prompt content is currently
  not stored, so the default outcome is safe but the switch is inert.
- Provider capability/endpoint metadata is administrator supplied and not
  validated against real external providers in this baseline.

### 完全未实现

- Provider `contract_status` and explicit allowed-region policy/enforcement.
- Payment processor, payment reconciliation, tax, invoices, refunds tied to an
  external settlement system, and public supplier onboarding/settlement.
- Native retention/deletion worker.
- Password change/reset/recovery API, MFA, SSO, and server-side session
  revocation list.
- HA, Kubernetes, distributed wallet reservations, and multi-node commercial
  billing authorization.
- Frontend lint, unit/component, accessibility, and browser E2E suites.
- Database down migrations; rollback is backup/restore plus compatible image
  rollback only.
- Project license file.

### 安全问题

- High: money crosses a floating-point boundary.
- High: inference charge/audit evidence is incomplete and audit failures are
  swallowed.
- High: Provider contract status and allowed-region enforcement are absent.
- Medium: refresh-token replay/revocation is not controlled server-side.
- Medium: no explicit upstream redirect policy in the Provider HTTP client.
- Medium: no dedicated login/key-creation abuse limiter.
- Medium: two known production React Router dependency vulnerabilities in each
  SPA.
- Low/operational: images use release tags rather than immutable digests; no
  SBOM/signing pipeline is present.

Positive controls verified: AES-GCM credential encryption, HMAC-only downstream
key storage, one-time secrets, cookie/CSRF realm isolation, secret redaction,
Webhook SSRF/redirect controls, non-root/read-only containers, loopback port
defaults, and no account/CAPTCHA/proxy-evasion capability.

### 商业化阻断项

1. Add transactional, attributable inference audit evidence keyed by request ID
   without logging prompts or secrets.
2. Replace every monetary `float64` boundary with decimal/minor-unit types and
   add precision/concurrency/property tests.
3. Add Provider contract state, allowed regions, enforcement, migration,
   admin UI, API/OpenAPI, audit, and tests.
4. Reconcile all runtime routes into OpenAPI and enforce drift in CI.
5. Resolve production frontend dependency advisories and add lint/browser test
   gates.
6. Select a license before distribution or commercial customer delivery.
7. Define failure handling for successful responses whose deferred ledger
   transaction fails; prepaid commercial use requires reservation/reconciliation.

### 测试失败项

- `npm run lint` fails in both SPAs: no `lint` script.
- `npm audit --omit=dev` exits 1 in both SPAs: 2 moderate production findings.
- Full `npm audit` exits 1 in both SPAs: 3 moderate + 1 high findings.
- First `verify-v2.ps1` attempt failed before business assertions because its
  default mock token misspelled `relaydock`; the baseline fix corrected it and
  the rerun passed.
- First commercial smoke attempt had an incomplete PowerShell SQL argument; the
  new test was corrected before its final run.
- Final commercial smoke intentionally exits 1 because both tested inference
  request IDs have `AuditLog=0`. All other smoke assertions and 6 SDK tests pass.
- First truly fresh Compose attempt returned nonzero because PostgreSQL's
  socket-only health check raced TCP readiness. After the TCP healthcheck fix,
  a second fresh directory started all six services with exit 0.
- An initial ad-hoc Go container invocation used an Alpine login shell that
  reset `PATH`; rerunning with the official image's normal non-login shell made
  `gofmt`, `go vet`, and `go test` all pass. This was not a repository failure.

## B. 架构和数据模型设计

This step made no business/data-model change. The retained architecture is a
modular Go binary with two listeners, PostgreSQL system of record, Redis
coordination, and two React SPAs. The critical commercial transaction boundary
is `InsertScopedRequestLog`: request log, aggregate usage, billing usage, wallet
charge, budget event, and Webhook outbox are one PostgreSQL transaction.

The missing boundary is audit: inference does not create an audit row, and
control-plane audit calls are separate best-effort inserts. A later phase must
design an append-only audit event keyed by `request_id`, with explicit actor/key,
organization/project, action/result, timestamps, and no prompt/provider secret.
That is intentionally not implemented in this audit step.

## C. 将修改的文件

- `.env.example`: documented the isolated mock profile variables.
- `docker-compose.yml`: restored the missing test-only mock service and changed
  PostgreSQL readiness to TCP.
- `docker-compose.production.yml`: changed PostgreSQL readiness to TCP.
- `deploy/mock-openai/Dockerfile`, `Dockerfile.dockerignore`, `server.py`:
  restored deterministic test infrastructure required by existing docs/tests.
- `tests/integration/verify-v2.ps1`: corrected the mock control-token typo.
- `tests/integration/commercial-baseline-smoke.ps1`: new executable commercial
  trace/fallback/wallet/SDK smoke test.
- `docs/commercial-readiness-baseline.md`: this report.

No `internal/`, `apps/`, migration SQL, API behavior, or database column was
changed.

## D. 分阶段实施

1. Read and cross-referenced README, docs, deploy assets, all migrations,
   internal packages, both apps, tests, OpenAPI, Compose, auth, providers,
   routing, wallet/usage/audit, and Git state.
2. Introspected live Gin routes and PostgreSQL tables; inventoried pages,
   adapters, policies, workers, tests, and deploy components.
3. Ran empty and V1-upgrade migration contracts.
4. Ran Go, frontend, Compose, dependency, integration, V2, SDK, and commercial
   smoke checks.
5. Restored only missing test infrastructure and fixed only test/startup
   blockers; reran failures after each fix.
6. Started both the existing local stack and an isolated, genuinely fresh
   six-service stack with separate data directories and ports.
7. Recorded pass/fail evidence here; stopped the temporary fresh stack.

## E. 数据迁移及回滚方案

No migration was added or edited.

Verified paths:

- Empty database: migrations `1:core` through `6:modeldock`, exact ordered
  ledger, second-start idempotence, unknown-version rejection, checksum-tamper
  rejection.
- Existing V1 database: applied only `0001_core.sql`, inserted a legacy user and
  correct version-1 checksum, then started the current image. Versions 2–6 were
  applied, the user remained, both Legacy memberships existed, and the Legacy
  organization wallet existed.

Existing rollback policy is forward-only: never edit applied SQL. Roll back an
application image only when it remains schema compatible. Database rollback
requires stopping writers and restoring a tested PostgreSQL backup together
with the matching master-key escrow. There are no down scripts.

Rollback for this audit-only patch requires no database action: remove the
mock profile/files and smoke test, restore the prior healthcheck/token lines,
and rebuild. Doing so reintroduces the documented integration/startup failures.

## F. 测试结果

| Command / suite | Result |
| --- | --- |
| `gofmt -l cmd internal migrations tests` in `golang:1.24-alpine` | PASS, no files listed |
| `go vet ./...` | PASS |
| `go test -count=1 ./...` | PASS, all packages |
| Admin `npm ci` | PASS; audit reported 4 vulnerabilities |
| Console `npm ci` | PASS; audit reported 4 vulnerabilities |
| Both `npx tsc -b --pretty false` | PASS |
| Both `npm run build` | PASS |
| Both `npm run lint` | FAIL, missing script |
| Both `npm audit --omit=dev` | FAIL, 2 moderate each |
| Both full `npm audit` | FAIL, 3 moderate + 1 high each |
| Development Compose config, with and without mock profile | PASS |
| Production Compose config with production example environment | PASS |
| `verify-migrations.ps1` | PASS, 4 migration assertions |
| Manual V1 fixture upgrade | PASS: `1:core,...,6:modeldock|1|1|1|1` |
| `verify.ps1` | PASS |
| `verify-v2.ps1` final run | PASS |
| Official Python SDK suite in isolated venv | PASS, 6 tests |
| Existing full six-service Compose | PASS, all healthy |
| Fresh full six-service Compose after TCP health fix | PASS, first `up` exit 0, all healthy |
| Commercial smoke final run | FAIL only on audit trace; all other assertions PASS |

Final fresh-stack trace evidence:

```text
normal request rd_req_59ab345639375217dc9d185b:
  request_logs=1 billing_usage_records=1 wallet_transactions=1 audit_logs=0
stream request rd_req_67f96d9d0358809cc7dd46b1:
  request_logs=1 billing_usage_records=1 wallet_transactions=1 audit_logs=0
concurrent same-key topups: 8 x HTTP 201, wallet ledger rows=1
```

The mock is a real running HTTP service inside Compose, but it is not evidence
that any external paid Provider was reachable or contractually approved.

## G. 安全检查

- No real API key, payment key, cookie, password, database URL, or personal data
  was added to source, docs, logs, examples, or tests.
- The restored mock values are clearly test-only and accepted only by the
  optional local profile.
- Runtime secret outputs were neither printed nor persisted in tracked files;
  SDK keys remained process-local.
- Secret-bearing API responses were scanned by both integration suites.
- No account registration automation, verification bypass, proxy evasion,
  geographic bypass, credential sale, or Provider-term evasion was added.
- The audit confirms a prompt/response-content logging default that does not
  persist content, but the documented toggle is inert.
- The unresolved security issues and dependency advisories are listed in A and
  remain release blockers.

## H. 剩余风险

- Audit trace acceptance is not met.
- No real Provider, payment, TLS certificate issuance, restore drill, or
  production DNS flow was executed.
- API-based login/UI workflows were verified, and SPAs were built/served, but
  no browser automation validated visual/admin form behavior.
- The isolated fresh Compose data directories remain under ignored `.cache`
  because recursive deletion was blocked by the execution policy; its
  containers and networks were removed.
- Test fixtures created by integration scripts remain in the existing local
  development database; they use unique test names and no real credentials.
- Eight-request same-idempotency concurrency is a smoke, not a sustained load,
  fault-injection, or serializability proof.
- External Provider endpoint compatibility, contract status, data residency,
  and region enforcement remain unknown.
- No SBOM, image signature, vulnerability scan of container OS packages, DAST,
  penetration test, or backup restore exercise was completed.

## I. 本步骤验收清单

- [x] Enumerated runtime endpoints, database tables, jobs, pages, adapters,
  routing, wallet/ledger paths, tests, and deploy components.
- [x] Applied every migration to an empty database.
- [x] Upgraded a V1 database fixture and preserved legacy identity/scope.
- [x] Built and started a truly fresh complete six-service Compose stack.
- [x] Verified admin/user login, user/org/project/key, provider/model, normal and
  streaming requests, usage, wallet debit, and pre-dispatch fallback.
- [x] Ran all available backend/frontend/integration/SDK checks and recorded
  every failure.
- [x] Added an executable commercial smoke test without fabricated results.
- [x] Created this baseline document.
- [ ] Trace one inference request through request log, billing usage, wallet
  transaction, **and audit log**. The first three pass; audit is missing.
- [ ] Meet the no-floating-money requirement.
- [ ] Meet Provider contract-status and allowed-region requirements.
- [ ] Commercial release approval. Blocked by the items above.

This audit stops here and does not implement a subsequent commercial feature
phase.
