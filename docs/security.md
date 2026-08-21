# Security

Supplier onboarding, encrypted payout/credential handling, endpoint SSRF controls, and the manual approval boundary are documented in [supplier-onboarding.md](supplier-onboarding.md). Platform-derived payables, four-eyes settlement approval, dispute blocking, adapter idempotency, and the prohibition on paying supplier-declared usage are documented in [supplier-settlement.md](supplier-settlement.md).

Provider commercial status and BYOK tenant boundaries are documented in [provider-governance.md](provider-governance.md). Customer credentials are encrypted with credential-ID authenticated data, bound to one organization, never returned in plaintext, and never pooled between tenants. ModelDock prohibits API-key sale, consumer-account sharing, automated Provider account registration, geographic-control bypass, and Provider safety-control bypass.

Provider pricing fetches are deny-by-default: exact Provider-configured HTTPS host allowlists, public-IP validation and DNS pinning, no environment proxy, no cross-host redirect, five-second timeout, 1 MiB body limit, strict JSON, and no caller-provided upstream credentials. CSV imports are atomic, limited to 500 rows/1 MiB, accept exact decimal strings only, and reject formula-like cells. Upstream bodies and secrets are never logged; stored source evidence is a sanitized URL plus SHA-256 digest.

Provider quality probes use only platform-owned encrypted credentials and the
same allowlisted Provider adapters. Probe responses are discarded after metric
extraction and SHA-256; customer prompt/response content is never copied into
quality evidence. Supplier-declared uptime, quality, regions, and prices cannot
mutate platform quality policy/state, verified price evidence, SLA, ramp, or
circuit APIs. See [provider-quality.md](provider-quality.md).

RelayDock handles two different trust domains: downstream clients that hold a
RelayDock API key, and upstream providers whose credentials are controlled by
administrators. The system must never collapse these domains into a generic
"token" concept.

## Security invariants

1. An upstream provider secret is never returned to a browser or downstream
   API client.
2. A RelayDock API key is shown once and is not recoverable from storage.
3. Provider traffic uses only official public API credentials and endpoints.
4. Logs omit Authorization, cookies, secret-bearing headers, and prompt content
   by default.
5. Control-plane administration and data-plane inference have independent
   authentication and listeners.
6. An upstream enforcement response changes health/admission state; it is not a
   signal to evade the provider with accounts or proxies.

## Secret classes

| Secret | Storage | Runtime use | Exposure rule |
| --- | --- | --- | --- |
| Provider API/project credential | AES-256-GCM ciphertext in PostgreSQL | Decrypted in memory for one upstream request | Never returned; read APIs expose only `has_secret` and last four characters |
| RelayDock API key | Prefix plus HMAC-SHA-256 digest | Constant-time verification | Full value displayed once at creation |
| User/admin password | Adaptive password hash | Login verification | Never logged or returned |
| `RELAYDOCK_MASTER_KEY` | Deployment environment/secret manager | Provider-secret encryption/decryption | Never stored in PostgreSQL or image |
| `RELAYDOCK_API_KEY_HMAC_SECRET` | Deployment environment/secret manager | RelayDock key digest | Separate from encryption and JWT keys |
| `RELAYDOCK_JWT_SECRET` | Deployment environment/secret manager | Control-plane session signing | Separate from all other keys |
| Account verification/invitation/reset token | HMAC-SHA-256 digest only | Single-use, expiring account transition | Plaintext appears only in the encrypted outbox message |
| TOTP secret | AES-256-GCM ciphertext in PostgreSQL | Administrator MFA validation | Plaintext returned only during enrollment and decrypted in process memory |

AES-GCM encryption uses a fresh cryptographically random nonce for each write
and authenticates stable context such as credential ID/provider ID. Key
rotation should re-encrypt records transactionally and retain an audit event;
V2.0 operators should back up the master key separately because losing it makes
provider ciphertext unrecoverable.

## Key generation

Generate independent random values; never copy values from `.env.example`.

PowerShell 7 / modern .NET:

```powershell
[Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
```

OpenSSL:

```bash
openssl rand -base64 32
```

Run the command separately for the master key, API-key HMAC secret, JWT secret,
database password, Redis password, and initial administrator password. Store
production values in a platform secret manager when possible. Restrict `.env`
to the deployment account and never attach it to issues or support requests.

## Administrator bootstrap

There is no usable default administrator password. Before first startup, set a
unique `RELAYDOCK_ADMIN_EMAIL` and a long random
`RELAYDOCK_ADMIN_PASSWORD`. The bootstrap process creates one administrator
only when the database contains no `ADMIN` or `SUPER_ADMIN`; the database stores
only a password hash. Once an administrator exists, changing the environment
variable does not rotate that password.

