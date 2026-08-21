# Known limitations for the commercial beta candidate

**Release disposition:** `NO-GO` as of 2026-08-17.

This list distinguishes tested product behavior from unresolved production
readiness. A limitation described here is not an approval to accept it.

## Release blockers

1. **Licensing is undecided.** `docs/licensing-decision.md` records
   `Decision status: blocked` and `Selected option: undecided`; there is no
   approved distribution grant.
2. **Public legal documents are drafts.** Service terms, privacy, acceptable
   use, refund, data-processing, Provider/model disclosure, and company/contact
   information all remain `PENDING COUNSEL REVIEW`.
3. **No standard Provider is commercially admitted.** All eight standard rows
   inspected in the current database are `CONTRACT_PENDING / NOT_APPROVED`,
   with blank legal entity/terms evidence and empty processing-region evidence.
   Their technical `enabled` flag does not make them eligible for paid routing.
4. **Legacy monetary representations violate the release requirement.** The
   commercial funding ledger uses PostgreSQL `NUMERIC` and Go exact decimal
   handling, but older budgets, catalog/model prices, usage aggregates, and
   cost APIs still contain `float64` and PostgreSQL `::float8` conversions.
5. **Release identity is inconsistent.** Source metadata is `2.0.0`; the
   requested release is `v0.9.0-beta`; the release verifier rejects prerelease
   SemVer and requires stable `MAJOR.MINOR.PATCH`.

## Environment and integration limits

- Payment verification used only the built-in signed sandbox adapter. No real
  acquirer account, settlement file, chargeback, tax, invoice, or production
  webhook endpoint was used.
- Provider verification used only a deterministic local mock with synthetic
  contract metadata. It demonstrates fail-closed logic, fallback, streaming,
  and accounting, but not the behavior or commercial rights of a real Provider.
- Email verification used the local capture provider and `.invalid` addresses;
  delivery, bounce handling, reputation, and production-domain configuration
  are not proven.
- The app-failure test hard-killed one local container and started a replacement.
  It is not a multi-node, cross-zone, load-balancer, or orchestration control
  plane failover test.
- The database interruption used Docker pause/unpause. It verifies readiness
  and recovery behavior but not managed-primary promotion, replication lag, or
  split-brain protection.
- The backup drill restored a logical custom-format dump to an isolated local
  PostgreSQL 17 instance. Production object-storage access, encryption keys,
  retention, WAL/PITR, RPO, and RTO have not been demonstrated.
- Redis interruption was temporary and local. Sustained outage, cluster
  promotion, and eviction behavior need environment-specific validation.
- Concurrent coverage is deterministic (8 ordinary, 8 SSE, and 100 dedicated
  wallet reservations), not a sustained load/soak or capacity benchmark.

## Security and supply-chain limits

- Trivy found zero fixed HIGH/CRITICAL vulnerabilities in the final local
  images using a database updated 2026-08-15 and still within its `NextUpdate`
  time. The attempted online database refresh failed because the registry
  connection was refused, so release-day online refresh remains required.
- Gitleaks and npm audit passed, but no independent penetration test, external
  attack-surface scan, SAST beyond repository CI, or third-party dependency
  license opinion was provided in this review.
- The E2E test intentionally enabled HTTP/private-network Provider access inside
  its isolated Docker network. Production must retain HTTPS/public-host
  allowlisting and must not copy those test-only settings.

## Product and operational limits

- The Admin production build reports one 514.87 kB JavaScript chunk. This is a
  performance warning, not a correctness failure, but should be measured on the
  intended client/network profile.
- Provider kill switch stops new selections and attempts; it deliberately does
  not terminate an already-dispatched stream. Existing calls must be observed
  until settlement reaches a terminal state.
- Crash recovery may settle from an explicit estimated usage source when the
  upstream response is lost. Reconciliation alerts and manual review remain
  necessary for such requests.
- Database migrations are forward-only. Routine application rollback keeps the
  upgraded schema; destructive schema rollback is an exceptional restore
  procedure with potential loss of post-backup evidence.

The authoritative blocker disposition and retest requirements are in
[go-live-checklist.md](go-live-checklist.md).
