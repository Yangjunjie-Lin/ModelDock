# Financial close and reconciliation

Migration `0012_financial_close.sql` establishes the trace from verified customer
payments through wallet journals and request charges to provider attempts and
imported provider statement lines. The additive
`0013_financial_close_hardening.sql` migration closes concurrency, bidirectional
reconciliation, durable export, and actual Provider expense-journal gaps found
during production-readiness review. Both are additive: `/v1`, `rdk_*` keys, all
existing fields, and `RELAYDOCK_*` compatibility remain unchanged.

## Evidence and money model

All monetary values stay in PostgreSQL `NUMERIC(30,12)` and cross finance API
boundaries as decimal strings. A paid recharge creates one linked wallet
transaction, balanced posted journal, and refundable cash lot. Usage settlement
links `request_logs`, `billing_usage_records`, `funding_operation`, its wallet
transaction/journal, and `funding_provider_attempt`. Promotional balances are
reserved and consumed independently and are never refundable cash. Cash-lot
allocations record FIFO consumption; reversal allocations restore the original
source without rewriting earlier evidence. Credit is the portion below zero,
bounded by the configured credit limit.

During a schema-12 to schema-13 upgrade, active aggregate funding reservations
are attributed without exposing reserved cash as refundable: cash that schema
12 had already moved out of the available balance is represented by a
non-refundable migration lot, while any unmatched hold remains credit. Existing
`CREATED`/`PENDING` payment refunds receive a source-lot hold during the same
migration; migration aborts if the promised unused cash is no longer present.
Refund failure releases that hold exactly once and records an audit event.
Chargebacks attribute their entire wallet debit: remaining source cash is a
cash allocation and the already-consumed remainder is a credit/receivable
allocation.

Posted journals, entries, provider statements, statement lines, invoice source
items, reconciliation observations, and resolutions are immutable. Financial
operators cannot edit a settled journal. A repair must use a new balanced
reversal journal, name the original journal and reconciliation case, and retain
the handler, reason, and idempotency key.

## Customer workflows

The console exposes recharge and subscription history, request-level Token
charges and provider trace IDs, monthly statements, cash/bonus/credit balance
composition, CSV downloads, refund applications, and invoice applications.
Refund eligibility separates unused recharge cash, already-consumed service,
non-refundable bonus, subscription fee, and observed irreversible provider
cost. Only unused attributed recharge cash can enter the external payment
refund path. Subscription applications remain a reviewed financial workflow;
they do not falsely report a provider refund without external evidence.

Invoice support is deliberately limited to application, status, amount/source
validation, approval, and CSV export. Eligible recharge revenue is net of
successful refunds and excludes chargebacks; paid subscription revenue is net
of completed refund applications. Rejected or canceled invoice applications
release their source claims. Refund and invoice attribution share an
organization/currency transaction lock and cannot claim the same source amount.
The admin export persists an
immutable batch, exact CSV artifact, and SHA-256 before marking its approved set
`EXPORTED`; a retry with the same batch key downloads identical bytes. ModelDock is not connected to a tax
authority in this phase and does not claim to issue tax invoices automatically.
Organization-scoped accounting CSV includes both wallet journals and the
walletless subscription-payment journals attributed through their invoice.
Financial reports use accounting evidence rather than mutable usage labels:
user revenue is the net credit of posted usage/subscription revenue accounts,
Provider cost is grouped from statement lines by their actual usage date, and
gross margin converts a uniquely matched Provider statement line into the
customer currency using the request's immutable `usage_price_snapshot`
exchange rate before subtracting it from posted revenue. A statement line with
missing, ambiguous, or inconsistent usage/pricing evidence remains a positive
`unallocated_cost` in its original Provider currency with a machine-readable
reason; it is visible to finance but excluded from every gross-margin total.
No runtime FX lookup, binary floating-point arithmetic, or cross-currency sum is
used. The admin UI displays exact-decimal totals per currency and shows
unallocated Provider cost separately.

Multiple refund applications against the same paid subscription invoice are
serialized on that invoice. The sum of all non-rejected/non-failed applications
cannot exceed the exact paid subscription amount, including concurrent requests.

## Daily reconciliation

