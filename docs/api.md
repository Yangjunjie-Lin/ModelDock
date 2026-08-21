# API guide

## Financial close APIs

Finance routes use the existing authenticated control plane and never alter the
OpenAI-compatible `/v1` contract. Console organization routes expose recharge
history, subscription invoices, balance composition (`cash_available`,
`bonus_available`, credit limit/used/available), request-level Token charges,
monthly statements, refund applications, invoice applications, and CSV export.
Every amount is an exact decimal string.

Admin finance routes query payment/anomaly queues and immutable wallet entries;
approve/refuse refund and invoice applications; report Provider cost, customer
revenue, and gross margin; atomically export approved invoice applications and
accounting CSV; import immutable Provider
statements; and run/list/resolve six-way reconciliation cases. Decisions and
reversals require an idempotency key, named actor, and non-empty reason.

CSV cells beginning with `=`, `+`, `-`, or `@` are neutralized before download.
Provider statements require an enabled Provider, active contract, allowed
Provider and linked-organization region, exact line total, and SHA-256 source digest. Invoice APIs support only
application, status, amount/source validation, review, and export. No automatic
tax invoice is issued because no tax authority is integrated. See the exact
paths in [openapi.yaml](openapi.yaml) and operational semantics in
[financial-close.md](financial-close.md).

RelayDock exposes two independent HTTP surfaces:

| Surface | Default base URL | Authentication |
| --- | --- | --- |
| Gateway/data plane | `http://127.0.0.1:8080` | RelayDock API key (`rdk_live_...` or `rdk_test_...`) |
| Control plane | `http://127.0.0.1:8081` | HttpOnly access/refresh cookies plus CSRF |

The machine-readable contract is [openapi.yaml](openapi.yaml). OpenAI-compatible
request/response bodies are intentionally extensible because the official API
may add optional fields and streaming event types.

For a copy-ready path from checking the actual regional price through curl,
Python, JavaScript/TypeScript, SSE, Embeddings, billing, BYOK, retention, and
fallback behavior, read the [developer quickstart](developer-quickstart.md).

## Public commercial discovery

The following control-plane reads are unauthenticated so a visitor can make an
informed decision before registration or purchase:

| Method and path | Purpose |
| --- | --- |
| `GET /api/public/config` | Product identity, registration/verification policy, public support/Enterprise mailboxes, legal-review flag, supported regions/currencies and funnel behavior |
| `GET /api/public/catalog/models?region=CN&currency=CNY` | Region-filtered model capability, commercial availability, and current approved retail price |
| `GET /api/public/catalog/providers?region=CN` | Public-safe Provider contract/resale/region/status disclosure |
| `GET /api/public/pricing?region=CN&currency=CNY` | Separate subscription fees, Token prices, payment/platform fees, bonus, tax, refund, and availability terms |
| `GET /api/public/status` | Privacy-safe service and Provider status; Provider `OPERATIONAL` requires approved commercial/resale state, pricing enabled, and a schedulable credential, but is not a continuous upstream probe |
| `POST /api/public/funnel/events` | Idempotent, bounded `HOMEPAGE_VISITED` event only; raw anonymous IDs are not retained |

Every payable amount is an exact decimal string. A null price/terms object,
`available: false`, or `payment_region_supported: false` must be treated as
unavailable, not estimated by the client. Payment-region support requires both
the configured region and a runtime-admitted adapter with an approved fee
disclosure for that channel.
`legal_review_required` identifies draft deployment wording and must remain
visible until counsel approves it. Public catalog output does not expose
Provider credentials, base URLs, acquisition costs, margin, contract documents,
or internal capacity/budget details.
Public availability is a pre-purchase offer signal, not proof of project route
authorization, eligible credential inventory, or real-time upstream health and
capacity. Those gates are evaluated for the authenticated project at dispatch.
Provider processing regions are disclosure fields; they are not folded into
the public availability value.

`POST /api/public/funnel/events` requires an idempotency key in the
`Idempotency-Key` header or body. Only `HOMEPAGE_VISITED` is caller-writable.

Public `token_prices[]` rows include nested `availability`; callers must not
infer regional permission from the presence of a numeric price alone.
`payment_region_supported` is the deployment payment-region allowlist result,
separate from `payment_fees_configured`. `bonus_credit_amount` is disclosure
metadata only and never creates PromotionCredit or ledger value by itself.

`support_email` and `enterprise_email` are deliberately public deployment
configuration. Values ending in `example.invalid` are non-delivering
placeholders and must not be presented as working contact channels in a public
launch. Operators should configure monitored role mailboxes rather than a
person's private address.

Authenticated onboarding evidence is read from
`GET /api/console/onboarding?organization_id=...&project_id=...`; it validates
both memberships, echoes the selected project, derives project-scoped steps
from database evidence, and provides no client-write completion endpoint. First
and second call milestones require HTTP 2xx plus terminal settlement evidence.
Administrators can read aggregate UTC funnel counts from
`GET /api/admin/funnel/summary?from=...&to=...`.

