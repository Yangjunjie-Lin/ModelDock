# ModelDock

**中国开发者可用的 AI Model Gateway + Model Marketplace**

ModelDock is the commercial evolution of RelayDock: a self-hosted,
OpenAI-compatible model gateway that lets applications reach global and Chinese
model providers without changing SDK code. The existing `rdk_*` key format,
`RELAYDOCK_*` environment variables, database identities, and `/v1` behavior are
retained for in-place upgrades.

> 中文定位：面向 AI 模型调用的统一 API 中转、授权凭据池、模型路由、用量统计与管理平台。

ModelDock V3 seeds OpenAI, Anthropic, Gemini, DeepSeek, Qwen, Kimi, GLM, and
OpenRouter provider records. Every provider still requires an
administrator-owned official API credential. ModelDock does not create provider
accounts, automate verification/CAPTCHA, pool consumer web sessions, claim
trials/promotions, or rotate proxies/identities to evade limits.

## Highlights

- OpenAI-compatible `/v1/responses`, `/v1/chat/completions`, `/v1/embeddings`,
  and `/v1/models`
- incremental SSE proxying with cancellation and time-to-first-byte metrics
- dynamic model registry with context windows, versioned prices, quality scores,
  and latency penalties
- platform-measured Provider health, TTFT/full latency, throughput, errors/429,
  availability, output sampling, price truth, region coverage, SLA events,
  quality grades, supplier ramp-up, automatic downweighting, and circuit breaking
- manual, cost-optimized, quality-optimized, and balanced cross-model routing
- runtime Provider registry with OpenAI, Anthropic, Gemini, and DeepSeek adapter
  types plus OpenAI-compatible Chinese provider integrations
- Provider Marketplace listings plus versioned platform-evidence launch gates,
  controlled canary traffic, four-part production-payout readiness, emergency
  cutover, suspension, and supplier exit
- organization wallets, idempotent transaction ledger, request-level billing
  usage records, prepaid admission, and postpaid compatibility mode
- concurrent maximum-cost reservations, immutable balanced debit/credit
  journals, stream-safe settlement, late-usage correction, and crash recovery
- exact commercial provider-cost/retail price books, organization plans,
  immutable request price snapshots, margin protection, and non-refundable
  promotion credits separated from wallet cash
- version-frozen Free, Developer, Team, and Enterprise subscriptions with
  server-enforced feature entitlements; Token usage always remains separately
  metered and is never included as an unlimited plan allowance
- end-to-end finance traceability from verified recharge through cash/bonus/
  credit attribution and API charge to Provider attempt and statement line,
  with refund/invoice applications, CSV/accounting exports, and replay-safe
  six-way daily reconciliation
- enterprise organizations, projects, teams, memberships, quotas, budgets, and
  audit logs
- encrypted, administrator-imported provider credential inventory
- bounded JSON import and batch operations for up to 25 already-issued,
  authorized provider API credentials (never accounts or browser sessions)
- credential groups, priority, weight, concurrency ceilings, cooldown, and
  explainable selection decisions
- RelayDock `rdk_live_...` / `rdk_test_...` API keys with display-once secrets,
  project scope, versioned grace rotation, model allowlists, expiry, RPM/TPM,
  and monthly quotas
- organizations, projects, role-based memberships, and explicit project route
  grants with required/excluded credential-tag constraints
- daily/monthly project budget policies, threshold events, and pre-dispatch
  hard-limit admission
- signed project webhooks with a durable retry/dead-letter outbox, project CSV
  usage export, and attributable alert acknowledgement
- separate admin and user control-plane experiences
- request metadata, daily usage, internal cost estimates, health, metrics, and
  audit logs
- PostgreSQL as the system of record; Redis for atomic counters, rate limits,
  cooldown, locks, and cache
- hardened non-root containers and project-local D-drive-friendly bind mounts
- official Python SDK compatibility tests

## Architecture

```text
Authorized client
  -> versioned, project-scoped RelayDock API key authentication
  -> active user / organization / project / membership checks
  -> manual alias or intelligent model route + key allowlist + budget/wallet admission
  -> authorized credential scheduler + project tag constraints
  -> provider runtime registry
  -> official provider API
  -> streaming/non-streaming response
  -> scoped usage/log/budget event + transactional webhook outbox
```

One Go binary opens two listeners:

- gateway/data plane: `8080`;
- control-plane API: `8081`.

