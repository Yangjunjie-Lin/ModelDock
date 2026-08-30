# Commercial beta rollback plan

Supplier settlement migration 0022 is forward-only. Disable every supplier settlement policy and payout adapter, resolve all `PROCESSING` attempts by the stable payout idempotency key, and retain all posted journals and append-only subledger evidence. The guarded empty-install-only schema reversal and its evidence preconditions are specified in [supplier-settlement.md](supplier-settlement.md#migration-and-rollback); a non-empty production system requires a new compensating forward migration.

**Current decision:** `NO-GO`; no `3.0.0-beta.1` deployment or Release exists to
roll back. This document defines the required procedure for a later approved
candidate and the safe reversal of the validation-only code changes in this
step.

## Principles

- Preserve `/v1` compatibility, `rdk_*` keys, `RELAYDOCK_*` settings, database
  columns, migration ledger, funding journals, usage snapshots, payment events,
  Provider attempts, and audit history.
- Stop new financial exposure before changing data. Never repair an imbalance
  by deleting or editing posted ledger rows.
- Routine rollback changes the application image while keeping forward database
  migrations. A destructive schema downgrade is not routine rollback.
- Every operator action needs an incident ID, actor, UTC timestamp, reason,
  immutable image digest, database recovery point, and reconciliation result.
- A rollback is incomplete until readiness, idempotency, wallet balance,
  Provider admission, and four-way reconciliation are verified.

## Rollback triggers

Start rollback immediately for any of the following:

| Trigger | Immediate containment |
| --- | --- |
| Any ledger, wallet, payment, usage, or Provider-statement imbalance | Stop new paid requests and recharges; preserve workers/logs; snapshot database; do not mutate posted evidence. |
| Cross-organization access | Remove candidate traffic, suspend affected credentials/sessions, preserve audit evidence, start security incident response. |
| Unapproved Provider receives an attempt | Enable its kill switch, stop candidate routing, enumerate all attempts since admission, reconcile and notify legal/commercial owners. |
| Duplicate payment credit/refund | Disable the affected webhook/channel, preserve raw event digest and order evidence, stop financial workers only if replay cannot be isolated. |
| Negative-margin request admitted | Disable the affected price/model/Provider route, freeze the price version, reconcile all requests using it. |
| Backup cannot be restored | Stop rollout; retain current healthy system; repair backup/PITR before any further schema or image change. |
| Data corruption, migration failure, or readiness regression | Stop migration writers, retain failure logs, and choose application rollback or point-in-time restore based on whether committed data is trustworthy. |

## Prepared evidence before deployment

Before a future deployment, record:

1. current and candidate image digests for Server, Admin, and Console;
2. Git commit, source version, changelog entry, and approved license decision;
3. `docker compose ... config --quiet` output status and migration plan;
4. encrypted logical backup checksum plus managed PostgreSQL recovery point;
5. a completed isolated restore drill and the approved RPO/RTO;
6. wallet totals by currency, posted journal debit/credit totals, open funding
   operations, payment orders/webhooks/refunds, unclosed usage, and open
   reconciliation cases;
7. Provider commercial matrix, contract windows, kill switches, region rules,
   price versions, and minimum margins;
8. on-call, finance, security, legal, payment-partner, and Provider escalation
   contacts stored in the protected incident system rather than this repository.

## Application rollback

For a Compose production installation, use the existing guarded script with an
immutable previously verified tag:

```bash
./deploy/production/scripts/rollback.sh /opt/relaydock <previous-immutable-tag>
```

The script validates Compose configuration, pulls only the `relaydock` service,
replaces its replicas without dependencies, and waits up to 120 seconds for
readiness. Do not substitute a mutable `latest` tag.

After the Server is healthy:

1. deploy the matching previous Admin and Console assets by their immutable
   digests;
2. verify `/healthz`, `/readyz`, login, organization scoping, `/v1/models`, one
   ordinary request, one SSE request, and one idempotent replay using a synthetic
   canary tenant;
3. verify suspended users and Provider kill switches still fail closed;
4. let terminal financial workers drain, then run payment/wallet/usage/Provider
   reconciliation for every affected business date;
5. compare wallet cash/promotion/reserved balances and posted journal totals to
   the pre-deploy snapshot; any difference keeps traffic disabled.

If the previous binary cannot read the forward schema, do not improvise DDL.
Return to the candidate, keep paid traffic disabled, and proceed to database
recovery review.

## Migration 0025 Attestation and Decimal hardening rollback

`0025_commercial_attestation_and_decimal_hardening.sql` is additive and
forward-repair only. Older application binaries ignore its append-only
Attestation verification audit, Runtime readiness views, and Decimal integrity
function. Do not delete verification audit rows. Correct readiness logic with a
new migration and regenerate the exact-commit Runtime Attestation; never
rewrite settled amounts or a signed Artifact.

## Migrations 0026-0027 provisioning and routing rollback

`0026_provider_account_provisioning.sql` and
`0027_openrouter_operating_model.sql` are additive and forward-repair only.
Before rolling an application binary back, stop Provider provisioning workers,
disable automatic enterprise provisioning and SCIM, retain every binding/job,
capability document, shadow-spend row, free-usage counter, and identity audit
record, and confirm the older binary ignores the new columns/tables. Do not
delete a Provider binding, funding policy snapshot, SCIM link, or superseded
capability document to make an older UI appear consistent. Correct schema or
policy behavior with a later migration.

## Database rollback and recovery

The current forward schema includes migrations 1 through 27. Migrations
0021–0027 are additive; before an application rollback disable Provider quality
policies and every supplier-linked Provider, then retain all quality,
Marketplace, settlement, payout-readiness, lifecycle, and audit evidence.
Routine application rollback retains every migration and never deletes
`schema_migrations` rows.

For a suspected data-corruption event:

1. freeze new writes and record the last trusted UTC transaction/recovery point;
2. preserve the live database, WAL, application/worker logs, reconciliation
   cases, and backup checksums;
3. restore into an isolated database first; never overwrite the only copy;
4. validate migration ledger/checksums, audit hash columns/continuity, users and
   organization boundaries, Provider governance, payment events/orders/refunds,
   funding-operation terminal equations, wallet balances, ledger debit=credit,
   usage snapshots, monthly statements, and reconciliation;
5. obtain finance and incident-commander approval for the selected recovery
   point and document all transactions that would be lost;
6. switch traffic only after the restored database passes application readiness
   and the full financial/security smoke suite.

The repository drill for a logical backup is:

```bash
./deploy/production/scripts/restore-drill.sh /protected/path/modeldock.dump
```

`backup-pitr.sh` creates a checksum-protected logical dump and can upload it to
an `s3://` destination with server-side encryption. Managed continuous WAL/PITR
must be operated and tested separately. A logical dump alone cannot prove a
production RPO.

Destructively reversing migrations 14-27 would remove or weaken commercial,
pricing, funding, payment, reconciliation, governance, observability, and
onboarding/Provider-quality evidence. It requires a separately reviewed data-migration plan,
stopped writers, complete exports, dependency analysis, legal/finance approval,
and reconciliation before and after. It is not authorized by this plan.

## Financial worker recovery

- Keep the funding recovery worker active unless it is itself corrupting data;
  it must turn stale reservations into terminal operations where
  `settled_amount + released_amount = maximum_amount`.
- Keep payment event idempotency evidence. Restarting a worker must process the
  existing event/order, not create another event or top-up transaction.
- Do not manually set wallet balances. Corrections require a new, approved,
  idempotent compensating journal linked to the incident and original evidence.
- Refund reversals use the payment/refund workflow and exact currency amount;
  they are not negative ad-hoc wallet updates.

## Validation-only code reversal

This step changed runtime/test behavior without a schema migration:

- monthly-statement SQL avoids the reserved alias `month`;
- reconciliation buffers query rows before transactional writes and treats
  nullable/waived evidence safely;
- gateway cancellation during upstream reads remains `client_disconnected`;
- finance reconciliation failures use sanitized structured logging;
- the mock Provider supports test-only SSE chunk delay;
- the commercial onboarding script covers the final E2E/fault suite.

If any of these changes must be reverted, create a normal inverse commit after
review; do not discard the dirty worktree or unrelated changes. Re-run `gofmt`,
`go vet`, `go test`, frontend checks, migrations, finance suites, and the full
commercial onboarding suite. No database rollback is associated with these
files.

## Completion criteria

Rollback closes only when:

- service is stable on an identified immutable image;
- no unauthorized Provider or user/key can create a new attempt;
- every funding operation in the incident window is terminal or explicitly
  quarantined with an owner;
- journal debits equal credits by currency and wallet reserved balances match
  open operations;
- payment, wallet, usage, and Provider reconciliation reports zero unexplained
  differences;
- backup of the recovered state is created and restored successfully; and
- finance, security (when applicable), operations, and incident commander sign
  the incident record.
# V23 Marketplace launch acceptance rollback

`0023_marketplace_launch_acceptance.sql` is forward-only and additive. Leave
V23 tables and columns in place; do not edit the migration checksum or drop
evidence tables during an incident. A pre-V23 binary in migrate-on-start mode
rejects the newer ledger by design, so any temporary older binary requires the
externally owned migration/verification deployment mode and explicit
compatibility review.

For behavioral rollback, use `EMERGENCY_CUTOVER` or `SUSPEND`, disable the
supplier-linked Provider, and change any production payout-readiness decision
back to `PENDING` with an audited reason. Existing first-party `/v1` routes,
`rdk_*` keys, and `RELAYDOCK_*` variables are unchanged. Pre-V23 binaries do not
understand the new Marketplace gate, so keep all supplier-linked Providers
disabled until every replica again runs a V23-aware binary.

A schema down-migration may be authored only after all V23 binaries are retired,
no payout is processing, lifecycle and gate evidence has been exported under
retention policy, and finance/security/legal owners approve removal. It would
drop the two protection triggers first and the V23 tables last; this repository
intentionally does not ship an automatic destructive rollback.

## Migration 0024 exact-money rollback

`0024_exact_money_and_release_evidence.sql` is forward-only. Application
rollback is compatible because every historical money column remains present
and bidirectional triggers deterministically mirror legacy-only writes into the
new `NUMERIC(30,12)` columns. During this dual-write phase, explicit upper
bounds keep exact values within the range that the legacy `NUMERIC(20,8/10)`
columns can mirror without overflow or a rounding carry. A later removal of
legacy columns requires a separate forward migration and compatibility review.
Before deploying an older binary, run
`SELECT * FROM exact_money_migration_differences WHERE differences <> 0`; any
row is a stop condition requiring forward repair. Do not drop the exact columns
or triggers on a database containing post-0024 writes. If forward repair is
impossible, restore a verified pre-migration backup and separately preserve all
post-backup ledger, request, payout, and audit evidence for reconciliation.