## Common behavior

Every response includes both `X-Request-Id` and
`X-RelayDock-Request-Id`. They contain RelayDock's trusted request ID. A client
may send a bounded `X-Client-Request-Id`; it is forwarded to supported upstream
endpoints for diagnostics, but it never replaces the RelayDock-generated ID.

Errors use an OpenAI-style envelope:

```json
{
  "error": {
    "message": "The requested model does not exist or is not enabled.",
    "type": "relayedock_error",
    "param": null,
    "code": "model_not_found"
  }
}
```

Important status/code pairs:

| HTTP | Code | Meaning |
| --- | --- | --- |
| `400` | `invalid_json`, `invalid_request` | Malformed or semantically invalid request |
| `401` | `invalid_api_key`, `invalid_session` | Missing/invalid downstream credential or session |
| `402` | `insufficient_balance` | Prepaid wallet cannot reserve the maximum request cost |
| `403` | `model_not_allowed`, `quota_exceeded`, `project_budget_exceeded`, `provider_region_unavailable`, `provider_commercial_unavailable`, `provider_pricing_disabled`, `insufficient_permissions`, `csrf_failed` | Authenticated but not admitted |
| `404` | `model_not_found`, `not_found` | Route/resource not found |
| `409` | `idempotency_conflict`, `idempotent_replay` | Logical operation was already admitted or the key was reused with different input; no extra reservation/charge is created. An inference replay returns `X-RelayDock-Original-Request-Id`, not cached model output. |
| `413` | `request_too_large` | Body exceeds `MAX_REQUEST_BODY_BYTES` |
| `422` | `credential_validation_failed`, `provider_mismatch` | Provider validation failed, or a route/group attempted to cross provider ownership boundaries |
| `429` | `rate_limit_exceeded` | RelayDock client-key limit reached; inspect `Retry-After` |
| `502` | `provider_error` | Upstream connection/service failure |
| `503` | `provider_unavailable`, `service_unavailable` | No eligible credential or required state unavailable |

List endpoints return `{ "data": [...] }`. High-volume resources such as users,
credentials, API keys, logs, alerts, and audit logs accept `limit` (default 50,
maximum 200) plus `offset`; catalog/configuration lists are currently returned
in full. Usage endpoints accept `days` (default 30, maximum 365).

## Gateway API

Send only a RelayDock key:

```http
Authorization: Bearer rdk_live_xxxxxxxxxxxxxxxxx
```

An OpenAI provider key is never a downstream gateway credential.

### List configured model aliases

```http
GET /v1/models
```

The list contains enabled RelayDock aliases allowed by the calling key. It is a
configured catalog, not a transparent dump of every upstream model.

```bash
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY"
```

### Responses

```http
POST /v1/responses
Content-Type: application/json
```

```json
{
  "model": "gpt-default",
  "input": "Say hello in one sentence."
}
```

The gateway replaces the alias with its configured upstream model, injects the
selected authorized provider credential, and otherwise preserves supported
public request/response fields.

Python, using the official OpenAI SDK:

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

Streaming:

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

RelayDock forwards SSE incrementally. After the first response byte, it does not
switch credentials or replay the request.

### Chat Completions

```http
POST /v1/chat/completions
Content-Type: application/json
```

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

Set `stream: true` to receive standard Chat Completions SSE chunks ending in
`data: [DONE]` when supplied by the provider.

### Embeddings

```http
POST /v1/embeddings
Content-Type: application/json
```

```python
result = client.embeddings.create(
    model="embedding-default",
    input=["first document", "second document"],
)
vectors = [item.embedding for item in result.data]
```

The route must resolve to an embedding-capable upstream model. RelayDock stores
usage metadata, not the input text, under the default logging policy.

## Control-plane authentication

Login endpoints (each creates a session in its corresponding cookie realm):

```text
POST /api/auth/login
POST /api/admin/auth/login
POST /api/console/auth/login
```

Body:

```json
{
  "email": "admin@example.com",
  "password": "a secret supplied by the operator"
}
```

The response includes the user, access-session expiration, and a CSRF token.
Each endpoint uses an isolated cookie realm so the Admin and Console UIs can
remain signed in concurrently on the same host:

| Endpoint realm | Access (HttpOnly) | Refresh (HttpOnly) | CSRF (readable) |
| --- | --- | --- | --- |
| `/api/auth/*` | `relayedock_session` | `relayedock_refresh` | `relayedock_csrf` |
| `/api/admin/auth/*` | `relayedock_admin_session` | `relayedock_admin_refresh` | `relayedock_admin_csrf` |
| `/api/console/auth/*` | `relayedock_console_session` | `relayedock_console_refresh` | `relayedock_console_csrf` |