The admin web app runs on `3000`, the user console on `3001`, PostgreSQL on
host port `5433` (container port `5432`), and Redis on `6379` by default.
PostgreSQL, Redis, and application logs
bind to `./data/postgres`, `./data/redis`, and `./logs`.

Read [the ModelDock V3 guide](docs/modeldock.md),
[the public Beta production operations guide](docs/public-beta-operations.md),
[the architecture guide](docs/architecture.md), and
[the financial-close runbook](docs/financial-close.md),
[architecture decisions](docs/architecture-decisions.md) for request flow,
scheduler semantics, failure handling, and secret boundaries. V2 operators and
integrators should also read the [V2.0 contract](docs/v2.md), the
[account lifecycle and MFA operations guide](docs/account-lifecycle.md), the
[developer quickstart](docs/developer-quickstart.md), the
[public commercial onboarding runbook](docs/commercial-onboarding-operations.md),
the counsel-review drafts in [docs/legal](docs/legal/README.md), the
[API guide](docs/api.md), the [supplier onboarding guide](docs/supplier-onboarding.md), the
[Marketplace policy and launch set](docs/marketplace/README.md), and the machine-readable
[OpenAPI document](docs/openapi.yaml).

## Screenshots

V2.0 screenshot placeholders:

| Admin | Console |
| --- | --- |
| Dashboard, credential pool, groups, routes, users, usage, request logs | API keys, allowed models, usage, request history |

Run the web apps locally to see the current implementation. Project releases
can replace this table with versioned screenshots without changing product
behavior.

## Quick start with Docker Compose

Prerequisites: Docker Engine/Desktop 24+ and Docker Compose v2.

```powershell
Set-Location D:\RelayDock
Copy-Item .env.example .env
.\deploy\scripts\prepare-data.ps1
```

Edit `.env` and replace every `CHANGE_ME` value. Generate independent secrets;
for example, in modern PowerShell:

```powershell
[Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
```

Use separate generated values for `RELAYDOCK_MASTER_KEY`,
`RELAYDOCK_API_KEY_HMAC_SECRET`, `RELAYDOCK_JWT_SECRET`, database/Redis
passwords, and `RELAYDOCK_ADMIN_PASSWORD`. The master key must decode to exactly
32 bytes. There is no
usable default administrator password.

Validate and start:

```powershell
docker compose --env-file .env config --quiet
docker compose --env-file .env up -d --build
docker compose --env-file .env ps
```

Open:

- Admin: <http://127.0.0.1:3000>
- User console: <http://127.0.0.1:3001>
- Gateway health: <http://127.0.0.1:8080/healthz>
- Control-plane health: <http://127.0.0.1:8081/healthz>

Sign in to the admin app with the `RELAYDOCK_ADMIN_EMAIL` and
`RELAYDOCK_ADMIN_PASSWORD` you set in the deployment environment. The password
is used only when the database has no administrator and is hashed in
PostgreSQL; it is not a compiled-in credential. After bootstrap, remove it from
the steady-state runtime environment. See [deployment.md](docs/deployment.md) for TLS,
reverse-proxy, backups, Linux permissions, upgrades, and production hardening.

## First working route

1. In the official OpenAI platform, create a project/API credential that your
   organization is authorized to use.
2. Sign in to RelayDock Admin. The built-in OpenAI provider uses
   `https://api.openai.com/v1`.
3. Add the credential and select **Validate & save**. RelayDock validates it
   through the public Models API, encrypts it, and never returns its plaintext.
4. Create a credential group (for example, `Production Pool`) and add the
   credential with an explicit priority, weight, and concurrency ceiling.
5. Synchronize models or configure model metadata, then create an alias such as
   `gpt-default` pointing to an official upstream model and that group.
6. Create an organization and project, grant the `gpt-default` route to that
   project, and assign the user an active organization/project membership.
7. Sign in to the console and issue a project-scoped `rdk_live_...` or
   `rdk_test_...` key. Copy the secret from its one-time display.

RelayDock stores only the downstream key prefix and HMAC digest. Losing the
one-time value requires issuing a replacement key.

## Call RelayDock

Python with the official OpenAI SDK:

```python
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["RELAYDOCK_API_KEY"],
    base_url="http://127.0.0.1:8080/v1",
)

response = client.responses.create(
    model="gpt-default",
    input="Say hello in one sentence.",
)
print(response.output_text)
```

