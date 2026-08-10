# API guide

RelayDock exposes two independent HTTP surfaces:

| Surface | Default base URL | Authentication |
| --- | --- | --- |
| Gateway/data plane | `http://127.0.0.1:8080` | RelayDock API key (`rdk_live_...` or `rdk_test_...`) |
| Control plane | `http://127.0.0.1:8081` | HttpOnly access/refresh cookies plus CSRF |

The machine-readable contract is [openapi.yaml](openapi.yaml). OpenAI-compatible
request/response bodies are intentionally extensible because the official API
may add optional fields and streaming event types.

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
| `403` | `model_not_allowed`, `quota_exceeded`, `project_budget_exceeded`, `insufficient_permissions`, `csrf_failed` | Authenticated but not admitted |
| `404` | `model_not_found`, `not_found` | Route/resource not found |
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

## User console endpoints

These paths are automatically scoped to the authenticated user:

| Method and path | Purpose |
| --- | --- |
| `GET /api/console/overview` | Today/month-to-date summary, recent requests, active keys, and available models |
| `GET /api/console/dashboard` | User-only usage summary |
| `GET/POST /api/console/api-keys` | List/issue the user's RelayDock keys |
| `DELETE /api/console/api-keys/{id}` | Revoke one of the user's keys |
| `GET /api/console/models` | List configured route aliases |
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

## Operational endpoints

Both listeners expose:

```text
GET /healthz   process liveness
GET /readyz    PostgreSQL and Redis readiness
GET /metrics   Prometheus text exposition
```

Restrict `/metrics` at the reverse proxy in production because operational
labels/counters should not be internet-visible.