The full signed tokens are not returned in JSON or exposed to browser
JavaScript.

Browser clients send `X-CSRF-Token` equal to the applicable realm's CSRF cookie
on every state-changing cookie-authenticated request; requests without a
matching double-submit value are rejected. The bundled Nginx keeps `/api/*`
same-origin. A valid signed access JWT is also accepted as a Bearer credential
for a trusted non-browser integration, but the normal login endpoint
intentionally delivers the session only by HttpOnly cookie.

```text
POST /api/auth/refresh          rotate shared access and refresh cookies (CSRF required)
POST /api/admin/auth/refresh    rotate administrator access and refresh cookies
POST /api/console/auth/refresh  rotate console access and refresh cookies
GET  /api/auth/me               current user
POST /api/auth/logout           clear the shared-realm cookies
POST /api/admin/auth/logout     clear the administrator-realm cookies
POST /api/console/auth/logout   clear the console-realm cookies
```

### SaaS account lifecycle

Public account endpoints are available under `/api/console/auth`, with
compatibility aliases under `/api/auth` and `/api/admin/auth`:

| Method and suffix | Purpose |
| --- | --- |
| `GET /config` | Return `CLOSED`, `INVITE_ONLY`, or `PUBLIC` registration mode |
| `POST /register` | Register with email/password and optional registration code or organization invitation |
| `POST /verify-email` | Consume an expiring one-time verification token |
| `POST /resend-verification` | Queue a replacement verification email; response is enumeration-safe |
| `POST /forgot-password` | Queue a reset link; response is enumeration-safe |
| `POST /reset-password` | Consume the reset token and revoke all prior sessions |
| `GET /invitations/{token}` | Preview an organization invitation with masked email |
| `POST /invitations/{token}/accept` | Accept once; cannot create a new user in `CLOSED` mode |
| `POST /invitations/{token}/reject` | Reject once |

Authenticated realm endpoints add `change-password`,
`logout-other-sessions`, `mfa/status`, `mfa/setup`, `mfa/confirm`,
`mfa/disable`, and the signed-in user's `/auth/organization-invitations`
list/respond operations. Access and refresh JWTs carry a database
`session_version`; every
password or security-state transition invalidates older values.

Administrator organization invitation APIs are
`GET/POST /api/admin/organizations/{organizationID}/invitations` and
`DELETE .../invitations/{invitationID}`. Registration-code management is under
`/api/admin/registration-invites`; dead-letter inspection/requeue is under
`/api/admin/email-outbox`. Secrets and codes are returned only at creation.

Administrator login accepts optional `mfa_code`. When deployment policy
requires MFA but the administrator is not enrolled, the login response sets
`mfa_enrollment_required: true` and the session is restricted to MFA
enrollment endpoints. See [account-lifecycle.md](account-lifecycle.md).

## Administrator endpoints

All paths require an authenticated `SUPER_ADMIN` or `ADMIN` principal.

| Method and path | Purpose |
| --- | --- |
| `GET /api/admin/dashboard` | Global request, token, cost, latency, and credential-health summary |
| `GET/POST /api/admin/providers` | List/create provider configurations |
| `PUT/DELETE /api/admin/providers/{id}` | Update/delete a provider; built-in OpenAI cannot be deleted |
| `POST /api/admin/providers/{id}/sync-models` | Synchronize official models using an explicitly selected credential |
| `GET/POST /api/admin/credentials` | List/create encrypted provider credentials |
| `POST /api/admin/credentials/import` | Import 1–25 already-issued official API credentials from JSON; each is encrypted and validation failures are stored disabled |
| `POST /api/admin/credentials/bulk` | Enable, disable, delete, move, or health-check selected stored credentials |
| `PUT/DELETE /api/admin/credentials/{id}` | Update/delete a credential; omitted `secret` preserves the existing secret |
| `POST /api/admin/credentials/{id}/test` | Validate through the provider's public Models API |
| `PATCH /api/admin/credentials/{id}/status` | Set `ACTIVE` or `DISABLED` |
| `GET/POST /api/admin/credential-groups` | List/create groups |
| `DELETE /api/admin/credential-groups/{id}` | Delete a group when not protected by route references |
| `PUT/DELETE /api/admin/credential-groups/{id}/members/{credentialId}` | Add/update/remove membership weight and priority |
| `GET /api/admin/models` | List synchronized model metadata |
| `GET/POST /api/admin/models/{id}/prices` | List/add immutable effective-dated pricing versions |
| `GET/POST /api/admin/model-routes` | List/create aliases |
| `GET/POST /api/admin/routes` | UI-compatible alias for listing/creating model routes |
| `PUT/DELETE /api/admin/model-routes/{id}` | Update/delete an alias route |
| `GET/POST /api/admin/api-keys` | List keys or issue a key for a specified user |
| `PATCH /api/admin/api-keys/{id}/status` | Set `ACTIVE`, `DISABLED`, or `REVOKED` |
| `DELETE /api/admin/api-keys/{id}` | Revoke a key |
| `GET/POST /api/admin/users` | List/create users |
| `PATCH /api/admin/users/{id}/status` | Set `ACTIVE` or `DISABLED` |
| `GET /api/admin/request-logs` | Privileged request metadata, including credential/upstream request ID |
| `GET /api/admin/usage?days=30` | Global daily aggregates |
| `GET /api/admin/audit-logs` | Administrative mutation history |
| `GET /api/admin/alerts` | Operational alerts |
| `GET/PUT /api/admin/settings` | Read/update the allowlisted system settings |

