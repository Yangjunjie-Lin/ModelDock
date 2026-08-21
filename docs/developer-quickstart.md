# Developer quickstart: price check to first request

This guide is the public integration contract for the current ModelDock
release. ModelDock keeps the RelayDock-compatible `/v1` data plane,
`rdk_live_*` / `rdk_test_*` keys, and `RELAYDOCK_*` deployment settings.
Examples use placeholders only. Never paste an upstream Provider key into a
ModelDock client.

Two HTTP origins are shown in local examples:

| Surface | Local URL | Purpose |
| --- | --- | --- |
| Public and console control plane | `http://127.0.0.1:8081` | Catalog, actual published prices, registration, billing, and usage |
| OpenAI-compatible gateway | `http://127.0.0.1:8080/v1` | Models, Responses, Chat Completions, and Embeddings |

Hosted deployments may publish both behind one HTTPS origin. Use the URLs
shown by that deployment; do not infer an API hostname from a marketing
hostname.

## 1. Check availability and the published price before purchase

Choose the customer's actual ISO 3166-1 alpha-2 region and the displayed
ISO-4217 currency. The public endpoints require no cookie or API key.

```bash
curl --fail-with-body \
  "http://127.0.0.1:8081/api/public/catalog/models?region=CN&currency=CNY"

curl --fail-with-body \
  "http://127.0.0.1:8081/api/public/catalog/providers?region=CN"

curl --fail-with-body \
  "http://127.0.0.1:8081/api/public/pricing?region=CN&currency=CNY"
```

The pricing response deliberately separates:

- `subscription_plans[].subscription_fee`, which buys the finite
  product entitlements in that version;
- `token_prices[]`, which are metered separately for every request;
- `payment_fees[]`, which describe an enabled payment channel or platform fee;
- `commercial_terms.bonus_credit_amount`, whose non-refundable status is
  stated by `bonus_non_refundable`;
- `commercial_terms.subscription_tax_included`,
  `token_tax_included`, and `tax_disclosure`;
- `refund_summary` and `refund_policy_url`; and
- per-model/per-Provider region availability.

Every `token_prices[]` item also includes nested `availability` with
`available`, `region`, `status`, and a public-safe `reason_code`. Treat the row
as callable in the selected region only when that nested value is available.

Money is returned as exact decimal JSON strings. `unit` is the number of
Tokens to which a Token price applies. A missing `pricing` object, an
unavailable model, an unavailable Provider, a currency mismatch, or missing
commercial terms is not permission to estimate a sale price: the UI and
checkout must fail closed and ask the user to choose an available combination.
Public availability is a pre-purchase disclosure, not a promise that every
project can call the model: project route grants, eligible credentials,
dispatch-time policy, upstream health, and capacity are checked separately.
`terms_configured` and `payment_fees_configured` state whether the corresponding
effective disclosures were deliberately published; `false` must block checkout
even if an empty array would otherwise look like a zero fee.
`payment_region_supported` is true only when the deployment payment-region
allowlist, a runtime-admitted adapter, and that adapter's current approved
`PAYMENT_CHANNEL` fee evidence all agree. A configured row can still be
`legal_review_status: PENDING`, so the other configured flags are not
counsel-approval claims.
Prices are effective-dated. Re-read the public price immediately before a
purchase or long-running batch.

`legal_review_required: true` means deployment-specific legal/tax wording is
still awaiting professional review. It does not change ledger arithmetic, but
the deployment must not present that draft wording as final legal advice.
`bonus_credit_amount` is a disclosure, not an automatic account credit. A
non-zero amount is meaningful only when the deployment has enabled a real,
eligibility-reviewed promotion issuance flow; the user's persisted promotion
credit and ledger are authoritative for spendable value.

## 2. Complete console onboarding

The normal self-service sequence is:

1. register under `/api/console/auth/register` when public registration is
   enabled;
