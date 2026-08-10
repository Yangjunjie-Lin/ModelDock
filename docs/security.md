# Security

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
remove it from the steady-state runtime environment. V2.0 does not expose a
self-service password-change endpoint, so deployments must define a controlled
database recovery procedure before production use.

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

Login and key-creation endpoints require dedicated rate limits. Authentication
errors are intentionally generic so callers cannot enumerate accounts or key
prefixes.

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

RelayDock contains no automatic account registration, email verification,
CAPTCHA solver/bypass, fingerprint evasion, consumer web session/cookie pool,
proxy rotation for policy evasion, or trial/promotion acquisition. See
[compliance.md](compliance.md) for the enforceable acceptable-use boundary.