When `POST /api/admin/users` omits `password`, RelayDock generates a temporary
password and returns it once alongside the user record. Store or deliver it
through an approved channel; it is never available from later reads.

Credential creation accepts:

```json
{
  "provider_id": "uuid",
  "name": "OpenAI production project",
  "credential_type": "api_key",
  "secret": "provider-secret-entered-once",
  "organization_id": null,
  "project_id": "proj_...",
  "group_id": "uuid",
  "priority": 10,
  "weight": 100,
  "max_concurrency": 10,
  "validate": true,
  "save_disabled": false
}
```

If validation fails, `save_disabled: true` stores the encrypted credential in
`DISABLED` state for later correction. Read responses never include `secret` or
ciphertext.

Safe JSON import accepts only already-issued official API credentials:

```json
{
  "validate": true,
  "credentials": [
    {
      "name": "Authorized project A",
      "api_key": "provider-key-entered-once",
      "project_id": "proj_...",
      "group_id": "uuid",
      "weight": 100,
      "priority": 10,
      "max_concurrency": 10
    }
  ]
}
```

The endpoint is capped at 25 items, never accepts usernames/passwords, cookies,
consumer sessions, registration inputs, or browser tokens, and never echoes an
API key. Bulk actions operate only on stored credential IDs; they do not create
provider accounts or evade upstream limits.

Model synchronization body:

```json
{ "credential_id": "uuid" }
```

Route creation body:

```json
{
  "alias": "gpt-default",
  "provider_id": "uuid",
  "upstream_model": "an-official-model-id",
  "credential_group_id": "uuid",
  "fallback_group_id": null,
  "routing_policy": "priority_weighted",
  "fallback_config": {}
}
```

`fallback_group_id` is a bounded alternate credential group on the same
physical route. Any primary scheduler error may select it before dispatch. If
the first primary upstream call instead fails with a non-cancellation,
non-deadline connection error before an HTTP response is received, the gateway
may issue at most one additional call through that fallback group. It does not
fallback after any HTTP response (`4xx`/`5xx` included), on cancellation or
deadline, or after SSE response bytes begin.

No-response connection errors are ambiguous: the first upstream may already
have processed the request. Both attempts can therefore generate output or
incur Provider cost. RelayDock request idempotency prevents a second client
admission/funding operation, but does not guarantee upstream deduplication
between internal attempts. Integrations must tolerate duplicate upstream
processing and place non-idempotent external side effects behind their own
durable idempotency boundary.

## User console endpoints

These paths are automatically scoped to the authenticated user:

| Method and path | Purpose |
| --- | --- |
| `GET /api/console/overview` | Today/month-to-date summary, recent requests, active keys, and available models |
| `GET /api/console/dashboard` | User-only usage summary |
| `GET/POST /api/console/api-keys` | List/issue the user's RelayDock keys |
| `DELETE /api/console/api-keys/{id}` | Revoke one of the user's keys |
| `GET /api/console/models?project_id=...` | List authorized aliases plus non-secret `provider_id`/`upstream_model` keys used to join the current-region public price catalog; credential groups and endpoints remain hidden |
| `GET /api/console/usage?period=30d` | User-only totals, daily series, and per-model breakdown |
| `GET /api/console/request-logs` | User-only logs with credential, scheduler, and upstream IDs removed |
| `GET /api/console/logs` | UI-compatible alias for the same redacted request logs |

API-key creation body:

```json
{
  "project_id": "project-uuid",
  "name": "local development",
  "environment": "test",
  "expires_at": null,
  "rate_limit_rpm": 60,
  "rate_limit_tpm": 100000,
  "monthly_token_limit": null,
  "monthly_cost_limit": null,
  "allowed_models": ["gpt-default", "embedding-default"]
}
```

The `201` response contains metadata plus the full secret in `key` (and the
legacy compatibility alias `secret`). It is shown once. Later list responses contain
only metadata and `key_prefix`.

## V2.0 multi-project endpoints

V2.0 tenant resources are exposed below both control-plane realms:

- administrator base: `/api/admin`; every call requires `ADMIN` or
  `SUPER_ADMIN`, and tenant membership does not restrict the administrator;