2. open the one-time verification link sent by the configured mail provider;
3. sign in, create or use the initial organization/project, and confirm the
   billing region;
4. choose a non-enterprise plan version, or contact sales for a manually
   reviewed Enterprise contract;
5. create and complete an enabled recharge order if prepaid funds are needed;
6. create a project-scoped API key and copy its one-time plaintext;
7. run the copied example; and
8. inspect project usage plus the organization's exact charge history.

The console reads the server-derived checklist at:

```http
GET /api/console/onboarding?organization_id=<uuid>&project_id=<uuid>
```

The checklist is evidence, not a client-controlled progress flag. Registration,
email verification, organization creation, plan selection, verified recharge,
API-key creation, an HTTP-2xx first API call with terminal settlement, and
recorded usage/billing evidence that the Console can display are derived from
transactional records for the selected project. A browser must not mark a step
complete locally.

API keys are displayed once and stored only as a prefix plus keyed digest. If
the value is lost, rotate or issue another key; it cannot be recovered.

## 3. First ordinary request with curl

```bash
export RELAYDOCK_API_KEY="rdk_test_replace_with_the_one_time_value"
export RELAYDOCK_BASE_URL="http://127.0.0.1:8080/v1"

curl --fail-with-body "$RELAYDOCK_BASE_URL/responses" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: quickstart-response-0001" \
  -d '{"model":"gpt-default","input":"Reply with one short greeting."}'
```

Use an alias listed by authenticated `GET /v1/models` and shown as available
for the customer's region in the public catalog. The `Idempotency-Key` above is
an example value, not a shared constant: generate a fresh logical-operation ID
for every new request.

Chat Completions remains available for existing clients:

```bash
curl --fail-with-body "$RELAYDOCK_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: quickstart-chat-0001" \
  -d '{"model":"gpt-default","messages":[{"role":"user","content":"Reply with one short greeting."}]}'
```

## 4. Python and OpenAI SDK `base_url`

```python
import os
import uuid
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["RELAYDOCK_API_KEY"],
    base_url=os.getenv("RELAYDOCK_BASE_URL", "http://127.0.0.1:8080/v1"),
)

response = client.responses.create(
    model="gpt-default",
    input="Reply with one short greeting.",
    extra_headers={"Idempotency-Key": str(uuid.uuid4())},
)
print(response.output_text)
```

Only `base_url` and the downstream key change. Do not replace public OpenAI SDK
types with ModelDock-specific response wrappers.

## 5. JavaScript and TypeScript

JavaScript (Node.js, ESM):

```javascript
import crypto from "node:crypto";
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.RELAYDOCK_API_KEY,
  baseURL: process.env.RELAYDOCK_BASE_URL ?? "http://127.0.0.1:8080/v1",
});

const result = await client.chat.completions.create(
  {
    model: "gpt-default",
    messages: [{ role: "user", content: "Reply with one short greeting." }],
  },
  { headers: { "Idempotency-Key": crypto.randomUUID() } },
);
console.log(result.choices[0]?.message.content);
```

TypeScript uses the same SDK surface:

```typescript
import OpenAI from "openai";

const client: OpenAI = new OpenAI({
  apiKey: process.env.RELAYDOCK_API_KEY,
  baseURL: process.env.RELAYDOCK_BASE_URL ?? "http://127.0.0.1:8080/v1",
});

const response = await client.responses.create({
  model: "gpt-default",
  input: "Return the word ready.",
});

console.log(response.output_text);
```

Browser-side use is discouraged because it exposes an API key. Call ModelDock
from a trusted backend and apply your own end-user authentication.

## 6. Server-sent events (SSE)

Disable client/proxy buffering when testing raw SSE:

```bash
curl --no-buffer --fail-with-body "$RELAYDOCK_BASE_URL/responses" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -H "Idempotency-Key: quickstart-stream-0001" \
  -d '{"model":"gpt-default","input":"Count from one to three.","stream":true}'
```

