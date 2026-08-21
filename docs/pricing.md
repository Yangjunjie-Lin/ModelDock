# Commercial pricing and settlement

Migration `8:pricing` separates provider cost, customer retail price, promotion
value, and wallet cash. The legacy `request_logs.estimated_cost` field remains
available to existing clients and dashboards, but it is no longer a wallet
charge input.

## Public purchase disclosure

`GET /api/public/pricing?region=<ISO-3166-1-alpha-2>&currency=<ISO-4217>` is
the purchase-time read model. It never collapses commercial charges into one
marketing number:

| Field | Meaning |
| --- | --- |
| `subscription_plans[].subscription_fee` | Effective versioned fee for finite product entitlements |
| `token_prices[]` | Separately metered request/input/cached-input/output Token retail rates and unit |
| `token_prices[].availability` | Nested region status for that price row (`available`, `region`, `status`, `reason_code`) |
| `payment_fees[]` | Effective payment-channel or platform-service fee schedule |
| `commercial_terms.bonus_credit_amount` | Promotional value, separately marked non-refundable |
| `commercial_terms.subscription_tax_included`, `token_tax_included`, `tax_disclosure` | Reviewed tax presentation; no automatic tax-engine claim |
| `commercial_terms.refund_summary` / `refund_policy_url` | Reviewed refund disclosure and its policy page |
| Model/Provider `availability` | Whether the item can be publicly offered in this region at the displayed price before checkout |
| `payment_region_supported` | Whether the requested region is enabled by the deployment payment-region allowlist; separate from fee configuration |

Amounts are exact decimal strings. Subscription Token billing mode is always
`METERED_SEPARATE`. A missing/expired price, missing commercial terms,
unavailable region, disabled Provider, unapproved resale status, or kill switch
must prevent purchase/call and must not be replaced by a browser estimate.
Draft legal/tax wording carries `legal_review_required: true` and cannot be
represented as final legal advice.

Public availability does not guarantee that a particular project is authorized
or able to call: project route grants, eligible credential inventory,
dispatch-time processing/policy gates, Provider health, and capacity remain
separate checks. Provider data-processing regions are disclosed to the buyer
but are not themselves part of the public availability calculation.

`terms_configured` and `payment_fees_configured` report current publication,
not lawyer approval; consumers must also inspect `legal_review_status`. A
non-zero `bonus_credit_amount` may be published only after a real promotion
program, PromotionCredit issuance path, and eligibility review are enabled. The
field does not issue value or create a journal entry. The user's persisted
promotion-credit and ledger records are the sole spendable-balance evidence.

Administrators publish these disclosures through the append-only, audited
`GET/POST /api/admin/public/commercial-terms` and
`GET/POST /api/admin/public/payment-fees` APIs. Each POST requires an
idempotency key in the body or header; an identical replay returns the original
version, while different content under the same key conflicts. Published rows
cannot be updated or deleted at the database layer. Corrections use a later
effective version. `legal_review_status=APPROVED` additionally requires the
explicit `legal_review_confirmed` proof; even approved records retain the
machine-generated-text review boundary until the deployed legal page itself is
replaced by counsel-approved text.

## Domain model

| Relation | Purpose |
| --- | --- |
| `provider_cost_price_book` | Effective-dated, approved provider acquisition cost for one provider/model. |
| `customer_retail_price_book` | Customer-specific or system-default retail price. A null organization is the system default. |
| `organization_price_plan` | Subscription or organization-override retail price. |
| `model_price_version` | Immutable, resolved combination of one cost source and one retail source. |
| `pricing_quote` | Five-minute immutable estimates with tokens, cost, retail, discount, tax, final amount, and version. |
| `usage_price_snapshot` | Permanent request settlement snapshot; it is the authoritative per-request margin record. |
| `promotion_credit` | Non-refundable promotional value, kept outside `wallets.available_balance`. |
| `promotion_credit_redemptions` | Idempotent request-level consumption ledger for promotion credits. |
| `pricing_margin_policies` | Minimum absolute and basis-point margin policy scoped to a model, provider, organization, or a combination. |

Every price row records provider, model, non-cached input, cached input,
output, fixed request amount, ISO-4217 currency, token unit, effective/expiry
time, source, creator, and approval state. Provider and retail token units are
snapshotted independently, so a request may safely combine differently scaled
cost and sale prices. Published rows are append-only.

## Resolution order and concurrency

Retail resolution is deterministic:

1. `ORGANIZATION_OVERRIDE` in `organization_price_plan`;
2. `SUBSCRIPTION` in `organization_price_plan`;
3. organization row in `customer_retail_price_book`;
4. null-organization system default in `customer_retail_price_book`.

Provider cost and the winning retail row are read in one PostgreSQL
`REPEATABLE READ` transaction. The transaction copies both into an immutable
`model_price_version`; a database sequence gives every version a unique
monotonic number under concurrent publication and quoting. A gateway request
fixes that version before upstream dispatch and uses the same version when
actual usage is settled. Later prices cannot change the snapshot.

## Exact arithmetic