- console base: `/api/console`; reads and mutations are checked against the
  authenticated user's active organization/project memberships. An
  inaccessible tenant resource returns `404` to avoid cross-project discovery.

The following table lists the suffix once because the method and body contract
are identical under both bases. For example, the first entry represents both
`GET /api/admin/organizations` and `GET /api/console/organizations`.

| Method and suffix under both bases | Console minimum role | Purpose |
| --- | --- | --- |
| `GET /organizations` | active organization membership | List visible organizations (`limit`, `offset`) |
| `POST /organizations` | authenticated user | Create an organization; the actor becomes its owner |
| `GET /organizations/{organizationID}` | organization `VIEWER` | Read organization metadata |
| `PUT /organizations/{organizationID}` | organization `ADMIN` | Update name, slug, status, or metadata |
| `DELETE /organizations/{organizationID}` | organization `OWNER` | Archive the organization |
| `GET/POST /organizations/{organizationID}/members` | `VIEWER` / `ADMIN` | List or add an organization member |
| `PUT/DELETE /organizations/{organizationID}/members/{userID}` | organization `ADMIN` | Replace membership role/status or disable membership |
| `GET/POST /organizations/{organizationID}/projects` | `VIEWER` / `ADMIN` | List visible projects or create a project |
| `GET /projects/{projectID}` | project `VIEWER` | Read project metadata |
| `PUT/DELETE /projects/{projectID}` | project `ADMIN` | Update or archive a project |
| `GET/POST /projects/{projectID}/members` | project `VIEWER` / `ADMIN` | List or add a project member |
| `PUT/DELETE /projects/{projectID}/members/{userID}` | project `ADMIN` | Replace membership role/status or disable membership |
| `GET/POST /projects/{projectID}/routes` | project `VIEWER` / `ADMIN` | List or grant a global model route to the project |
| `PUT/DELETE /projects/{projectID}/routes/{routeID}` | project `ADMIN` | Update or remove a project route grant |
| `GET/POST /projects/{projectID}/budgets` | project `VIEWER` / `ADMIN` | List or create a daily/monthly budget policy |
| `PUT/DELETE /projects/{projectID}/budgets/{policyID}` | project `ADMIN` | Update or delete a budget policy |
| `GET /projects/{projectID}/budget-usage?period=MONTHLY` | project `VIEWER` | Current `DAILY` or `MONTHLY` usage window |
| `GET /projects/{projectID}/budget-events` | project `VIEWER` | Bounded event list (`from`, `to`, `limit`, `offset`) |
| `GET/POST /projects/{projectID}/webhooks` | project `VIEWER` / `ADMIN` | List or create signed Webhook endpoints |
| `PUT/DELETE /projects/{projectID}/webhooks/{webhookID}` | project `ADMIN` | Update or disable an endpoint |
| `POST /projects/{projectID}/webhooks/{webhookID}/test` | project `ADMIN` | Enqueue a durable `webhook.test` delivery |
| `GET /projects/{projectID}/webhook-deliveries` | project `VIEWER` | List outbox deliveries, optionally filtered by `status` |
| `POST /projects/{projectID}/webhook-deliveries/{deliveryID}/retry` | project `ADMIN` | Return a `DEAD` delivery to `PENDING` |
| `GET /projects/{projectID}/usage/export` | project `VIEWER` | Download bounded, formula-neutralized CSV usage |
| `POST /api-keys/{keyID}/rotate` | key owner plus project `DEVELOPER` | Create a new active version and place the old version in grace |
| `POST /api-keys/{keyID}/finalize` | key owner plus project `DEVELOPER` | Revoke one or all grace versions immediately |

The Console also exposes `GET /api/console/projects`, which flattens all
projects visible to the current user and adds `organization_name` and
`organization_slug`. Existing Console endpoints accept `project_id` for V2
scope: `overview`, `models`, `api-keys`, `usage`, `request-logs`, and `logs`.
When supplied, access is checked before data is returned; project API-key
creation requires `DEVELOPER` and project reads require `VIEWER`.

V2 administrator-only additions are:

| Method and path | Purpose |
| --- | --- |
| `GET /api/admin/credentials/{id}/tags` | Read normalized scheduler tags for an authorized provider credential |
| `PUT /api/admin/credentials/{id}/tags` | Atomically replace tags with `{ "tags": [...] }` |
| `POST /api/admin/alerts/{alertID}/acknowledge` | Set `ACKNOWLEDGED`, timestamp, and authenticated `acknowledged_by` actor |

### Project route and credential-tag semantics

A project route grant references an administrator-defined `model_route_id` and
may override its public alias. `routing_config` currently recognizes these
arrays:

```json
{
  "model_route_id": "uuid",
  "alias": "gpt-default",
  "enabled": true,
  "routing_config": {
    "required_credential_tags": ["region:apac"],
    "excluded_credential_tags": ["maintenance"]
  }
}
```

