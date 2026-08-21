# Subscriptions and metered Token billing

ModelDock maintains two independent commercial streams:

1. subscription invoices pay for finite product entitlements;
2. every Token request is priced and settled through the existing
   `model_price_version`, `usage_price_snapshot`, `billing_usage_records`,
   funding operation, wallet, and usage-revenue ledger path.

No subscription version accepts a Token entitlement. PostgreSQL constrains
`token_billing_mode` to `METERED_SEPARATE`, and `plan_entitlement` accepts only
the eleven supported non-Token keys. Free, Developer, Team, and Enterprise are
seeded as configurable templates. Operators publish changes by creating and
freezing a new version; frozen versions and their entitlements are immutable.
Existing subscriptions and invoice `plan_snapshot` values retain the old
version and fee.

Migration 0011 assigns pre-existing organizations a disabled-from-catalog,
finite `legacy-compat` frozen version so an in-place upgrade does not silently
change established `/v1` routing or control-plane behavior. It is not an
unlimited Token plan: Token usage remains metered. Organizations created after
migration receive Free. Operators may explicitly move grandfathered
organizations to the published templates.

## Entitlements and enforcement

The server enforces API Key count, active organization-member count, gateway
concurrency, organization RPM, log retention, advanced routing, cost analysis,
custom budgets, Webhook count, priority support, and SLA level. Creation
transactions lock the organization before counting resources so concurrent
requests cannot exceed a limit. Gateway RPM and concurrency use Redis atomic
counters and fail closed when Redis is unavailable.

Downgrades and expiry never delete API Keys, memberships, logs, Webhooks,
invoices, usage snapshots, or journals. Existing resources remain auditable,
but new creation is denied and Webhook fan-out is capped to the effective
limit. Immediately unpaid initial subscriptions do not grant paid
entitlements. Paid subscriptions retain entitlements during a configured
renewal grace period, then expire and receive a separate Free fallback.

## Lifecycle and operations

Statuses are `TRIALING`, `ACTIVE`, `PAST_DUE`, `GRACE_PERIOD`, `CANCELED`, and
`EXPIRED`. Upgrade/downgrade supports immediate or period-end change. Immediate
and period-end cancellation are explicit. The lifecycle worker creates renewal
invoices, records payment failure, starts grace, expires contracts, applies
scheduled versions, and activates the Free fallback. Every mutation requires
an idempotency key, writes an immutable `subscription_event`, and participates
in one PostgreSQL transaction. Enterprise versions use `CUSTOM` billing and
require administrator-supplied contract reference/start/end dates.

`RELAYDOCK_SUBSCRIPTION_POLL_INTERVAL` defaults to `1m`. Multiple replicas may
run the worker; row locks, `SKIP LOCKED`, and event uniqueness prevent duplicate
state transitions.

## Accounting and reconciliation

Subscription payment journals have no `wallet_id`. They debit
`system:subscription-cash:<currency>` and credit
`system:subscription-revenue:<currency>`. Recharge journals continue to credit
wallet liability, while Token settlement continues to credit
`system:revenue:<currency>`. Use `subscription_invoice.ledger_journal_id` to
reconcile plan fees, and `billing_usage_records`/`usage_price_snapshot` plus
wallet journals to reconcile Token fees.

## Migration 0011 and rollback

Before upgrade, back up PostgreSQL and verify migrations 1–10. Migration
`0011_subscriptions.sql` only adds tables, columns, accounts, triggers, seeded
templates, and Free subscriptions. It does not rewrite request usage, wallet
balances, recharge orders, price snapshots, or existing API contracts.

Preferred rollback is application rollback while retaining schema 11; older
binaries ignore the new tables. Destructive database rollback is allowed only
after proving there are no dependent subscription journals and exporting all
subscription invoices/events. In reverse dependency order: drop the
organization default-subscription trigger/function, immutable triggers,
subscription journal index/column, coupon redemptions, events, invoices,
trials, organization subscriptions, coupons, entitlements, plan versions, and
plans; then restore the migration-10 journal type constraint and remove the two
subscription system accounts. Never delete a posted subscription journal to
make rollback succeed. Restore the pre-upgrade database backup instead.

## Verification

Run:

```powershell
.\tests\integration\verify-subscriptions.ps1 -EnvFile .env -ConfirmIsolatedTestDatabase
```

The disposable-database test exercises concurrent limit enforcement, frozen
history, invoice immutability, separate accounting, immediate cancellation,
renewal failure, grace, expiry, Free fallback, and preservation of resources.