Python SDK:

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

ModelDock forwards events incrementally and propagates cancellation. Once an
HTTP response has been received or response-stream bytes have started,
ModelDock does not switch credentials or replay the request. A pre-response
connection failure can follow the bounded fallback behavior in section 14.

## 7. Embeddings

```bash
curl --fail-with-body "$RELAYDOCK_BASE_URL/embeddings" \
  -H "Authorization: Bearer $RELAYDOCK_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: quickstart-embedding-0001" \
  -d '{"model":"embedding-default","input":["first document","second document"]}'
```

```python
vectors = client.embeddings.create(
    model="embedding-default",
    input=["first document", "second document"],
)
print(len(vectors.data))
```

The route and upstream model must advertise embedding capability. ModelDock
does not translate an endpoint a Provider does not implement.

## 8. Errors and request correlation

Errors use the OpenAI-compatible envelope:

```json
{
  "error": {
    "message": "The requested model does not exist or is not enabled for this project.",
    "type": "relayedock_error",
    "param": null,
    "code": "model_not_found"
  }
}
```

Keep `X-RelayDock-Request-Id` from the response when contacting support. Never
send a full API key, Provider key, prompt, payment credential, or cookie.

| HTTP | Typical code | Client action |
| --- | --- | --- |
| `400` | `invalid_json`, `invalid_request`, `invalid_idempotency_key` | Correct the request; do not retry unchanged input. |
| `401` | `invalid_api_key`, `invalid_session` | Replace/rotate the correct downstream credential or sign in again. |
| `402` | `insufficient_balance` | Review the exact quote and complete an enabled recharge path. |
| `403` | `model_not_allowed`, `provider_region_unavailable`, `provider_commercial_unavailable`, `provider_pricing_disabled`, `project_budget_exceeded`, `quota_exceeded` | Change policy/model/region only when contractually permitted; do not bypass the restriction. |
| `404` | `model_not_found`, `not_found` | Refresh the catalog and project grants. |
| `409` | `idempotency_conflict`, `idempotent_replay` | Reconcile by logical operation/request ID; do not issue a new key merely to repeat a chargeable operation. |
| `413` | `request_too_large` | Reduce the bounded request. |
| `429` | `rate_limit_exceeded`, `subscription_rate_limit_exceeded`, `subscription_concurrency_exceeded`, `provider_capacity_unavailable` | Honor `Retry-After` where present and use exponential backoff with jitter. |
| `502` | `provider_error` | Retry only if the application can prove replay safety. |
| `503` | `provider_unavailable`, `service_unavailable` | Back off and consult `/api/public/status`; do not rotate identities or proxies. |

## 9. Rate limits

Limits can apply to the API key (RPM/TPM), user/team/project quotas, project
budget, organization subscription RPM/concurrency, Provider commercial
capacity, and upstream Provider. A `429` is never permission to create another
account, key, IP, or proxy path to evade a limit. Honor `Retry-After`, bound
automatic retries, add jitter, and surface a stable client request ID for
support.

## 10. Idempotency

For every chargeable request, send a unique `Idempotency-Key` of at most 200
characters. ModelDock binds it to the authenticated organization and a request
fingerprint inside the transactional funding reservation:

- concurrent or later reuse with different input returns
  `409 idempotency_conflict`;
- reuse for an already admitted logical request returns
  `409 idempotent_replay`, includes `X-RelayDock-Original-Request-Id`, and
  creates no additional reservation or charge; and
- it does not promise to cache and replay the original model output.

Recharge, subscription, refund, quote, promotion, and other financial mutation
APIs likewise require an idempotency key in the body or `Idempotency-Key`
header as documented by OpenAPI. Persist your operation ID until the resulting
order/journal has been reconciled.

## 11. Billing and usage evidence