`DELETE` tombstones the grant instead of removing its database row. The alias
immediately disappears from Admin/Console catalogs and Gateway resolution, but
historical request and usage rows retain their foreign-key reference for
auditability. Granting the same model route/alias again clears the tombstone.

All required tags must be present and no excluded tag may be present. A tag in
both arrays intentionally makes every credential ineligible. A model alias not
granted to the key's active project returns `404 model_not_found` before any
upstream request. The API-key `allowed_models` check runs after the project
grant and returns `403 model_not_allowed`.

### Budgets

Budget policy input contains `name`, `period` (`DAILY` or `MONTHLY`), at least
one non-negative `token_limit` or `cost_limit`, `alert_threshold` from `0` to
`1` (default `0.8`), `enforce_hard_limit`, and `status` (`ACTIVE` or
`DISABLED`). Threshold and exceeded events are deduplicated per policy period.
An already-reached hard cost limit, or a token limit exceeded by recorded usage
plus the request estimate, returns `403 project_budget_exceeded` before
upstream dispatch. Because output usage and concurrent requests are not
reserved transactionally in V2.0, provider-side budgets remain the final spend
boundary.

### API-key rotation

`POST .../rotate` accepts `grace_period_seconds` (the compatibility alias
`grace_seconds` is also accepted), defaults to `300`, and permits `30` through
`86400`. The `201` response shows the replacement in `key`/`secret` once. The
prior version remains valid as `GRACE` until its deadline or finalization, while
the replacement is immediately `ACTIVE`. `POST .../finalize` accepts optional
`{ "version": 1 }`; omitting `version` revokes every grace version for that
logical key. Disabling/revoking the logical key, user, organization, project,
or required membership invalidates every version immediately.

### Webhooks and exports

Supported Webhook event types are `webhook.test`, `budget.warning`,
`budget.exceeded`, and `api_key.rotated`. Creation requires a validated target,
at least one event type, and a signing secret of at least 16 characters; if the
secret is omitted, RelayDock generates and returns it once. Delivery headers,
signature input, retry/dead-letter behavior, and the event envelope are defined
in [v2.md](v2.md). Redirects and unsafe targets are rejected by default.

Usage export accepts `from` and `to` as RFC3339 timestamps or `YYYY-MM-DD`
dates, defaults to 30 days, and rejects windows longer than 366 days. Admin
exports contain the whole project; Console exports are additionally restricted
to the current user. CSV contains request/usage metadata only—never prompts,
responses, provider secrets, or RelayDock key values.

## ModelDock V3 administration

All paths below are relative to `/api/admin` and require an administrator
session plus the existing CSRF protection for mutations.

| Method and path | Purpose |
| --- | --- |
| `POST /models`, `PUT /models/{id}`, `DELETE /models/{id}` | Create, update, or disable registry models and legacy declared quality/latency fields (not platform ranking inputs) |
| `GET /provider-quality` | List platform-measured policies, grades, routing multipliers, ramp caps, and circuit states |
| `PUT /providers/{id}/quality-policy` | Replace the administrator-owned quality/probe policy |
| `POST /providers/{id}/quality/evaluate` | Evaluate the current immutable evidence window immediately |
| `POST /providers/{id}/quality/circuit-reset` | Audited move to half-open recovery; does not force-close |
| `POST /providers/{id}/supplier-link` | Link an approved supplier and atomically start controlled ramp-up |
| `GET /provider-quality/sla-events` | List opened/resolved Provider SLA events |
| `GET/POST /provider-quality/price-verifications` | List or append independent exact-decimal official price evidence |

The paths above are under `/api/admin`. Supplier declarations, Marketplace
uptime, and legacy model quality scores are not platform quality evidence and
are not routing/ranking inputs. Price-verification POST requires an
`Idempotency-Key`; see [provider-quality.md](provider-quality.md).
| `GET/POST /routing-rules` | List or create project intelligent-routing aliases |
| `PUT/DELETE /routing-rules/{id}` | Update or remove a routing rule |
| `GET/POST /marketplace/providers` | List or create Marketplace provider listings |
| `PUT/DELETE /marketplace/providers/{id}` | Review, verify, suspend, or remove a listing |
| `GET /marketplace/launch-reviews` | List versioned Marketplace acceptance reviews and all current gates |
| `POST /marketplace/providers/{id}/launch-reviews` | Idempotently open a review for a supplier-linked listing |
| `POST /marketplace/launch-reviews/{id}/evaluate` | Re-evaluate platform database evidence and append gate events |
| `PUT /marketplace/launch-reviews/{id}/gates/{gateCode}` | Attest one operational drill gate with a reviewed evidence reference |
| `POST /marketplace/launch-reviews/{id}/approve` | Second-administrator release approval; all 23 gates must pass |
| `POST /marketplace/providers/{id}/lifecycle` | Start canary, suspend, resume, emergency-cutover, or exit |
| `GET /marketplace/providers/{id}/lifecycle-events` | List immutable Marketplace lifecycle events |
| `GET/PUT /marketplace/payout-readiness/{supplierID}` | Read or review contract, tax, payment, and security payout gates |
| `GET/POST /teams` | List or create organization teams with token/cost limits |
| `PUT/DELETE /teams/{id}` | Update or archive a team |
| `GET /teams/{id}/members` | List team membership |
| `PUT/DELETE /teams/{id}/members/{userID}` | Set or remove a team member |
| `GET /wallets` | List organization wallets |
| `PUT /wallets/{id}` | Set `PREPAID`/`POSTPAID`, status, and credit limit |
| `GET /wallets/{id}/transactions` | List the immutable wallet ledger |
| `GET /wallets/{id}/funding-operations` | List reservation/settlement state and usage provenance |
| `GET /wallets/{id}/journals` | List balanced immutable debit/credit journals |
| `POST /wallets/{id}/topups` | Legacy internal, non-refundable balance adjustment; requires an idempotency key and is not customer-payment evidence |
| `POST /wallets/{id}/adjustments` | Apply an audited signed balance adjustment |