JavaScript:

```javascript
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.RELAYDOCK_API_KEY,
  baseURL: "http://127.0.0.1:8080/v1",
});

const completion = await client.chat.completions.create({
  model: "gpt-default",
  messages: [{ role: "user", content: "Say hello." }],
});

console.log(completion.choices[0].message.content);
```

Streaming Responses:

```python
stream = client.responses.create(
    model="gpt-default",
    input="Count from one to three.",
    stream=True,
)

for event in stream:
    if event.type == "response.output_text.delta":
        print(event.delta, end="", flush=True)
```

More examples and the complete endpoint inventory are in
[docs/api.md](docs/api.md). The machine-readable contract is
[docs/openapi.yaml](docs/openapi.yaml).

## Routing and credential lifecycle

Production routing is fail-closed on Provider commercial governance. Technical health does not imply commercial authorization: only `COMMERCIAL_APPROVED` Providers with approved resale permission, valid dates, matching customer/model/data regions, organization policy, available Provider limits/budget, current margin, and a disabled kill switch enter routing. Provider costs support bounded manual, allowlisted HTTPS API, and atomic CSV ingestion with separate approval; BYOK uses organization-bound encrypted credentials and append-only service-fee policies. See [Provider governance and BYOK](docs/provider-governance.md).

Public-operation controls add scoped user/organization risk state, API key leak freezing, pluggable request/response content policy hooks, complaint queues, and privacy lifecycle jobs. Prompt and response content is not persisted in ordinary request logs by default. Regulatory fields and UI disclosures are explicitly marked for professional legal review; see [Public operations governance](docs/public-operations-governance.md).

A route maps:

```text
RelayDock alias -> provider -> upstream model -> primary credential group
                                              -> optional fallback group
```

Within a group, the scheduler chooses the highest effective priority and then
the best weighted least-loaded eligible credential. It atomically acquires a
Redis concurrency lease and stores an explainable reason with the request log.

State handling:

- `401`: mark the selected credential `AUTH_FAILED` and remove it from
  scheduling;
- `429`: place the credential in a bounded cooldown and honor configured reset
  behavior;
- successful response: mark it healthy and clear cooldown;
- Redis state unavailable: fail closed rather than oversubscribe;
- once streaming begins: never replay or switch credentials.

The fallback group is consulted only if the primary group has no eligible
credential, before any upstream attempt. V2.0 does not replay a request after
an upstream attempt starts. Fallback is for bounded availability handling, not
rate-limit or policy evasion. RelayDock contains no proxy rotation service.

## Configuration

### Local Cockpit quota bridge

The Admin credential page can show a read-only, Cockpit-style view of locally
authorized account status and quota. RelayDock deliberately does not read or
mount Cockpit OAuth tokens, cookies, passwords, account files, or upstream
keys. Generate the allowlisted snapshot on the Windows host:

```powershell
.\scripts\sync-cockpit.ps1 -Test
```

This writes `data/cockpit/accounts.json` containing only a hashed local account
identifier, masked email, plan, status, quota percentages, reset/subscription
timestamps, and the sanitized result of the optional fixed-response model
test. Compose mounts only that directory read-only.

`COCKPIT_BASE_URL`, `COCKPIT_API_KEY`, and `COCKPIT_TEST_MODEL` optionally
enable the Admin page's live **Test sidecar** button. Keep the sidecar key in
the local untracked environment only; it is never returned by an API or
written to the snapshot. Without it, the UI still shows the latest host-side
test recorded by `sync-cockpit.ps1 -Test`.

Both Admin and Console support English and Simplified Chinese. The manual
switch is available on the login screen and in the top bar, and the preference
is stored locally as `rd-language`.

