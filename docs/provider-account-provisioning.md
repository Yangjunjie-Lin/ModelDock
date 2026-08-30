# Provider account provisioning

RelayDock can bind a local user and organization to an authorized upstream
enterprise project/service account, an existing BYOK credential, or an
operator-reviewed manual account. The design reuses the account-state,
health-check, pool, and scheduling concepts from the optional gptGrok2api
bridge, but it does not import consumer auto-registration, CAPTCHA/verification
bypass, browser fingerprinting, temporary mail, proxy rotation, or trial-credit
harvesting.

## Capability contract

Each configured Provider publishes four independent facts in
`GET /api/admin/provider-provisioning/capabilities`:

- onboarding mode (`OFFICIAL_ENTERPRISE`, `BYOK`, `MANUAL`, or local-only
  `MOCK_ENTERPRISE`);
- automatic project/service-account binding support;
- automatic official upstream credit-allocation support;
- binding refresh support.

These flags fail closed. A payment order can include `target_provider_id` only
when the runtime adapter is enabled and declares both automatic binding and
automatic credit allocation. Otherwise the normal RelayDock wallet recharge is
available, but no upstream credit claim is made.

OpenAI documents Admin APIs for creating API Platform projects and project
service accounts. It also documents organization invitations rather than a
consumer-account creation endpoint. The current documentation does not expose
a project-wallet balance transfer/top-up operation. RelayDock therefore enables
OpenAI automatic project/service-account binding when explicitly configured,
but advertises `supports_automatic_credit: false` and will reject OpenAI as a
payment-linked upstream-credit target.

Official references:

- <https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/methods/create>
- <https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/projects/subresources/service_accounts/methods/create>

Providers without a configured, documented project/sub-account API are exposed
as BYOK/manual. Adding a real automatic adapter requires a provider contract,
official API documentation, an idempotent external operation, secure secret
capture, and an allocation/reconciliation API. UI labels must not be changed to
"automatic" before those requirements are met.

## Payment and allocation sequence

1. RelayDock creates a recharge order. An optional `target_provider_id` is
   accepted only for a fully capable Provisioner. The order transaction also
   reserves the user's binding mode, so an incompatible existing binding is
   rejected before any payment is initiated.
2. A signed webhook or reviewed payment transition marks the order paid.
3. `CreditPaidRecharge` posts the platform wallet transaction and double-entry
   journal. In the same PostgreSQL transaction it revalidates the reserved
   user/Provider binding and creates one `ALLOCATE_CREDIT` job keyed by the
   recharge order.
4. The Provisioner worker claims the job with a lease. It first ensures the
   enterprise binding, encrypts any one-time service-account key into the
   existing credential pool, scopes the managed credential to the owning
   organization and user, and then invokes the official allocation API.
5. Only a successful external allocation writes an immutable
   `provider_credit_allocation` row and increments the binding's displayed
   upstream amount. Payment webhook replay cannot create a second job or
   allocation.

Platform wallet balance and upstream enterprise allocation are deliberately
different fields and states. A credited RelayDock wallet is not presented as
proof that a Provider received funds.

## Local no-charge acceptance loop

`mock_enterprise` is disabled by default and production Compose forces it off.
Its test covers one user binding, encrypted-credential handoff contract,
concurrent payment-allocation replay, a `mock-free` model call, unchanged balance
for the free call, and cross-user binding/allocation rejection:

```powershell
go test ./internal/provisioning -run TestMockEnterpriseSingleAccountClosedLoop -v
```

With an isolated PostgreSQL database, the durable payment-to-worker loop is:

```powershell
$env:TEST_DATABASE_URL='postgres://...'
go test ./internal/provisioning -run TestProviderPaymentProvisioningDatabaseClosedLoop -v
```

Console self-service binding accepts only an organization-owned BYOK
credential or an enabled automatic Provisioner. Reviewed manual references and
platform-owned credentials require the administrator route.

No real payment or upstream account is created by this test. A real Provider
acceptance test must use a dedicated enterprise sandbox/contract account and
must not be enabled merely by pointing at an OpenAI-compatible inference URL.
