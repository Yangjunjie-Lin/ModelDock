# OpenRouter-inspired operating model

ModelDock adopts the parts of an aggregator operating model that fit its
existing tenant, evidence, and official-credential boundaries: one API across
multiple Providers, request-level routing controls, BYOK plus platform
capacity, model fallback, free-model discovery, public quality evidence, and
enterprise identity governance. It does not clone consumer-account pooling,
trial collection, browser-session automation, or Provider signup workflows.

## Request-level routing

OpenAI-compatible inference bodies may include a ModelDock `provider` object
and an ordered `models` fallback list. These fields are removed before the
request is sent upstream.

```json
{
  "model": "auto:balanced",
  "models": ["gpt-4.1-mini", "claude-3-7-sonnet"],
  "provider": {
    "order": ["anthropic", "openai"],
    "only": ["anthropic", "openai", "google"],
    "ignore": ["provider-under-review"],
    "allow_fallbacks": true,
    "sort": "latency",
    "max_price": { "prompt": "5", "completion": "15" },
    "zdr": true,
    "data_collection": "deny",
    "processing_regions": ["gb", "de"],
    "required_capabilities": ["tool_calling"],
    "use_shared_capacity": true
  },
  "messages": [{ "role": "user", "content": "Reply with OK." }]
}
```

Workspace defaults are merged first. A request can narrow an allowlist,
processing region, privacy rule, price ceiling, fallback permission, or shared
capacity permission, but cannot relax it. `tools`, `response_format`, and
`modalities` add derived capability requirements. Routing occurs before an
upstream request is sent; ModelDock does not issue a second paid upstream call
after an ambiguous response solely to hide a Provider failure.

Successful responses include:

- `X-RelayDock-Provider`
- `X-RelayDock-Resolved-Model`
- `X-RelayDock-Routing-Strategy`
- `X-RelayDock-Routing-Candidates`
- `X-RelayDock-Capacity-Source`

The normalized policy and candidate explanation are also stored with the
funding operation. No credential ID or secret is exposed in these headers.

## BYOK and shared capacity

Organization-owned BYOK credentials can be placed in `PRIORITIZED` or
`FALLBACK` capacity sections. Model, downstream API Key, and member filters
scope where a credential may run. `shared_capacity_fallback` has three modes:

- `ALWAYS`: platform capacity may be used if BYOK cannot serve the request;
- `OUTSIDE_FILTERS`: platform capacity is blocked where the BYOK policy claims
  the request, but may serve requests outside those filters;
- `NEVER`: matching organization traffic cannot use shared capacity.

The request policy may additionally set `use_shared_capacity=false`. BYOK
budget treatment is explicit per Workspace: budgets can include only the
configured service fee or include catalog-price shadow consumption. Monthly
free BYOK service-fee allowances and basis-point policies are snapshotted with
the funding operation so later policy changes cannot rewrite settled history.

## Free-model routing

`auto:free` selects only routes whose current input and output catalog prices
are both zero, then requires the complete request quote (including fixed fees)
to equal zero before consuming quota. Admission is atomic per Workspace and
downstream API Key using daily request and Token limits. A zero limit disables
the feature; it never means unlimited. An active wallet is required for account
status enforcement, but a prepaid wallet does not need a positive balance for
the zero-cost route. Estimated Tokens are reserved before dispatch and replaced
by actual usage against the original UTC usage date during settlement.

## Provider declarations and measured quality

Administrators publish Provider capability documents under
`/api/admin/provider-capabilities`. The server canonicalizes and hashes each
JSON document, supersedes the prior active version, and prevents content
mutation or deletion. A declaration may describe supported models,
capabilities, pricing source, capacity modes, processing regions, and
compliance claims.

Declarations are not measurements. Public quality at
`/api/public/provider-quality` comes from ModelDock traffic/probes and is
published only when the Provider is commercially/regionally available and the
platform-owned quality policy's minimum sample threshold has been reached.
The existing public Provider catalog continues to distinguish measured uptime
from supplier-declared uptime.

## Enterprise identity and regions

Workspace routing has an explicit processing-region allowlist. Organization
administrators can safely store an OIDC issuer, client ID, encrypted client
secret, allowed email domains, and SSO enforcement state. The discovery test
requires HTTPS, verifies issuer equality and required endpoints, limits the
response body, and rejects localhost, private, link-local, multicast, and
special-purpose addresses after DNS resolution.

SCIM 2.0 is exposed at `/scim/v2/{organizationID}` with a display-once bearer
token; only its keyed digest is stored. Users map to organization membership
and Groups map to Teams. Deprovisioning disables only that organization's
membership and project/team grants. It does not delete a shared global user,
cannot remove the last organization owner, and never grants platform
administrator roles.

OIDC configuration/discovery and SCIM provisioning are implemented here. A
production deployment must complete its IdP-specific OIDC callback/session
integration and recovery policy before enabling `enforce_sso`; configuration
storage alone is not evidence that a particular IdP has been approved.

## API and Console surfaces

- Console **Workspace Policy**: default routing, privacy, regions, free limits,
  BYOK shadow-budget behavior, OIDC, and SCIM token rotation.
- Console **BYOK**: capacity section, shared fallback, and filters.
- Console **Provider Quality**: public measured quality and active capability
  declarations for the selected region.
- Admin **Provider Capabilities**: append-only publication and evidence digest.

See [openapi.yaml](openapi.yaml) for the complete request/response contract.