Supplier-backed Provider routes fail closed unless the listing is either a
foundation-gated `CANARY` with an active review or `ACTIVE` with an approved
review and all gates passed. The route-selection check is repeated in the
dispatch transaction. Existing first-party Providers with no supplier link keep
their prior `/v1` behavior. Listing uptime, `verified`, price JSON, and supplier
usage are declarations and cannot satisfy an automated gate. See
[Marketplace launch acceptance](marketplace/launch-acceptance.md).

### Verified recharge and payment operations

Console endpoints use the existing authenticated `/api/console` session and
organization membership checks. `POST /organizations/{organizationID}/recharge-orders`
requires an exact decimal amount, currency, region, adapter name, and
idempotency key. The returned payment instructions never mean the wallet has
been credited. `GET .../recharge-orders/{orderID}` is safe for a success page to
poll; `POST .../{orderID}/query` performs a server-side adapter query.

The public `POST /api/payments/webhooks/{provider}` endpoint accepts only a
provider-specific signed event within the configured timestamp window. Admin
endpoints list orders, approve manual-transfer evidence, issue full refunds,
and persist immutable reconciliation records. See [payments.md](payments.md)
and [openapi.yaml](openapi.yaml) for the request schemas and signature contract.
| `POST /funding-operations/{id}/late-usage` | Idempotently post authoritative late usage as a difference journal |
| `POST /funding-operations/{id}/reversals` | Reverse a settlement with a new audited journal |

### Commercial pricing

| Endpoint | Purpose |
| --- | --- |
| `POST /api/pricing/quote` | Authenticated quote for organization, model, and estimated token counts |
| `POST /api/admin/pricing/quotes` | Administrator quote endpoint |
| `POST /api/admin/pricing/quote` | Deprecated compatibility alias for the administrator quote endpoint |
| `GET/POST /api/admin/pricing/provider-cost-price-books` | List/publish provider cost versions |
| `POST /api/admin/pricing/provider-cost-changes/manual` | Submit one exact-decimal manual Provider cost change |
| `POST /api/admin/pricing/provider-cost-changes/fetch` | Fetch an allowlisted official HTTPS JSON price feed |
| `POST /api/admin/pricing/provider-cost-changes/import-csv` | Atomically import a bounded `text/csv` price batch |
| `GET/POST /api/admin/pricing/byok-service-fee-policies` | List/publish append-only BYOK service-fee policies |
| `DELETE /api/admin/pricing/byok-service-fee-policies/{id}` | Disable a BYOK service-fee policy |
| `GET/POST /api/admin/pricing/customer-retail-price-books` | List/publish customer or system retail versions |
| `GET/POST /api/admin/pricing/organization-price-plans` | List/publish subscription and organization overrides |
| `GET/POST /api/admin/pricing/margin-policies` | Model/provider/organization minimum-margin policies |
| `GET/POST /api/admin/pricing/promotion-credits` | Non-refundable promotion grants separate from wallet cash |

Money values in this domain are exact decimal strings. Pricing priority is
organization override, subscription plan, customer price book, then system
default. A forced below-margin publication requires `force_override=true` plus
`confirmation=CONFIRM_NEGATIVE_MARGIN_OVERRIDE` and is audited. See
[pricing.md](pricing.md). Retail publication currently requires the same
currency as the matching provider cost; promotion grants and wallet mutations
require idempotency keys, and reusing a key with different data returns `409`.

### Subscriptions