All secrets are injected through the environment. The main variables are:

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection URL |
| `REDIS_URL` | Authenticated Redis URL |
| `RELAYDOCK_MASTER_KEY` | Exactly 32 decoded bytes for AES-256-GCM provider-secret encryption |
| `RELAYDOCK_API_KEY_HMAC_SECRET` | At least 32 bytes for downstream key digests |
| `RELAYDOCK_JWT_SECRET` | At least 32 bytes for control-plane sessions |
| `RELAYDOCK_ADMIN_EMAIL`, `RELAYDOCK_ADMIN_PASSWORD` | Operator-selected initial administrator; no production default |
| `RELAYDOCK_PUBLIC_SUPPORT_EMAIL`, `RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL` | Public monitored role mailboxes returned by `/api/public/config`; replace the non-delivering `example.invalid` defaults before launch |
| `COOKIE_SECURE` | Set `true` behind production HTTPS so login cookies are Secure |
| `ALLOWED_ORIGINS` | Exact comma-separated admin/console browser origins |
| `MAX_REQUEST_BODY_BYTES` | Gateway JSON request-body ceiling |
| `CREDENTIAL_COOLDOWN` | Default cooldown after upstream `429` |
| `RELAYDOCK_PROVIDER_TIMEOUT` | Per-attempt Provider deadline for inference |
| `RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION` | Actual ISO alpha-2 egress region; empty safely disables scheduled probes |
| `RELAYDOCK_PROVIDER_QUALITY_POLL_INTERVAL` | Probe/evaluation worker polling interval |
| `RELAYDOCK_PROVIDER_QUALITY_LEASE` | Cross-replica database probe lease |
| `RELAYDOCK_PROVIDER_QUALITY_BATCH_SIZE` | Maximum claims handled per worker cycle |
| `RELAYDOCK_SUPPLIER_SETTLEMENT_POLL_INTERVAL`, `RELAYDOCK_SUPPLIER_SETTLEMENT_BATCH_SIZE` | Platform-measured payable accrual, reserve release, cycle, and payout recovery cadence |
| `RELAYDOCK_PAYOUT_ALLOWED_REGIONS` | Explicit payout-adapter region allowlist |
| `RELAYDOCK_PAYOUT_SANDBOX_ENABLED`, `RELAYDOCK_PAYOUT_SANDBOX_SECRET` | Disabled-by-default test-only idempotent payout adapter; never enable for production settlement |
| `RELAYDOCK_FUNDING_RECOVERY_INTERVAL` | Stale funding operation recovery poll interval |
| `RELAYDOCK_FUNDING_STALE_AFTER` | Age after which an abandoned reservation is recovered |
| `RELAYDOCK_PAYMENT_ORDER_TTL`, `RELAYDOCK_PAYMENT_POLL_INTERVAL` | Recharge expiry and recovery cadence |
| `RELAYDOCK_SUBSCRIPTION_POLL_INTERVAL` | Idempotent renewal, grace-period, expiry, and scheduled plan-change cadence |
| `RELAYDOCK_PAYMENT_ALLOWED_REGIONS` | Explicit payment-adapter region allowlist |
| `RELAYDOCK_PAYMENT_SANDBOX_ENABLED`, `RELAYDOCK_PAYMENT_SANDBOX_SECRET` | Test-only signed sandbox switch and environment-injected secret |
| `RELAYDOCK_PAYMENT_MANUAL_ENABLED` | Administrator-reviewed manual-transfer switch |
| `LOG_DIR` | Optional directory for append-only `relaydock.jsonl`; Compose uses `/app/logs` bound to `D:\RelayDock\logs` |
| `LOG_PROMPT_CONTENT` | Keep `false` unless a governed logging policy explicitly permits content |
| `COCKPIT_SNAPSHOT_PATH` | Read-only path to the sanitized Cockpit account snapshot |
| `COCKPIT_BASE_URL`, `COCKPIT_API_KEY` | Optional local sidecar endpoint and client key for the live fixed-response check |
| `COCKPIT_TEST_MODEL` | Model alias used by the optional Cockpit sidecar test |

See [.env.example](.env.example) for the complete development template. Never
commit `.env`, provider credentials, downstream keys, database dumps, or logs.

## Local development

Host toolchain:

- Go 1.26.6 (the module toolchain is pinned for security and reproducibility)
- Node.js 22.22.3 and npm
- PostgreSQL 17 and Redis 7.4, or run only those dependencies with Compose

When the Go process runs on the host while PostgreSQL and Redis run in Compose,
copy the required secret values from your private development configuration and
use loopback addresses rather than the Compose service names:

```powershell
$env:DATABASE_URL = "postgres://<user>:<password>@127.0.0.1:5433/<database>?sslmode=disable"
$env:REDIS_URL = "redis://:<password>@127.0.0.1:6379/0"
$env:RELAYDOCK_MASTER_KEY = "<base64-encoded-32-byte-key>"
$env:RELAYDOCK_API_KEY_HMAC_SECRET = "<at-least-32-random-bytes>"
$env:RELAYDOCK_JWT_SECRET = "<at-least-32-random-bytes>"
```

