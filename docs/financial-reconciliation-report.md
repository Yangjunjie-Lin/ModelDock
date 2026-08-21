# Financial reconciliation report

**Overall release decision:** `NO-GO`  
**Synthetic financial test result:** PASS with zero reconciliation differences  
**Run date:** 2026-08-17 (Asia/Shanghai)

This report records exact results from a disposable database, signed sandbox
payments, and a deterministic local Provider. It demonstrates implementation
invariants only. It is not a bank, acquirer, tax, or real Provider settlement
statement.

## Scope and representations

The commercial path stores currency amounts as PostgreSQL `NUMERIC` and returns
fixed-scale decimal strings. Funding, payment, price snapshot, promotion,
refund, wallet, journal, Provider statement, and reconciliation operations use
idempotency keys, transactions, terminal-state checks, and audit evidence.

Release is nevertheless blocked because older budget, usage aggregate, catalog,
and model-cost paths still use Go `float64` and PostgreSQL `::float8`. A zero
difference in this synthetic commercial run does not waive the no-float rule.

## Recharge and webhook replay

| Evidence | Exact result |
| --- | --- |
| Sandbox recharge order | `10.000000000000 USD` |
| Signed `payment.succeeded` event | Accepted; order reached `CREDITED` |
| Same signed event replay | Accepted idempotently |
| Durable event rows for Provider event ID | `1` |
| Wallet top-up transactions for recharge | `1` |

The replay therefore did not duplicate cash balance or payment evidence.

## Request economics

| Request | Provider cost | Retail sale | Promotion used | Final cash charge | Gross margin before promotion | Result |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| First ordinary request (fallback attempt) | `0.000007` | `0.000014` | `0.000005` | `0.000009` | `0.000007` | Settled; unused reservation released |
| Second ordinary request | `0.000007` | `0.000014` | `0` | `0.000014` | `0.000007` | Settled; unused reservation released |
| Combined | `0.000014` | `0.000028` | `0.000005` | `0.000023` | `0.000014` | Exact request-time price snapshots retained |

For the first request:

```text
customer_sale_amount - provider_cost_amount = platform_gross_margin
0.000014 - 0.000007 = 0.000007

customer_sale_amount - promotion_amount = final_user_amount
0.000014 - 0.000005 = 0.000009
```

The operation was terminal and `settled_amount + released_amount` equaled the
original maximum reservation. The request replay with the same idempotency key
returned conflict and retained exactly one funding operation.

## Streaming, interruption, and fault accounting

- SSE request reached `SETTLED` with positive settlement and positive release.
- A client-disconnected stream reached a terminal funding status and recorded
  `client_disconnected` rather than being overwritten by content-policy state.
- Provider timeout, 429, and 500 each produced a failed operation with
  `settled_amount=0` and `released_amount=maximum_amount`.
- Redis interruption left no invalid open/partially charged reservation.
- During PostgreSQL pause, readiness stopped reporting success and recovered
  after connectivity returned.
- After a hard app/worker restart, the reserved operation reached a terminal
  state with `usage_source=ESTIMATED_CRASH_RECOVERY`,
  `settled_amount + released_amount = maximum_amount`, and wallet
  `reserved_balance=0`.
- A price version changed while a request was reserved. The usage snapshot kept
  the original retail input price `1`; the new latest price was `3`, and their
  price-version IDs differed as intended.

## Monthly statement and refund

- The current UTC monthly customer statement contained at least the two settled
  ordinary requests.
- A separate recharge of `2.000000000000 USD` was credited and refunded for
  exactly `2.000000000000 USD`.
- Replaying the same refund idempotency key returned the existing result; the
  database retained exactly one `SUCCEEDED` refund row for that order/key.

## Four-way close

Two Provider statement lines were imported, each for
`0.000007000000 USD`, total `0.000014000000 USD`, linked by internal request ID
and upstream request ID. The daily reconciliation run completed with:

| Plane | Evidence compared | Result |
| --- | --- | --- |
| Payment | Recharge order, signed event, reconciliation record, top-up | MATCHED |
| Wallet/ledger | Cash/promotion/reserved movements and posted journal | MATCHED |
| Usage/customer sale | Funding settlement and immutable usage price snapshot | MATCHED |
| Provider cost | Successful attempts, request trace IDs, statement lines | MATCHED |
| Aggregate reconciliation summary | Open difference cases | `0` differences |

Zero-value `WAIVED` pre-funding/idempotency-replay evidence is explicitly
reconciled rather than misclassified as a missing financial record. Nullable
match expressions fail safely, and reconciliation reads are buffered before
write transactions to avoid a busy PostgreSQL connection.

## Concurrency and integrity suites

- 8 concurrent ordinary and 8 concurrent streaming calls all returned 200 and
  reached settled operations.
- The dedicated funding suite ran 100 concurrent reservations against one
  wallet, plus cancellation and crash recovery, while retaining immutable,
  debit-equals-credit journals.
- Payment, pricing, subscription, and financial-close integration suites passed
  replay, lifecycle, concurrent-version, margin, recovery, and traceability
  assertions.

## Release blockers and required financial revalidation

The following prevent commercial release despite the passing synthetic close:

1. replace all remaining money/quota/catalog `float64` and `::float8` paths with
   exact representations through a separately reviewed compatibility migration;
2. reconcile an approved payment partner's sandbox settlement/export, fees,
   refunds, and replay behavior;
3. reconcile an approved real Provider billing export under its signed contract;
4. approve tax, invoice, promotion, refund, chargeback, FX, rounding, and minor
   unit policies for each currency/region;
5. exercise production worker restart, managed database failover, object backup,
   and PITR with finance evidence retained; and
6. rerun the full suite on the exact immutable release artifacts.

Any non-zero unexplained difference is an automatic release blocker and must
not be converted to a manual success status.
