# Supplier payables, settlement, payout, and disputes

ModelDock derives supplier payables only from its own immutable pricing snapshot and a terminal funding operation. A supplier bill is a declaration used for reconciliation; it cannot create usage, increase payable balance, approve a settlement, or trigger payout.

## Authoritative evidence chain

An eligible accrual requires all of the following in one database transaction:

1. `billing_usage_records.status = CHARGED`;
2. the linked `funding_operation` is `SETTLED` or `PARTIALLY_SETTLED`;
3. an immutable `usage_price_snapshot` has `credential_owner = PLATFORM`;
4. the snapshot was settled after the approved supplier/Provider link began;
5. the supplier is approved, KYB verified, and under an active contract;
6. the Provider is enabled, contracted, not pricing-disabled, and not killed;
7. the settlement policy is explicitly enabled.

`supplier_payable_accrual` snapshots the gross platform-measured Provider cost, commission basis points, risk-reserve basis points, exact amounts, settlement time, and reserve release time. `supplier_payable_entry` is append-only. Concurrent workers lock source usage with `SKIP LOCKED`, and the usage ID, request ID, snapshot ID, and idempotency key are unique.

All amounts use PostgreSQL `NUMERIC(30,12)`, JSON decimal strings, and Go `big.Rat`. No supplier settlement path uses `float32` or `float64`.

## Amount model

For each platform-measured usage:

```text
commission = round(gross × commission_bps / 10000, 12)
reserve    = round((gross - commission) × reserve_bps / 10000, 12)
payable    = gross - commission - reserve
```

The policy value is snapshotted on the accrual, so later policy changes do not rewrite history. Mature reserve releases are new credit entries, never updates. An audited refund share is a debit entry associated with the original platform accrual. Refund shares added before payout force the affected batch back through dispute/approval controls; post-payout shares carry forward against later eligible credits.

## Settlement cycles and approval

Policies support daily, weekly, and monthly UTC cycles, a minimum exact-decimal payout, commission, risk reserve, reserve hold days, tax/invoice requirements, adapter, and adapter region. Existing and newly created suppliers receive a disabled policy.

A batch contains only unbatched payable entries for one supplier, Provider, and the supplier's reviewed payout currency. Entries in a cancelled unpaid batch become eligible for a later cycle, so upheld appeals and full pre-payout refund allocations do not strand or duplicate the subledger. Approval is four-eyes when an operator created the batch. The approving transaction re-locks the batch and rejects it unless:

- payout remains at or above the configured minimum;
- tax is `VERIFIED` and invoice is `APPROVED` when required;
- every positive usage/reserve item has an administrator-imported `provider_statement_line` matching request ID, currency, and platform gross cost;
- every funding operation and customer charge remains terminal and settled;
- no open usage or settlement appeal exists;
- supplier status, KYB, contract, payout destination, policy, adapter, and region remain eligible.

A supplier-declared `supplier_bill` cannot satisfy the Provider statement match. Its immutable lines are independently classified as `RECONCILED` or `DISCREPANT` against platform accruals.

## Payout adapter and retries

`internal/payout` is the only adapter boundary. Each adapter exposes an enabled switch, contract status, allowed regions, and production-readiness status. The settlement worker decrypts the destination only immediately before `Send`, never logs it, and clears the plaintext reference after the call.

The stable `supplier-payout:<batch UUID>` key must be passed unchanged to the processor on every retry. The database records each attempt before external I/O. A crash after processor success is safe only when the contracted adapter honors that idempotency key: the next attempt receives the same provider result, and the unique batch journal/payable entry prevents duplicate local completion.

The bundled `sandbox` adapter is test-only, disabled by default, and returns an HMAC-derived replay-stable reference. It is never production-ready. A real processor must be implemented, contract-reviewed, region-allowed, independently tested, and explicitly enabled before production payout.

On confirmed success, one transaction:

- rechecks every eligibility and dispute condition;
- posts a balanced `SUPPLIER_PAYOUT` journal against Provider payable, platform cash, commission revenue, and refund recovery;
- appends the payout debit to the supplier subledger;
- completes the attempt and batch;
- writes settlement events and audit evidence.

Adapter failure records a bounded code and exponential retry time. Operators can approve a retry with a reason; the original payout idempotency key never changes.

## Appeals and reconciliation

Suppliers can appeal platform usage, an unpaid settlement, a bill, or a reconciliation case. An open usage/settlement appeal immediately moves the unpaid batch to `DISPUTED` and blocks both approval and final payout completion. Resolution is audited. A rejected appeal returns the batch to approval; an upheld settlement appeal cancels the unpaid batch.

Daily financial close additionally checks:

- payable accrual versus the immutable platform snapshot and terminal funding state;
- supplier-declared bill lines versus platform accruals;
- paid settlement versus its posted balanced journal and exact cash credit.

Differences remain in the existing operator reconciliation queue and are never silently accepted.

## Operations

Environment variables:

- `RELAYDOCK_SUPPLIER_SETTLEMENT_POLL_INTERVAL` (default `1m`)
- `RELAYDOCK_SUPPLIER_SETTLEMENT_BATCH_SIZE` (default `100`, maximum `500`)
- `RELAYDOCK_PAYOUT_ALLOWED_REGIONS` (explicit ISO alpha-2 list)
- `RELAYDOCK_PAYOUT_SANDBOX_ENABLED` (default `false`)
- `RELAYDOCK_PAYOUT_SANDBOX_SECRET` (required only when sandbox is enabled)

Stop-loss procedure:

1. Disable the affected supplier policy. This stops new accrual claims, cycle creation, and payout claims.
2. Disable the payout adapter at deployment configuration and restart workers if processor behavior is suspect.
3. Do not edit payable entries, settlement items, statement matches, payout entries, posted journals, or event rows.
4. Open or retain the reconciliation case and supplier appeal with redacted evidence.
5. For a processor-side ambiguous result, reconcile by the stable payout idempotency key before allowing a retry.
6. Never mark a batch paid manually. Implement a contracted adapter reconciliation result and complete through the same transactional method.

Monitor `supplier_payable_accrual_failed`, `supplier_reserve_release_failed`, `supplier_settlement_cycle_failed`, `supplier_payout_claim_failed`, and `supplier_payout_completion_failed`. Logs contain IDs and bounded failure classes, never destinations, tax identifiers, invoices, credentials, or API keys.

## Migration and rollback

Migration `0022_supplier_settlement.sql` is forward-only. It adds supplier policies, immutable accrual/entry/bill/item/match/event evidence, settlement/appeal/attempt state, three reconciliation types, and additive journal links/types. It deletes no field or API.

Before any rollback, disable every settlement policy and payout adapter and wait for `PROCESSING` batches to resolve. Application rollback is safe only to a binary that tolerates schema version 22. Schema removal is allowed only when there are no accruals, bills, batches, appeals, attempts, settlement events, statement matches, payout journals, or migration-22 audit records. Posted evidence must instead be retained and reversed by a new forward migration; never drop it. A guarded empty-install rollback may drop migration-22 triggers/tables, remove the additive journal columns and reconciliation/journal enum values, restore the migration-21 protection functions, and remove schema version 22. Take and verify a backup first.
