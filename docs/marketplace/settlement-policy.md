# Marketplace settlement policy

Policy version: `marketplace-launch-2026-08-21`

## Authoritative payable basis

Supplier payable is accrued only from ModelDock `CHARGED` billing usage whose funding operation is settled, price snapshot is immutable, credential owner is the platform, and timestamp is after the approved supplier-Provider link. Supplier bills and Provider-reported usage are reconciliation declarations, never a payout source.

All amounts use PostgreSQL `NUMERIC(30,12)` and exact decimal strings. Commission basis points and reserve basis points are integers. Binary floating point is not used for Marketplace money.

## Calculation and availability

For each eligible usage record:

`initial payable = provider cost - platform commission - risk reserve`

The approved supplier policy defines commission, risk reserve, hold days, cycle, minimum payout, currency, tax requirement, invoice requirement, payout adapter, and allowed payout region. Refund shares are append-only debit entries allocated to the related platform accrual and can reopen or cancel an unpaid batch.

## Approval and payout

A batch requires a different approving administrator from its creator, minimum amount, verified tax/invoice state where required, exact Provider statement-line matches, settled customer usage, no open appeal, and no unresolved allocation change. The payout idempotency key remains constant across retries.

Sandbox payout is test-only. Every non-sandbox adapter is treated as production and is blocked until supplier-specific contract, tax, payment-destination, and security reviews are all `APPROVED`. The application checks this at settlement approval, queue claim, and completion; PostgreSQL also blocks the financial status transition.

## Reconciliation, failure, and records

Provider statements are imported by administrators and supplier bills remain separately identifiable. Differences open or update reconciliation evidence. Failed payouts use bounded retry with the same idempotency identity; operator-approved retries do not bypass disputes or readiness. Paid batches create a balanced journal and immutable payout entry.

Suspension or exit stops new accrual eligibility. Previously approved amounts remain governed by disputes, reconciliation, tax/invoice, payout readiness, legal hold, and the signed contract.