| Endpoint | Purpose |
| --- | --- |
| `GET /api/admin/subscription-plans` | List enabled/disabled templates and current frozen versions |
| `POST /api/admin/subscription-plans` | Create a standard or enterprise-contract template |
| `GET/POST /api/admin/subscription-plans/{planID}/versions` | List or create immutable-version candidates |
| `POST /api/admin/plan-versions/{versionID}/freeze` | Freeze and publish a complete version |
| `GET/POST /api/admin/coupons` | List/create subscription-only coupons |
| `GET /api/{admin|console}/organizations/{organizationID}/subscription` | Current version and effective server-side entitlements |
| `POST /api/{admin|console}/organizations/{organizationID}/subscription/change` | Immediate or period-end upgrade/downgrade; requires idempotency key |
| `POST /api/{admin|console}/organizations/{organizationID}/subscription/cancel` | Immediate or period-end cancellation; requires idempotency key |
| `GET /api/{admin|console}/organizations/{organizationID}/subscription-invoices` | Independent subscription reconciliation stream |
| `GET /api/{admin|console}/organizations/{organizationID}/subscription-events` | Immutable lifecycle audit events |
| `POST /api/admin/subscription-invoices/{invoiceID}/pay` | Record verified subscription payment and post separate journal |
| `POST /api/admin/subscription-invoices/{invoiceID}/fail` | Record renewal failure and begin past-due/grace lifecycle |

All subscription amounts are exact decimal strings. Every version has
`token_billing_mode=METERED_SEPARATE`; Token request charges still use the
pricing snapshot/funding/wallet APIs. See [subscriptions.md](subscriptions.md).

The built-in model aliases `auto`, `auto:cost`, `auto:quality`, and
`auto:balanced` invoke intelligent routing. Exact physical aliases continue to
use manual routing. See [modeldock.md](modeldock.md) for scoring and billing
semantics.

## Operational endpoints

Both listeners expose:

```text
GET /healthz   process liveness
GET /startupz  startup completion probe
GET /readyz    PostgreSQL/Redis readiness; returns 503 while draining
GET /metrics   Prometheus text exposition
GET /api/version product, RelayDock compatibility identity, semantic version,
                 full release commit SHA, and source-commit build timestamp
```

`/api/version` keeps `name: RelayDock` for existing clients and adds
`product: ModelDock`, `compatibility_name`, `commit`, and `build_time` fields.
Development builds report `unknown` provenance; release images inject immutable
values and mirror them in OCI labels.

Restrict `/metrics` at the reverse proxy in production because operational
labels/counters should not be internet-visible.
Inference POSTs also accept an optional `Idempotency-Key` (maximum 200
characters). Replaying the same logical request never creates another
reservation or charge. See [funding-ledger.md](funding-ledger.md).

Provider governance adds the Provider kill-switch, Provider cost-change review,
and organization-scoped BYOK credential endpoints. Manual/API/CSV cost inputs
always enter pending approval; approval publishes an append-only,
effective-dated price. Gateway policy failures use stable redacted error codes
without exposing contracts, region lists, budgets, or credential identifiers.
See [provider-governance.md](provider-governance.md).

## Observability, status, and support

Public `GET /status` and `GET /api/status` expose only customer-safe component
and incident state. Authenticated console users use `/api/console/status` and
`/api/console/support/tickets`; administrators use
`/api/admin/status/summary`, `/api/admin/status/events`,
`/api/admin/observability`, and `/api/admin/support/tickets`.

`GET /api/admin/observability/requests/{requestID}` is the investigation join:
it returns route, Provider attempts, token usage, exact-decimal pricing/funding
evidence, wallet transactions, and ledger journal IDs. It never returns API
keys, Provider secrets, prompts, responses, or payment credentials. See
[observability-operations.md](observability-operations.md).
## Supplier applications

Authenticated console users can submit supplier evidence under `/api/console/suppliers`. Administrators review it under `/api/admin/suppliers`. Supplier credentials and payout account values are write-only inputs and are never returned. `APPROVED` is accepted only by the administrator review endpoint after the server verifies KYB, contract, endpoint-isolation, and security-questionnaire prerequisites; a supplier cannot self-approve. See [supplier-onboarding.md](supplier-onboarding.md) and [openapi.yaml](openapi.yaml) for the complete request/response contract.

Supplier finance endpoints are under `/api/console/suppliers/{supplierID}/payables|settlements|bills|appeals` and `/api/admin/supplier-payables|supplier-settlements|supplier-bills|supplier-appeals`. Exact amounts are decimal strings. Supplier bills are immutable declarations only; payout approval requires administrator-imported Provider statement lines matched to platform-settled usage, verified tax/invoice status, no open appeal, and a second administrator when the batch was manually created. Payout retries keep the original idempotency key. See [supplier-settlement.md](supplier-settlement.md).

Every payout adapter other than the built-in `sandbox` is considered
production. Approval, queue claim, completion, and a PostgreSQL trigger require
all four supplier payout-readiness reviews to be `APPROVED`. A supplier cannot
write those review statuses.