Inject the bootstrap value through a secret manager for the first start, then
remove it from the steady-state runtime environment. Administrators can change
or reset passwords through the account lifecycle APIs; both operations revoke
existing refresh sessions. Require TOTP with
`RELAYDOCK_ADMIN_MFA_REQUIRED=true` after enrolling two independently
controlled administrator accounts.

## Authentication and authorization

- Gateway: `Authorization: Bearer rdk_live_...` or `rdk_test_...`.
- Control plane: short-lived signed access cookie, rotating longer-lived
  HttpOnly refresh cookie, readable CSRF cookie, and explicit roles. The normal
  login response does not expose signed tokens to browser JavaScript.
- Administrator operations require `SUPER_ADMIN` or `ADMIN`; user-scoped
  console operations derive the user ID from the authenticated principal, not
  a request parameter. Organization/project reads and mutations additionally
  enforce active membership roles and return `404` for inaccessible tenant IDs.
- API-key prefix narrows lookup only. Authentication requires a constant-time
  version-digest comparison plus logical-key, user, organization, project,
  membership, status, grace, and expiry checks.
- Project route grants, key model allowlists, project budgets, and credential
  tag constraints are checked before upstream dispatch.
- Project-route deletion is fail-closed and tombstoned so historical request
  and usage evidence cannot be orphaned or erased by a control-plane action.

RelayDock quota and project-budget checks are not a transactional billing
authorization system. They use recorded usage and a bounded pre-request token
estimate; unknown output size and concurrent requests can exceed a limit before
accounting settles. Configure provider-side project budgets and alerts as the
hard spend control.

Login, registration, verification/resend, password-reset, and key-creation
endpoints use independent rate limits. Authentication and recovery responses
are intentionally generic so callers cannot enumerate accounts or key prefixes.

## Transport and browser security

The development Compose file binds services to loopback by default. Production
must place a TLS-terminating reverse proxy or load balancer in front of the web
apps and APIs. Do not expose PostgreSQL or Redis publicly.

- Configure exact `ALLOWED_ORIGINS`; wildcard CORS is not used with authenticated
  endpoints.
- Login uses `SameSite=Strict` cookies; access and refresh cookies are
  `HttpOnly`, and production must set `COOKIE_SECURE=true`. Mutations require a
  matching double-submit CSRF value.
- Never store provider keys, the master key, or JWT signing material in
  `localStorage`, browser bundles, or Vite environment variables.
- Nginx sets a restrictive baseline CSP, frame denial, MIME sniff protection,
  referrer policy, and permissions policy. Adjust CSP `connect-src` to the real
  production origins at deployment time.

## Upstream request controls

- RelayDock constructs upstream `Authorization` and optional organization/
  project headers from the selected credential. Incoming versions of those
  headers are discarded.
- Hop-by-hop headers, cookies, proxy-authorization headers, and internal
  infrastructure headers are not forwarded.
- Request bodies have a configurable upper bound (`MAX_REQUEST_BODY_BYTES`).
- Client-supplied request IDs must be bounded and syntactically safe; RelayDock
  always supplies its own trusted internal correlation ID.
- Redirects from an upstream API are not followed to arbitrary hosts with an
  Authorization header.
- Provider base URLs are administrator-only configuration and should be
  restricted to HTTPS in production. The HTTP mock URL is an explicit local
  development exception.
- Webhook targets are validated before storage and delivery, redirects are
  disabled, process proxy settings are not inherited, and private/loopback/link-
  local destinations are rejected unless the explicit local-test exception is
  enabled. Delivery bodies are HMAC-SHA-256 signed with a per-endpoint secret.

## Logging and privacy

Structured logging redacts values matching or associated with:

- `Authorization`, `Proxy-Authorization`, `Cookie`, and `Set-Cookie`;
- `sk-...`, `rdk_live_...`, and `rdk_test_...` key patterns;
- password, secret, session, and token fields;
- encrypted credential bytes and database connection URLs.

`LOG_PROMPT_CONTENT=false` is the required default. Request logs retain the
RelayDock request ID, actor/key IDs, route/model, selected credential ID for
administrators, status, usage, timings, estimated cost, upstream request ID,
and error classification. Establish retention and deletion schedules for these
records because metadata can still be sensitive.

Public-operations governance adds risk scores and abuse/payment states for
users and organizations. Frozen accounts and leaked API keys fail closed;
key-leak checks store only HMAC matches and audit metadata. Content policy
providers run before dispatch and use `RELAYDOCK_CONTENT_POLICY_FAILURE_MODE`
(`FAIL_CLOSED` by default). A provider outage therefore blocks requests unless
an operator explicitly accepts fail-open behavior and its legal/security review.
Prompt and response bodies are not persisted by these controls. Privacy
settings default to `save_content=false`; export/close/delete/purge requests
are idempotent lifecycle jobs, and legal holds block destructive cleanup while
preserving the audit trail.