Set the optional bootstrap administrator variables only for a new database.
The remaining development defaults are documented in [`.env.example`](.env.example).

Typical commands:

```powershell
go test ./...
npm --prefix apps/admin-web ci
npm --prefix apps/admin-web run dev
npm --prefix apps/console-web ci
npm --prefix apps/console-web run dev
go run ./cmd/relaydock
```

Container builds use same-origin `VITE_API_BASE` paths, with Nginx proxying
`/api/*` to the control plane and `/v1/*` to the gateway. The development Vite
servers provide equivalent local proxies. Vite values are public build
configuration—never place a secret in a `VITE_*` variable.

## Repository layout

```text
RelayDock/
├── cmd/relaydock/             single Go entry point
├── internal/                  domain, auth, crypto, routing, scheduler, provider, usage
├── migrations/                PostgreSQL schema
├── apps/
│   ├── admin-web/             administrator control plane
│   └── console-web/           user console
├── deploy/
│   ├── docker/                non-root application/web images
│   ├── nginx/                 static SPA server hardening
│   └── scripts/               project-local runtime directory setup
├── docs/                      architecture, API, security, compliance, deployment
├── tests/sdk/python/          official Python SDK compatibility suite
├── docker-compose.yml
└── .env.example
```

## Security

- Provider credentials are AES-256-GCM encrypted at rest.
- RelayDock keys are random, shown once, and stored as prefix + HMAC digest.
- Browser control-plane access uses short-lived HttpOnly access cookies,
  rotating HttpOnly refresh cookies, and same-origin CSRF protection.
- Browser responses never include upstream secrets or internal authorization
  headers.
- Prompt/response content logging is off by default.
- Application containers run non-root with dropped capabilities and read-only
  root filesystems where practical.
- Admin and user mutations are audited without secret values.
- Inference credentials and any provider-side organization-management
  credential remain separate.

Read [security.md](docs/security.md) before production deployment. Please report
security issues with redacted request IDs and without live keys, cookies,
database URLs, or personal data.

## Compliance boundary

RelayDock is for authorized official API credentials. The project explicitly
excludes:

- automatic or bulk provider/consumer account registration;
- temporary-email or OTP/email-verification automation;
- CAPTCHA/Turnstile solving or bypass;
- fingerprint evasion and consumer browser session/cookie pools;
- proxy, IP, identity, project, or credential rotation intended to evade
  upstream controls;
- automated trials, promotions, checkout flows, or benefit claims;
- extraction/replay of private consumer web tokens.

Safe bulk operations apply only to already-authorized provider API credentials
and metadata. See [compliance.md](docs/compliance.md) and the clean-room
[reference analysis](docs/reference-analysis.md).

## Production deployment

The hardened Ubuntu 24.04 deployment package, including Nginx, Certbot,
resource limits, systemd fallback, backups, DNS, HTTPS, and troubleshooting, is
documented in [deploy/production/README.md](deploy/production/README.md).
Set `RELAYDOCK_PUBLIC_SITE_DOMAIN` to a dedicated public website/Console
hostname and keep it aligned with `RELAYDOCK_PUBLIC_CONSOLE_URL` and
`ALLOWED_ORIGINS`. Leaving it empty preserves the legacy API-only hostname;
the public hostname allowlists only Console static assets, `/api/public/*`,
`/api/console/*`, and `/v1/*`, while Admin remains loopback/profile-only.

## Known V3 limits

- The seeded providers use their official OpenAI-compatible surfaces. ModelDock
  does not translate an endpoint a provider does not implement; apply model
  capability filters and route each endpoint to a compatible upstream.
- The deployment topology is single-node Compose, not HA/Kubernetes.
- Only Models, Responses, Chat Completions, and Embeddings are exposed through
  the compatibility gateway.
- Provider capabilities and context windows may require administrator metadata;
  unknown values remain null instead of being guessed.
- `estimated_cost` remains a compatibility estimate, not a provider invoice;
  wallet settlement uses the immutable commercial price snapshot and final user
  amount documented in [pricing.md](docs/pricing.md).
- API-key quotas and project budgets are admission guards over recorded usage.
  Because
  output size and final cost are unknown before completion, one request (or
  concurrent in-flight requests) can overshoot a configured limit; retain
  provider-side project budgets as the hard financial boundary.