Token usage is always metered separately from the subscription fee. Before
dispatch, ModelDock resolves an immutable price version and reserves a maximum
exact-decimal amount. After final usage, it transactionally settles or releases
the reservation and links request log, usage snapshot, wallet transaction, and
balanced journal. The legacy `estimated_cost` field remains compatibility
metadata and is not the payable ledger source.

After a call, inspect:

```text
GET /api/console/usage
GET /api/console/request-logs
GET /api/console/organizations/{organizationID}/finance/usage
GET /api/console/organizations/{organizationID}/finance/balance
```

Amounts shown by finance endpoints are decimal strings. A quote is an
effective-dated estimate; the immutable request price snapshot and journal are
the settlement evidence.

## 12. BYOK

BYOK accepts an official Provider API credential already owned and authorized
by the customer. It never creates Provider accounts and never trades, shares,
or pools the credential across organizations. Create/list/delete credentials
under `/api/console/byok/credentials`; plaintext is encrypted at rest and is
not returned by list APIs.

The organization must accept the recorded ownership terms and an applicable
effective BYOK service-fee policy must exist. For BYOK settlement, Provider
cost is treated as customer-owned and the published ModelDock service fee—not
the ordinary retail Token price—is charged. Provider contract, region,
kill-switch, data-region, and model-capability gates still apply.

## 13. Data retention

Under the default policy, ModelDock does not persist prompt or response bodies.
It does retain operational and financial metadata needed for authentication,
rate limiting, usage, billing, disputes, audit, security, and legal holds.
Retention is deployment- and plan-specific. The public Provider catalog
discloses Provider `data_retention_policy` and processing regions when the
operator has supplied reviewed values.

Current native lifecycle jobs cover governed privacy/report records and honor
legal holds. Automatic deletion of every request/usage/audit table is not a
bundled promise; an operator must publish and run its reviewed retention job.
Do not claim a fixed deletion period unless the deployed job and legal hold
policy actually enforce it. See [security.md](security.md) and
[public-operations-governance.md](public-operations-governance.md).

## 14. Provider selection and fallback

Every candidate is filtered before dispatch for enabled/contract/resale state,
emergency kill switch, customer and model region, data-processing region,
organization policy, Provider budget/capacity, margin, endpoint capability,
and credential ownership.

For a physical route with `fallback_group_id`, the bounded behavior is:

1. If primary-group credential scheduling returns any error, ModelDock may try
   the fallback credential group before making an upstream request.
2. If a primary credential was selected but its first upstream call returns a
   non-cancellation, non-deadline connection error before ModelDock receives an
   HTTP response, ModelDock may select the fallback group and make at most one
   additional upstream call.
3. A client cancellation or Provider deadline does not trigger that second
   call. Receiving any HTTP response—including `4xx` or `5xx`—also ends
   fallback eligibility. Once SSE response bytes begin, ModelDock never
   switches or replays the active stream.

A connection error with no HTTP response is ambiguous: the first upstream may
already have accepted or processed the request. The bounded fallback can
therefore cause the Provider to process the logical request twice, including
duplicate generation and upstream billing. A downstream `Idempotency-Key`
prevents a second client admission/charge in ModelDock, but it does not prove
upstream deduplication between these internal attempts. Applications must
tolerate this risk and must not rely on fallback for non-idempotent external
side effects unless they add their own durable idempotency boundary.

Intelligent routing may choose another eligible physical route before the
first attempt. The configured fallback group is an alternate credential group
for that route, not an unrestricted cross-Provider bypass. A Provider `401`
response disables the credential and `429` creates a bounded cooldown; neither
HTTP response is replayed through fallback. These controls are availability and
safety behavior, not mechanisms for evading Provider terms or regional
restrictions.

## 15. Contract and source of truth

The complete machine-readable HTTP contract is
[openapi.yaml](openapi.yaml). Public catalog/pricing responses are the runtime
source for availability and price; this guide explains their meaning but does
not replace effective-dated runtime data.
