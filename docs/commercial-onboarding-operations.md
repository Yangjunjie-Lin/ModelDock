# Public commercial experience and onboarding operations

This step joins existing account, pricing, funding, subscription, Provider
governance, usage, status, support, and audit capabilities into a public path
from first visit to a verified first API call. It is additive: `/v1`, `rdk_*`
keys, `RELAYDOCK_*` settings, database identities, and existing control-plane
paths remain compatible.

## A. 当前状态与缺口

Before this step, the repository already had verified registration, initial
workspace creation, exact-decimal effective-dated pricing, funded requests,
subscriptions, Provider contract/region gates, public status, support tickets,
and request-level finance evidence. Those capabilities were discoverable only
across internal/admin documentation and authenticated screens.

The remaining product risk was a split-brain commercial experience: a visitor
could not query the exact currently saleable model/price/region set before
sign-up; onboarding completion was inferred by the browser; conversion events
were not transactionally tied to business evidence; and the production Nginx
did not expose a public Console origin.

## B. 架构和数据模型设计

### Public read model

The unauthenticated control-plane allowlist is:

```text
GET  /api/public/config
GET  /api/public/catalog/models?region=CN&currency=CNY
GET  /api/public/catalog/providers?region=CN
GET  /api/public/pricing?region=CN&currency=CNY
GET  /api/public/status
POST /api/public/funnel/events
```

`region` is an ISO 3166-1 alpha-2 customer region and `currency` is ISO-4217.
The server does not infer a more permissive region from IP geolocation. Model
availability combines model and Provider flags, commercial/resale approval,
contract dates, kill switch, Provider customer-region and model-region rules,
and a current approved retail price whose currency matches approved Provider
cost evidence. Provider data-processing regions are disclosed separately; they
are not part of this public availability projection. Unavailable/unknown values
remain explicit and cannot be presented as a sale.

Public availability means the deployment may offer that Provider/model in the
requested region at the displayed price. It does not prove that a particular
project has a route grant, that an eligible credential is currently available,
or that real-time upstream health/capacity will admit a call. Those authenticated
and dispatch-time gates still apply.

`GET /api/public/pricing` returns separate arrays for versioned subscription
fees, metered Token prices, and payment/platform fees. `commercial_terms`
separately states tax treatment, promotional amount/non-refundability, refund
summary/link, effective time, and legal-review status. All payable values are
exact decimal strings backed by PostgreSQL `NUMERIC`; no binary floating point
is introduced by the public projection.

Each `token_prices[]` row carries the same nested `availability` shape
(`available`, `region`, `status`, `reason_code`) used by the model catalog, so a
price row cannot be mistaken for regional permission. `payment_region_supported`
requires the requested region in `RELAYDOCK_PAYMENT_ALLOWED_REGIONS`, a
runtime-admitted adapter, and an approved fee disclosure for that channel;
`payment_fees_configured` only says a
current matching fee disclosure exists. Neither flag implies counsel approval;
inspect every row's `legal_review_status`.

`bonus_credit_amount` is publication metadata, not a PromotionCredit issuance
or ledger operation. A non-zero value may be published only when a real
promotion program and PromotionCredit issuance flow are enabled and eligibility
has been reviewed. Spendable credit is determined solely by the account's
persisted promotion-credit and ledger evidence; the disclosure is not an
automatic-credit promise.

### Onboarding evidence

Authenticated users read:

```text
GET /api/console/onboarding?organization_id=<uuid>&project_id=<uuid>
```

The response derives registered, email verified, organization created, plan
selected, first verified recharge, API Key created, first successful API call,
and usage/billing evidence recorded and available to view from server-side
evidence and returns `next_step`. The response echoes the selected `project_id`.
The endpoint enforces both organization and project membership and never accepts
a write that marks progress complete.

### Funnel events

Canonical event names are:

| Event | Evidence boundary |
| --- | --- |
| `HOMEPAGE_VISITED` | Bounded anonymous public event; the raw anonymous ID is HMACed and not stored |
| `REGISTERED` | Successful account creation transaction |
| `EMAIL_VERIFIED` | One-time verification transaction activates the account |
| `API_KEY_CREATED` | Project-scoped logical Key and first version commit |
| `FIRST_RECHARGE` | First verified recharge reaches `CREDITED`, not browser redirect/pending state |
| `FIRST_API_CALL` | First gateway call with HTTP 2xx and terminal settlement evidence |
| `SECOND_API_CALL` | Second gateway call with HTTP 2xx and terminal settlement evidence |
| `FIRST_SUBSCRIPTION` | First explicit create/immediate-change/scheduled-change subscription event; migration/organization-trigger Free rows do not count |

Only `HOMEPAGE_VISITED` is accepted from an unauthenticated caller. The other
events are created by the same trusted database transaction or durable
business transition as their evidence. Clients cannot post arbitrary
conversion milestones.

Anonymous IDs must be random first-party pseudonyms, never email, phone,
payment, IP, advertising ID, prompt, API Key, or fingerprint. The server stores
only an HMAC; metadata is bounded and allowlisted. An idempotency key is
required in the `Idempotency-Key` header or request body and is bound to the
event payload. A replay returns the original event, while reuse for different
input is rejected.

Administrators read bounded UTC conversion counts at:

```text
GET /api/admin/funnel/summary?from=<RFC3339>&to=<RFC3339>
```

The summary returns aggregate counts only. It is not a user-export endpoint
and must not include anonymous HMACs, emails, API keys, request content, or
payment data.

## C. 将修改的文件

The step is intentionally distributed across the existing layers:

- additive migration `0019_public_commercial_onboarding.sql` and migration ledger;
- public/onboarding/funnel handlers and transactional store logic;
- public site and Console onboarding UI;
- `docs/openapi.yaml`, this runbook, developer quickstart, and lawyer-review
  pack under `docs/legal/`;
- production Compose, Nginx templates, environment example, and deployment
  scripts; and
- unit, integration, OpenAPI, frontend, and Compose validation.

## D. 分阶段实施

1. Apply migration 0019 while existing application traffic remains compatible.
2. Publish only professionally reviewed commercial terms/payment fee rows via
   the audited append-only Admin APIs and approved effective retail prices;
   leave unavailable combinations closed.
3. Configure a distinct public hostname, HTTPS certificate, SMTP verification,
   registration policy, exact `ALLOWED_ORIGINS`, Console URL, and monitored
   `RELAYDOCK_PUBLIC_SUPPORT_EMAIL` / `RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL` role
   mailboxes. `example.invalid` values block launch because they cannot receive
   support, complaint, or Enterprise enquiries.
4. Validate public catalog/price/status without authentication, then validate
   registration through first/second gateway request in an isolated tenant.
5. Compare aggregate funnel events with account/key/recharge/request/
   subscription source records before using them for business decisions.
6. Enable public traffic gradually and monitor registration errors, email
   outbox, price/catalog staleness, region denials, wallet settlement, and
   Provider kill-switch state.

## E. 数据迁移及回滚方案

Migration 0019 is forward-only and additive. Before deployment, back up
PostgreSQL plus master/HMAC key escrow and verify both a fresh database and a
copy upgraded from migration 0018. The migration must not delete or rename an
existing field and must not rewrite `/v1` usage/ledger evidence.

Routine rollback deploys the previous application/web/Nginx images while
leaving the migration and funnel evidence in place. Remove the public-site DNS
or restore the previous API-only Nginx template if the public experience is
the incident source. Do not remove existing `/v1` traffic or reissue `rdk_*`
keys.

Destructive schema rollback is permitted only in an isolated maintenance
window after exporting public commercial configuration and aggregate funnel
evidence. Stop all application replicas/workers; prove no later migration
depends on migration 0019; remove the new triggers/functions/indexes and new
tables in reverse dependency order; then remove only ledger version 19. This
loses conversion and publication history and is not a routine rollback.
Restoring the verified pre-upgrade backup is preferred.

## F. 测试结果

The repository-level acceptance command is:

```powershell
.\tests\integration\verify-commercial-onboarding.ps1 -ConfirmIsolatedTestDatabase
```

It must use a disposable local PostgreSQL/Redis/mock Provider stack and verify
public price/region disclosures, anonymous event replay, server-derived
milestones, `rdk_test_*` compatibility, first and second calls, exact finance
evidence, aggregate funnel counts, secret absence, and public Nginx allowlists.
Do not run it against a production database. Record actual command output in
the change handoff; this document does not claim a pass by itself.

## G. 安全检查

- Public catalog output excludes Provider base URLs, contract documents,
  credential counts/secrets, cost books, margin, customer IDs, and internal
  budget/capacity details.
- Public pricing uses approved retail values, not confidential Provider costs.
- Admin routes are absent from the public-site Nginx allowlist; unmatched
  `/api/*` returns 404 before reaching the inner Console proxy.
- Registration, verification, login, and public event ingestion have bounded
  independent rate limits; Redis failure closes identity protections.
- Browser mutations retain HttpOnly cookies, exact-origin CORS, and CSRF.
- Funnel metadata contains no prompt/response, API key, credentials, payment
  identity, or raw anonymous identifier.
- Legal pages remain visibly marked pending counsel review until an authorized
  approval changes the published version.
- Provider contract/customer-region and model-region gates execute before sale
  disclosure and again at gateway dispatch. Processing-region, project grant,
  credential, and real-time capacity gates remain dispatch-time controls; a
  warning banner is not the enforcement boundary.

## H. 剩余风险

- Seeded Provider rows are not proof of a signed contract or external endpoint
  behavior; each deployment must supply reviewed facts.
- A public price is only as current as approved effective-dated input. Monitor
  stale/missing terms and prices and fail the sale closed.
- No contracted production payment processor or tax invoice engine is bundled;
  sandbox/manual adapters must be labeled accordingly.
- Request/usage/audit retention still requires a deployment-specific reviewed
  retention job; do not promise deletion periods that are not enforced.
- Funnel counts are operational product analytics, not financial ledger totals;
  reconciliation remains authoritative for money.
- Legal, tax, filing, consumer, cross-border, Provider, and company disclosures
  remain deployment-specific and require professional approval.

## I. 本步骤验收清单

- [ ] Visitor can see capabilities, models, Providers, current status, support,
  enterprise contact, and counsel-review policy pages without authentication.
- [ ] Subscription, Token, payment/platform fee, bonus, tax, refund, currency,
  and model/region availability are separate before purchase.
- [ ] Missing/expired/unapproved price or disallowed region cannot be purchased
  or called.
- [ ] User can register, verify email, create/use an organization, select a
  plan, recharge through an enabled channel, create a one-time key, copy an
  example, call `/v1`, and view exact charge evidence.
- [ ] First and second calls and the other required milestones come from
  transactional server evidence, not arbitrary browser events.
- [ ] Existing `/v1`, `rdk_*`, and `RELAYDOCK_*` compatibility tests still run.
- [ ] Production public hostname exposes only the documented allowlist and no
  admin/database/Redis route.
- [ ] Legal pages say pending counsel review and company/filing placeholders are
  visibly unresolved rather than fabricated.
- [ ] OpenAPI parses and contains every new public/onboarding/funnel operation.
- [ ] Empty and populated database migration paths plus rollback rehearsal are
  documented and executed in the release evidence.