The worker wakes every `RELAYDOCK_RECONCILIATION_INTERVAL` (default `15m`) and,
after `RELAYDOCK_RECONCILIATION_RUN_AT` UTC (default `02:00`), reconciles the
previous UTC business date. A unique daily run key makes retries and multiple
replicas safe. A repeated request for an already completed business date returns
the original durable run; it does not refresh that historical snapshot. It checks:

1. payment-channel evidence against recharge orders;
2. recharge orders against wallet transactions and balanced journals;
3. API usage against customer charges;
4. API usage against Provider attempts/upstream request IDs;
5. Provider usage/cost against imported Provider statement lines;
6. subscription invoices against subscription state and journals.

Every mismatch receives a deterministic classification and durable queue case.
A later matching observation does not silently close it. A named finance/admin
operator must accept the exception or create a reversal and must provide a
reason and idempotency key.

All timestamp-backed business-day predicates use explicit half-open UTC bounds;
they do not inherit the PostgreSQL session time zone. The run row commits before
the snapshot transaction begins, so a query or row-scan failure remains a
terminal `FAILED` run with `completed_at`, `error_code`, and failed-check
summary instead of disappearing with rolled-back observations.

Payment-channel matching requires independent evidence: a verified signed
webhook, an actual Provider reconciliation API response, or a reviewed manual
transfer reference. An adapter without an independent query source must not
echo local status as Provider truth. Those orders are classified
`PAYMENT_CHANNEL_RECONCILIATION_UNSUPPORTED` and stay in the handling queue;
this release does not invent a channel API that has not been implemented.

Because the daily snapshot is immutable, Provider statements or other evidence
arriving after that business date do not rewrite the completed run. Operations
must handle the retained case explicitly and, when needed, record a documented
exception; backfill/reopen automation is outside this step.

Provider statement import requires an enabled Provider with an active contract
and an allowed two-letter region; linked usage must also belong to an
organization whose billing region matches the statement (or `*`). The total
must exactly equal its lines and
the supplied SHA-256 is stored as immutable source evidence. Exact replays are
checked against a canonical full-payload fingerprint; reusing a reference or
source hash with changed header, line, token, amount, or metadata fields fails.
The imported total
also creates one balanced Provider-expense/Provider-payable journal, linked to
the statement, so the accounting export closes actual Provider cost. Imports do not
contain credentials or payment secrets.

The Provider-bill check scans in both directions. Usage with zero, multiple, or
mismatched statement lines creates a case, and statement lines with no matching
usage (or ambiguous matching usage) create a separate durable case. A `LIMIT 1`
match cannot hide duplicate Provider charges. A posted source journal may also
be reversed at most once across all reconciliation cases; another case must
reference the existing correction or be accepted with a documented reason.

## Operations

Before rollout, back up PostgreSQL, validate Compose, run migration verification
on both an empty database and a populated pre-upgrade copy, and run
`tests/integration/verify-financial-close.ps1`. Alert on failed reconciliation
runs, newly opened critical/high cases, old open cases, missing Provider
statements, and any wallet attribution gap.

CSV files neutralize spreadsheet formulas. Limit finance/admin access to the
documented RBAC roles, retain audit logs according to policy, and never log tax
identifiers, statement source files, payment secrets, or provider credentials.
Customer CSV queries apply the requested month in PostgreSQL and use the
documented 100,000-row finance ceiling; they do not inherit the 200-row
interactive-list pagination limit. Accounting export is likewise bounded at
100,000 rows per request; operations must split large exports into narrower
half-open UTC date intervals because the CSV response is not a paged artifact.
PostgreSQL sessions are pinned to UTC so host/database timezone settings cannot
move financial evidence across days.

## Rollback

Migrations 0012 and 0013 are forward-only once any finance evidence exists. The preferred
rollback is an application rollback that leaves schema 13 and all evidence in
place. Disable the new worker/endpoints, keep the tables and columns, and run an
older binary only after confirming it tolerates additive schema changes.

A destructive down migration is intentionally not shipped because dropping
cash attribution, refund/invoice decisions, statements, cases, or reversals
would break auditability. If migration 0012 or 0013 fails before its transaction
commits, PostgreSQL rolls it back atomically. If a database-level rollback is
required after commit, stop writes and restore the verified pre-upgrade backup;
do not delete posted evidence or remove columns in place.