- Retention values are control-plane policy metadata in V2.0; automatic database
  cleanup is not bundled. Operators must run an approved scheduled retention
  job until a native worker is added.
- Recharge orders support a test-only sandbox adapter and an
  administrator-reviewed manual-transfer adapter. Supplier settlement now has
  platform-measured payables, reconciliation, approval, disputes, and a disabled
  test-only payout adapter, but no contracted production payment/payout processor
  or tax-invoice issuance engine is bundled. Any future non-sandbox payout
  adapter is database-blocked until supplier-specific contract, tax, payment,
  and security reviews are all approved.
- A project license has not yet been selected; see **License** below.

## V2.0 multi-project governance

V2.0 is implemented and documented in [docs/v2.md](docs/v2.md). It includes
organizations/projects, membership roles, project route grants, credential-tag
constraints, project-scoped versioned keys, budget policies/events, signed
durable webhooks, project CSV export, and alert acknowledgement. Existing V1
data is preserved in deterministic `Legacy` organization/project records.

## V3 roadmap

Potential future work, subject to the same authorization and compliance rules:

- distributed budget reservations with post-response reconciliation;
- payment processor and invoice integrations;
- supplier onboarding, verification, SLA probes, and settlement;
- richer policy/region/data-residency-aware routing;
- webhook replay observability, additional approved event types, and external
  alert integrations;
- provider failover policies with replay-safety proofs;
- high availability, horizontal scaling, external secret managers, and
  Kubernetes deployment.

Registration automation, CAPTCHA bypass, consumer session pooling, promotion
abuse, and upstream-limit evasion are permanent exclusions, not roadmap items.

## Troubleshooting

- **Startup rejects a secret:** the master key must decode to exactly 32 bytes;
  HMAC/JWT secrets must be at least 32 bytes.
- **Database/Redis unhealthy:** verify bind-directory permissions and that
  passwords embedded in `DATABASE_URL`/`REDIS_URL` match the service variables.
- **Admin UI cannot call the API:** verify the web container's `/api` proxy,
  exact `ALLOWED_ORIGINS`, HTTPS rules, and port 8081 readiness.
- **SSE is buffered:** disable buffering in the production reverse proxy.
- **Credential receives `401`:** revoke/reissue through the official provider
  dashboard; do not route around the failure.
- **Credential receives `429`:** reduce admitted traffic and honor cooldown/reset
  information; do not attempt proxy/account rotation.

Detailed diagnostics are in [deployment.md](docs/deployment.md).

## Operations and incident response

ModelDock includes JSON structured logs, W3C/OTLP traces, Prometheus metrics,
five SLO definitions, automatic incident alerts, a privacy-safe public status
API, and audited user/admin support tickets. Operators can investigate a
request ID across route, Provider attempt, usage, funding settlement, wallet
transaction, and ledger journal without collecting an API key or prompt. Setup,
rollback, alert ownership, and eight stop-loss runbooks are in
[observability-operations.md](docs/observability-operations.md).

## Contributing

Changes should preserve the data/control plane split, wire compatibility,
secret redaction, replay-safety, deterministic tests, and the compliance
boundary. Run backend tests, both frontend typechecked builds, Compose config,
the mock integration, and SDK compatibility suite before proposing a release.

The complete contribution gates are in [CONTRIBUTING.md](CONTRIBUTING.md).
Release semantics, SBOM/provenance outputs, immutable image tags, and rollback
are defined in [RELEASE.md](RELEASE.md). ModelDock product naming deliberately
retains RelayDock technical compatibility as documented in
[naming-compatibility.md](docs/naming-compatibility.md).

Do not submit features or documentation for account farming, CAPTCHA bypass,
consumer session scraping, automated benefit acquisition, or policy evasion.

## License

No license file has been selected for this repository yet. Until a license is
added by the project owner, do not assume permission to copy, redistribute, or
create derivative works. Reference repositories retain their own licenses; no
code from them is included in RelayDock.

The owner decision and consequences of proprietary, Apache-2.0, AGPL, and dual
licensing are tracked in [licensing-decision.md](docs/licensing-decision.md).
Its unresolved status is an automated formal-release blocker.
- verified recharge orders with replay-safe sandbox webhooks, administrator-reviewed manual transfers, recoverable atomic wallet credit, refunds, and reconciliation evidence
