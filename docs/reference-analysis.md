# Reference analysis

This note records the sources and design lessons used for RelayDock V1. It is
an architectural study, not a code provenance claim. No source code was copied
from either reference repository.

Reviewed on: 2026-08-09

## OpenAI official API documentation

OpenAI's current developer documentation is the sole source of truth for the
OpenAI adapter. RelayDock deliberately uses the public API surface rather than
consumer ChatGPT web sessions or private browser endpoints.

| Official page | RelayDock takeaway |
| --- | --- |
| [API reference overview](https://developers.openai.com/api/reference/overview) | Use HTTP Bearer credentials, keep provider keys server-side, log RelayDock request IDs, retain upstream `x-request-id` for administrator diagnostics, and treat rate-limit headers as signals rather than as limits to evade. The overview distinguishes ordinary application credentials from Admin API credentials. |
| [Create a response](https://developers.openai.com/api/reference/resources/responses/methods/create) | Proxy `POST /v1/responses` as the primary V1 generation surface. Preserve supported request fields and upstream response/event shapes while resolving the RelayDock model alias before forwarding. |
| [Create a chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) | Keep `POST /v1/chat/completions` for ecosystem compatibility. It is a separate adapter operation, not an undocumented conversion through a consumer endpoint. |
| [Create an embedding](https://developers.openai.com/api/reference/resources/embeddings/methods/create) | Proxy JSON embedding requests through a route whose configured capability includes `embedding`; record usage without persisting input text by default. |
| [List models](https://developers.openai.com/api/reference/resources/models/methods/list) | Upstream model synchronization may use the official Models API. The public RelayDock list exposes configured aliases and never infers undocumented context windows. |
| [Streaming Responses](https://developers.openai.com/api/docs/guides/streaming-responses) | Stream Server-Sent Events incrementally, preserve event ordering, propagate cancellation, measure time to first byte, and never buffer a complete stream in memory. |
| [Rate limits](https://developers.openai.com/api/docs/guides/rate-limits) | Respect `429` responses and reset information. RelayDock cools down the selected credential and admits later requests only within configured project/client limits; it does not rotate identities or network egress to evade limits. |
| [Administration overview](https://developers.openai.com/api/reference/administration/overview) | Organization administration is a separate, least-privilege concern. A future management credential must be isolated from inference credentials and is never selected by the data-plane scheduler. |

### Consequences for V1

- The gateway implements `/v1/models`, `/v1/responses`,
  `/v1/chat/completions`, and `/v1/embeddings`.
- A RelayDock API key authenticates the downstream client. The provider adapter
  injects an authorized upstream credential only after authorization, model
  resolution, rate limiting, and scheduling succeed.
- `OpenAI-Organization` and `OpenAI-Project` are derived from administrator
  credential metadata. Downstream callers cannot override them.
- The client receives a RelayDock request ID. The upstream request ID is stored
  for privileged diagnostics but is not used as a public credential or routing
  signal.
- Capability and pricing metadata are administrator-controlled when the
  official API does not provide authoritative values. Unknown values remain
  null; RelayDock does not invent them.
- RelayDock may pass through additional backwards-compatible response fields.
  Consumers must tolerate additive fields and new stream event types.

## `jlcodes99/cockpit-tools`

Source reviewed: [repository README](https://github.com/jlcodes99/cockpit-tools)

The reusable lesson is the high-density operational experience:

- one dashboard that combines health, recent activity, capacity, and warning
  state;
- status badges and progress bars that make a large collection scannable;
- search, filters, tags, groups, and explicit multi-select batch actions;
- progressive disclosure: a compact list/card first, detailed history in a
  drawer or detail view;
- visible refresh timestamps, empty states, and error states;
- consistent status vocabulary across summary cards and detail tables.

RelayDock applies those patterns only to administrator-imported provider API
credentials. It does **not** reproduce IDE account switching, local consumer
login injection, subscription-plan harvesting, account wake-up behavior, or
browser-profile management.

## `AuuCoder/gptGrok2api`

Source reviewed: [repository README](https://github.com/AuuCoder/gptGrok2api)

Only the following high-level, provider-neutral ideas are relevant:

- one OpenAI-compatible gateway namespace with streaming and non-streaming
  endpoints;
- provider adapters behind a stable internal contract;
- explicit credential lifecycle states, health signals, grouping, and
  scheduler-visible cooldown;
- structured request/usage logging and operational health checks;
- containerized deployment with durable data and log directories.

RelayDock is a clean-room Go implementation and does not import code, account
data, or runtime components from that project.

The following areas are categorically excluded from RelayDock:

- automated or bulk creation of OpenAI, ChatGPT, xAI, or other consumer
  accounts;
- temporary-email, email-verification, or OTP acquisition automation;
- CAPTCHA or Turnstile solving, bypass, or challenge outsourcing;
- browser fingerprint manipulation or consumer session/cookie pools;
- proxy or egress rotation intended to evade rate limits, policy enforcement,
  geographic controls, or abuse detection;
- automated trial, promotion, checkout, or new-user benefit acquisition;
- extraction or replay of private web application tokens;
- automatic delivery of generated accounts or sessions to another system.

These exclusions apply even if a related repository implements such features.
RelayDock accepts only credentials an administrator is authorized to possess
and use with a provider's official public API.

## Design synthesis

The resulting product is neither an account switcher nor an account factory.
It combines the operational clarity of a dense fleet-management console with a
conservative, official-API-only gateway:

```text
authorized provider credential
  -> encrypted credential inventory
  -> deterministic route and scheduler
  -> official provider API
  -> metered RelayDock response
```

The compliance boundary is enforced in the domain model, API surface,
documentation, and deployment: there is no registration worker, CAPTCHA
service, browser automation service, consumer session store, benefit claim
job, or evasive proxy scheduler.

