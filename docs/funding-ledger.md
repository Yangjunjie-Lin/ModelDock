# Funding reservations and immutable ledger

ModelDock reserves the maximum customer charge before dispatching a priced
`/v1` inference request. The implementation added by migration 0009 is an
incremental replacement for the previous check-then-charge wallet flow; it
does not remove `wallets`, `wallet_transactions`, or any existing endpoint.

## Request lifecycle

1. The gateway estimates input tokens from the bounded JSON body and reads
   `max_output_tokens`, `max_completion_tokens`, or `max_tokens`. Text requests
   without an explicit maximum use 4096; embeddings use zero output tokens.
2. The pricing resolver creates an immutable price version and computes the
   maximum exact-decimal charge. Promotional balance is deliberately excluded
   from admission so concurrent requests cannot reserve the same promotion.
3. PostgreSQL locks the wallet row, checks prepaid funds or postpaid credit,
   inserts one `funding_operation`, posts a balanced RESERVATION journal, and
   moves the compatibility projection from available to reserved in one
   transaction.
4. Primary and safe pre-response fallback dispatches are recorded as separate
   `funding_provider_attempt` rows under the same operation. They never create
   separate customer charges.
5. Provider-reported usage is preferred. If a successful Provider omits usage,
   the gateway uses the admission input estimate and observed response bytes
   divided by four; this provenance is stored as `ESTIMATED_PROVIDER_MISSING`.
   Partial streams use `ESTIMATED_PARTIAL_STREAM`.
6. SETTLEMENT debits reserved funds and credits revenue. RELEASE returns the
   unused maximum. An estimation overage debits available funds, consumes the
   small risk allowance, and freezes new requests when the risk threshold is
   reached. The settlement runs on a bounded background context after client
   cancellation.
7. A replica-safe recovery worker settles stale reservations from their saved
   input estimate and observed output byte watermark. A later authoritative
   usage report uses the admin late-usage endpoint to post only the difference.

`Idempotency-Key` is optional for backward compatibility. When omitted, the
trusted ModelDock request ID is used, preserving the existing behavior of a new
logical request per HTTP call. Supplying the same key and identical request
returns `409 idempotent_replay` with `X-RelayDock-Original-Request-Id`; no funds
are reserved or charged again. Reusing it with another request returns
`409 idempotency_conflict`.

## Double-entry model and replay

- `ledger_account` contains per-wallet available/reserved liability accounts
  and system cash, revenue, and equity counterparts.
- `ledger_journal` is the journal header. It can move exactly once from DRAFT
  to POSTED after a database trigger verifies debit equals credit.
- `ledger_journal_entry` stores one DEBIT or CREDIT line with a positive
  `numeric(30,12)` amount.
- Posted journals and all journal entries reject UPDATE and DELETE. Corrections
  are REVERSAL or LATE_USAGE_ADJUSTMENT journals.
- `wallets.available_balance` and `reserved_balance` remain compatibility
  projections. Rebuild them by summing POSTED entries for the wallet accounts:

```sql
SELECT a.wallet_id,a.account_key,
  sum(CASE e.entry_side WHEN a.normal_side THEN e.amount ELSE -e.amount END) AS balance
FROM ledger_journal_entry e
JOIN ledger_journal j ON j.id=e.journal_id AND j.status='POSTED'
JOIN ledger_account a ON a.id=e.account_id
WHERE a.wallet_id IS NOT NULL
GROUP BY a.wallet_id,a.account_key;
```

Migration 0009 writes balanced opening journals for non-zero legacy wallet
balances, making an upgraded database replayable without altering those values.

## Failure and negative-balance policy

- PREPAID: the maximum reservation must fit available cash. No credit line is
  used. Estimation differences may settle into `risk_exposure`; reaching
  `risk_limit` freezes the wallet before another request.
- POSTPAID: new wallets enforce the explicit `credit_limit`. Existing zero-
  limit postpaid wallets retain their historical unlimited behavior through
  `credit_enforced=false` until an administrator opts them into a limit.
- Provider failure before any billable response releases the reservation and
  records FAILED. A partial stream charges the deterministic estimated usage.
- Provider timeouts use `RELAYDOCK_PROVIDER_TIMEOUT`. Stale recovery must be
  longer than that timeout.

## Operations and rollback

Configuration:

- `RELAYDOCK_PROVIDER_TIMEOUT` (default `10m`)
- `RELAYDOCK_FUNDING_RECOVERY_INTERVAL` (default `30s`)
- `RELAYDOCK_FUNDING_STALE_AFTER` (default `15m`, must exceed the Provider timeout)

Before deployment, validate an empty migration and an upgrade copy, then check
that every POSTED journal balances. Alert on stale PENDING/RESERVED operations,
FAILED settlements, frozen wallets, or risk exposure at/above the limit.

Migration 0009 is operationally forward-only. To roll application code back,
first stop admission, wait for or recover every PENDING/RESERVED operation,
verify balanced journals, and take a database backup. Older binaries can read
the retained wallet projection and transaction API, but they must not accept
new traffic because they do not reserve funds. Dropping the added tables or
triggers is permitted only in a disposable environment with no post-migration
business journals. Production rollback is a code rollback plus preserved
schema; settled financial evidence must never be deleted.

## Security boundaries

Funding records contain identifiers, token counts, byte counts, and monetary
amounts only. They never store prompts, responses, Provider secrets, downstream
API keys, cookies, or payment credentials. Admin late-usage and reversal writes
require authenticated administrator access, CSRF protection for cookie
sessions, an idempotency key, an audit record, and an immutable journal.