## Database and Redis

- PostgreSQL is the system of record. Use encrypted storage, authenticated
  backups, a dedicated database user, and TLS when it runs off-host.
- Redis contains ephemeral counters and cooldown state. It is password
  protected in Compose and placed on an internal network, but it is not a
  secret vault.
- A Redis outage fails closed for distributed rate limiting and scheduler lease
  acquisition. Concurrency counters are clamped/atomically released so they
  cannot become negative.
- Migrations execute with a bounded, auditable deployment role in mature
  environments; the steady-state application should have only needed rights.

## Container hardening

The RelayDock and web containers run as explicit non-root users, use read-only
root filesystems, drop Linux capabilities, and set `no-new-privileges`. Runtime
writes are limited to project-local bind mounts and small `tmpfs` paths.
Images are pinned to major/minor release lines in V2.0; production release
automation should pin digests, emit an SBOM, scan dependencies, and sign images.

## Pricing and financial integrity

- Payable amounts and balances use PostgreSQL `NUMERIC` plus exact decimal
  strings/`math/big.Rat`; binary floating point is limited to legacy estimate
  compatibility fields and is never a wallet input.
- `model_price_version`, `pricing_quote`, and `usage_price_snapshot` reject
  update/delete operations at the database layer.
- Provider cost and retail price resolve in one repeatable-read transaction;
  settlement references the selected immutable version.
- Margin overrides require an explicit force flag, the exact second
  confirmation phrase, `FORCED_APPROVED` state, and an attributed audit event
  committed atomically with the forced publication.
- Promotion credit is non-refundable and has a separate locked redemption
  ledger. It cannot increase or be returned as wallet cash.
- Promotion grants, wallet adjustments, and usage charges commit their audit
  records in the same transaction as the financial mutation. Reusing an
  idempotency key with different operation data is rejected as a conflict.
- Provider contract status, region allowlist, and pricing-disable switch are
  checked before a priced gateway request is dispatched.
- Unexpected pricing-store failures fail the gateway request closed. Only the
  documented absence of a migrated commercial price uses the unpriced legacy
  compatibility path.

See [pricing.md](pricing.md) for the full settlement and rollback model.

## Rotation and revocation

- Provider key: disable in RelayDock, revoke at the provider, create/import the
  replacement, validate it, and then re-enable routes.
- RelayDock API key: use versioned rotation with a bounded 30–86400 second grace
  interval, update and verify the client, then finalize the grace version.
  Disabling or revoking the logical key invalidates every version immediately.
- JWT secret: invalidates all sessions; coordinate a maintenance window.
- HMAC secret: requires reissuing downstream keys unless a versioned digest
  migration is implemented.
- Master key: requires transactional re-encryption from the old key to the new
  key and a verified backup.

Every administrative rotation, disable, delete, group change, and route change
must produce an audit record without secret values.

## Explicit exclusions

ModelDock contains no provider/consumer account-registration automation,
CAPTCHA solver/bypass, fingerprint evasion, consumer web session/cookie pool,
proxy rotation for policy evasion, or trial/promotion acquisition. Its own SaaS
users may register and verify email according to the configured policy. See
[compliance.md](compliance.md) for the enforceable acceptable-use boundary.
## Funding ledger

Posted journals and journal entries are protected from UPDATE and DELETE by
database triggers. Every post is checked for equal debit and credit totals and
uses PostgreSQL NUMERIC amounts. Request reservations serialize on the wallet
row across application replicas. Funding metadata excludes prompts, responses,
API keys, Provider secrets, payment credentials, and personal profile data.

Payment-channel credentials and webhook secrets are process secrets, never
database metadata. Recharge credit requires a signed, timely, replay-protected
provider event, an authenticated active query, or an audited administrator
review for the manual adapter. Browser redirects and success pages cannot write
wallet balances. The database persists only normalized event evidence and a
raw-body SHA-256 digest. Sandbox and manual adapters declare
`production_ready=false`; a formal adapter must supply an active contract
status, region allowlist, kill switch, and real sandbox verification before it
may be treated as production-capable.
Late usage and reversals are administrator-only, CSRF-protected, idempotent,
audited operations; they create new entries rather than modifying evidence.
## Subscription authorization

Subscription UI visibility is not an authorization boundary. API Key/member/
Webhook creation, budgets, intelligent routing, cost analysis/log retention,
and every `/v1` request are checked server-side against the frozen effective
plan version. Redis organization RPM/concurrency counters fail closed. No
subscription stores provider credentials, payment secrets, or Token allowances;
invoice amounts remain PostgreSQL `NUMERIC` and Go/API decimal strings.

