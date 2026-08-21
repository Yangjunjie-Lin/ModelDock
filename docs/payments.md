# Verified recharge orders

ModelDock's payment subsystem creates durable recharge orders without binding
the product to an unsigned merchant account. The first adapters are:

- `sandbox`: test-only, signed HMAC-SHA256 webhook simulation. It moves no real
  funds and is never reported as production-ready.
- `manual_transfer`: an administrator-reviewed evidence workflow. Bank details
  and payment credentials are supplied out of band and are not stored in the
  ModelDock database. Evidence references must be opaque internal identifiers,
  never bank account numbers, payer names, or other personal data.

Both adapters are disabled by default. A formal payment plugin implements
`internal/payment.Provider`: `CreatePayment`, `QueryPayment`, `VerifyWebhook`,
`RefundPayment`, `ClosePayment`, and `ReconcilePayment`. Registration is gated
by an enable switch, contract status, and explicit allowed-region list.
Adapters must use the platform order/refund number as the upstream idempotency
key because ModelDock retries an ambiguous create or refund after a process
exit. An adapter that cannot prove this property must remain disabled.

## State and transaction boundary

The order states are `CREATED`, `PENDING`, `PAID`, `CREDITED`, `FAILED`,
`EXPIRED`, `REFUND_PENDING`, `REFUNDED`, and `CHARGEBACK`. Provider acceptance
first creates durable `PAID` evidence. `CreditPaidRecharge` then locks the
order and wallet and commits all of the following in one PostgreSQL transaction:

1. wallet `available_balance` increase;
2. one immutable `TOPUP` wallet transaction;
3. one balanced `PAYMENT_CREDIT` journal and two entries;
4. the order transition to `CREDITED` with both link IDs;
5. the audit record.

The unique order links and row locks make concurrent delivery safe. If a
process exits after the verified webhook transaction but before credit, the
payment worker finds `PAID` orders and retries the same transaction. A failed
or expired order cannot enter the credit path.

Refunds use the inverse `PAYMENT_REFUND` journal and atomically link
`refund_order`, `wallet_transactions`, and `ledger_journal`. This release allows
full refunds only; a partial-refund policy is intentionally not inferred.

## Webhook security

Sandbox webhooks use these headers:

- `X-Payment-Timestamp`: Unix seconds;
- `X-Payment-Event-Id`: event ID repeated in the JSON body;
- `X-Payment-Signature`: lowercase hex HMAC-SHA256 over
  `<timestamp>.<raw request body>`.

The adapter verifies the signature with constant-time comparison and rejects a
timestamp outside `RELAYDOCK_PAYMENT_WEBHOOK_TIMESTAMP_SKEW`. PostgreSQL unique
constraints on `(provider,event_id)` and `(provider,replay_key)` reject replay.
Only the SHA-256 digest and normalized non-secret fields are retained; the raw
payload and signature are not stored.

The console success page only polls a protected order endpoint. It cannot post
a wallet transaction. An optional active-query endpoint asks the adapter for a
server-side result, then uses the same `PAID` and atomic-credit path.

## Operations

Relevant settings:

| Variable | Purpose |
| --- | --- |
| `RELAYDOCK_PAYMENT_ORDER_TTL` | Time before an unpaid order is expired and closed |
| `RELAYDOCK_PAYMENT_POLL_INTERVAL` | Credit/expiry/refund recovery poll interval |
| `RELAYDOCK_PAYMENT_WEBHOOK_TIMESTAMP_SKEW` | Maximum signed-webhook clock difference |
| `RELAYDOCK_PAYMENT_ALLOWED_REGIONS` | Explicit comma-separated ISO alpha-2 allowlist |
| `RELAYDOCK_PAYMENT_SANDBOX_ENABLED` | Enables the test-only adapter |
| `RELAYDOCK_PAYMENT_SANDBOX_SECRET` | HMAC secret supplied by environment/secret manager |
| `RELAYDOCK_PAYMENT_MANUAL_ENABLED` | Enables administrator-reviewed manual transfer |

Rotate the sandbox secret by disabling order creation, allowing outstanding
sandbox orders to expire, replacing the injected secret, and restarting the
service. Do not put formal-channel merchant keys in `system_settings`, adapter
metadata, database columns, source code, screenshots, or logs.

Monitor `payment_credit_failed`, `payment_credit_recovery_failed`,
`payment_expiry_failed`, and `payment_refund_recovery_failed`. Reconcile an
order from Admin after consulting the provider's authoritative record; the
result is immutable evidence and does not silently rewrite either side.

## Migration and rollback

Migration `0010_payment_orders.sql` is forward-only. Before deployment, back up
PostgreSQL and verify migrations on an empty database and a populated pre-0010
database.

Application rollback is safe only while the new tables contain no financial
records: stop all ModelDock replicas and workers, disable both payment adapters,
verify `recharge_order`, `refund_order`, `payment_webhook_event`, and
`payment_reconciliation_record` are empty, then restore the pre-migration
backup. Once any payment evidence or journal is posted, do not drop or rewrite
it. Keep schema 0010, run the older gateway only if it tolerates the additive
schema, and perform a forward corrective deployment. This preserves audit and
accounting evidence.