Database amounts use `NUMERIC(30,12)`. Migration 8 widens the pre-existing
wallet, wallet-transaction, and billing-usage amount columns from eight to
twelve decimal places without deleting or renaming them. Go accepts/returns
monetary values as decimal strings and calculates with `math/big.Rat`; wallet
balance changes are performed by PostgreSQL `NUMERIC` expressions under row
locks. Binary floating point is not used for payable amounts.

For input tokens that include cached tokens:

```text
non_cached_input = input_tokens - cached_input_tokens
amount = fixed_request_price
       + (non_cached_input × input_price
       +  cached_input_tokens × cached_input_price
       +  output_tokens × output_price) / unit
```

The snapshot stores provider cost currency, customer currency, exact exchange
rate, gross margin before promotion, promotion amount, pre-tax amount, tax
rate, tax amount, final user amount, and the pricing rule version.

The current publication API requires provider and retail books to use the same
currency because there is not yet an approved, effective-dated FX policy book.
The exchange-rate field is still snapshotted explicitly (normally `1`) so
historical settlement remains unambiguous and a future FX policy can be added
without changing old records.

## Publication and minimum margin

An approved provider cost must exist before a retail price can be published.
The most specific matching margin policy is selected. A price below cost plus
the required absolute/basis-point margin returns `negative_margin`.

An administrator may force a publication only by sending both
`force_override: true` and the exact second confirmation
`CONFIRM_NEGATIVE_MARGIN_OVERRIDE`. The resulting price is marked
`FORCED_APPROVED`; an attributed `pricing.negative_margin_override` audit row
is committed in the same transaction as the forced price. The control-plane
also records the request metadata without storing the confirmation phrase.

## Provider contract and region gates

Providers now expose `contract_status`, `allowed_regions`, and
`pricing_disabled`. A quote or gateway dispatch is rejected when the provider
is disabled, pricing-disabled, not `ACTIVE`, or the organization's
`billing_region` is absent from the provider allowlist. `"*"` is the upgrade
default and should be replaced with reviewed regions before production use.

## Promotion and cash separation

Promotion grants are always `non_refundable=true`. Settlement locks active
credits in expiry order, writes one redemption row per source credit, and
reduces `amount_remaining` transactionally. It never increments or reclassifies
`wallets.available_balance`; only the discounted final amount is charged to
the wallet. Refund processes must use wallet cash transactions and must not
convert unused promotion credit into refundable funds.

Every promotion grant requires an idempotency key in the request body or
`Idempotency-Key` header. Repeating or racing the same organization/key pair
returns the original grant and cannot increase promotional value twice.
New promotion grants, wallet adjustments, and usage charges write their audit
row in the same transaction as the corresponding financial ledger mutation.

## API

- `POST /api/pricing/quote` for an authenticated control-plane user;
- `POST /api/admin/pricing/quotes` and compatibility alias
  `POST /api/admin/pricing/quote`;
- `GET/POST /api/admin/pricing/provider-cost-price-books`;
- `POST /api/admin/pricing/provider-cost-changes/manual`;
- `POST /api/admin/pricing/provider-cost-changes/fetch` (allowlisted public HTTPS JSON feed);
- `POST /api/admin/pricing/provider-cost-changes/import-csv` (`text/csv`, 1 MiB / 500 rows);
- `GET/POST /api/admin/pricing/byok-service-fee-policies` and
  `DELETE /api/admin/pricing/byok-service-fee-policies/{id}`;
- `GET/POST /api/admin/pricing/customer-retail-price-books`;
- `GET/POST /api/admin/pricing/organization-price-plans`;
- `GET/POST /api/admin/pricing/margin-policies`;
- `GET/POST /api/admin/pricing/promotion-credits`.

Quote input accepts organization, model, optional provider, estimated input,
cached input and output tokens, optional exact tax/exchange values, and an
idempotency key. Quote output contains estimated provider cost, retail amount,
promotion, tax, final amount, and `pricing_version_id`.

## Migration and rollback

Upgrade:

1. back up PostgreSQL and verify the backup off-host;
2. deploy a binary that embeds migration `8:pricing`;
3. confirm migration ledger entry `8|pricing`;
4. verify the seven required pricing tables, provider contract defaults, and
   legacy `model_prices` seed copies;
5. publish reviewed cost/retail prices before enabling prepaid billing for a
   model.

The widening `ALTER COLUMN TYPE` statements take table locks on `wallets`,
`wallet_transactions`, and `billing_usage_records`. Schedule the migration in
a maintenance window sized from a production-like copy and monitor lock wait
time before allowing application writers to resume.

Migration 8 is forward-only. A normal rollback uses the previous application
image only after a compatibility release has learned to tolerate ledger
version 8; do not delete its ledger row. Destructive schema rollback requires
stopping all writers, exporting `pricing_quote`, `model_price_version`,
`usage_price_snapshot`, promotion and wallet ledgers, removing the new foreign
keys/columns, then dropping new relations in reverse dependency order. That
loses the commercial audit chain and is not an acceptable routine rollback.