## Financial close controls

Recharge cash, non-refundable promotional balance, and credit exposure have
separate attribution. Posted journals and finance evidence cannot be edited or
deleted; a correction is a new balanced reversal linked to its source journal,
reconciliation case, named operator, reason, and idempotency key. Daily
differences always enter a durable queue and later matching evidence does not
silently close them. Provider statement imports enforce the Provider enabled
flag, active contract, allowed region, exact decimal total, and source SHA-256.
Before posting a reversal, the server verifies that the source journal belongs
to the case organization and its recharge, funding operation, or subscription
invoice link; an administrator cannot reverse an unrelated settled journal,
and a source journal cannot be reversed again through a second case.
Payment reconciliation accepts only independently identified Provider API,
signed-webhook, or reviewed manual evidence. Unsupported adapters enter the
exception queue instead of reflecting local order state as channel truth.
Provider cost, margin, and Provider billing currency use omitted JSON fields
after console sanitization, so customer responses and CSVs do not expose
commercial terms even as named empty fields.

Finance CSV output neutralizes spreadsheet formulas. Tax identifiers and raw
statement files are not logged. Invoice endpoints only accept applications,
validate/attribute amounts, expose status, and export review data; no tax
authority integration or automatic issuance is claimed. See
[financial-close.md](financial-close.md).

Console finance reads require an active organization `VIEWER`; refund and
invoice application writes require at least `MEMBER`. `VIEWER` and `MEMBER`
have distinct server-side ranks. Finance create idempotency checks bind the key
to the complete source/title/period payload, not just to its amount.

The legacy admin wallet topup path remains backward compatible as an explicitly
internal, non-refundable adjustment; it is never classified as a user payment.
Actual customer cash must enter through a verified recharge order so every user
payment has channel, order, journal, and cash-lot evidence. Promotional grants
remain separately audited and non-refundable.

## Observability and support privacy

Structured logs and traces contain correlation metadata, timing, route/model,
status, and aggregate usage. They exclude prompts, responses, API keys,
Provider/payment secrets, and authorization headers. Public status uses a fixed
safe schema and removes event metadata. Support input is redacted before
storage, internal notes are excluded from console reads, and arbitrary context
keys are dropped. Administrators must still avoid pasting credentials or
customer content because automated redaction cannot prove every future secret
format. See [observability-operations.md](observability-operations.md).

`RELAYDOCK_PUBLIC_SUPPORT_EMAIL` and
`RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL` are returned unauthenticated by
`GET /api/public/config`. They must be monitored organizational role mailboxes,
not personal addresses or aliases whose disclosure creates a privacy risk.
# Public Beta hardening

Production runtime settings must keep `RELAYDOCK_PROVIDER_ALLOW_HTTP=false` and
`RELAYDOCK_PROVIDER_ALLOW_PRIVATE_NETWORK=false`, and set an explicit
`RELAYDOCK_PROVIDER_ALLOWED_HOSTS` list. The Provider dialer validates the
allowlist and final DNS-resolved address, while response forwarding copies only
the documented safe headers. `RELAYDOCK_JWT_PREVIOUS_SECRET` is a temporary
rotation window; new sessions are always signed by `RELAYDOCK_JWT_SECRET`.

Migration `0018_beta_runtime_hardening` seals new audit rows with an immutable
SHA-256 predecessor hash. The public-beta deployment also caps request and
stream sizes, returns readiness false before graceful shutdown, and runs an
independent migration job before application replicas.
# Marketplace release and payout boundary

Migration V23 treats every Provider with a non-ended supplier link as a
third-party Marketplace route. Such a route requires an in-review foundation
canary or a fully approved 23-gate release. Both the initial admission and the
transactional dispatch recheck enforce this condition, so a concurrent suspend,
cutover, exit, or revocation cannot create a new authorized attempt.

Automated gates read only platform-owned endpoint verification, approved model
and price records, quality state, request/usage records, funding and wallet
state, payable entries, refunds, settlements, reconciliation, disputes, and
payout readiness. Listing `uptime`, `verified`, price JSON, and supplier bills
remain declarations. Operational drill attestations require an admin actor,
reason, and external evidence reference; every change appends an immutable gate
event and audit record.

Payout readiness never stores the destination itself and APIs never return the
encrypted destination. Evidence references must contain ticket/report IDs only,
not API keys, tax identifiers, account numbers, prompts, responses, or personal
data. Every non-sandbox payout is checked before approval, claim, completion,
and by a PostgreSQL status-transition trigger. Revoking readiness therefore
blocks the next state transition even during an operational race.

See [Marketplace policies](marketplace/README.md) and the
[launch runbook](marketplace/launch-acceptance.md).
