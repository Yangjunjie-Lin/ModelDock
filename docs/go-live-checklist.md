# ModelDock commercial beta go-live checklist

**Decision: NO-GO**  
**Review date:** 2026-08-17 (Asia/Shanghai)  
**Requested release:** `v0.9.0-beta`  
**Release created:** no

This is an evidence record for the final commercial-beta validation. It is not
a release announcement. Passing synthetic integration tests does not override
a release gate. The blockers below must be closed and the complete suite must
be rerun against the exact release commit and immutable images before another
go-live decision.

## Release gates

| Mandatory gate | Result | Evidence and disposition |
| --- | --- | --- |
| Payment, wallet, usage, and Provider records balance | PASS in the isolated synthetic run | Four-way reconciliation completed with `differences=0`; exact amounts are recorded in [financial-reconciliation-report.md](financial-reconciliation-report.md). |
| No cross-organization authorization bypass | PASS in the tested path | A console user received `404` when reading another organization; project/key scope and disabled-user invalidation were also exercised. This does not replace an independent penetration test. |
| Only commercially approved Providers enter production routing | **BLOCKED** | Every standard Provider in the inspected database is `CONTRACT_PENDING / NOT_APPROVED`; legal entity, contract evidence, and processing regions are incomplete. See [provider-commercial-matrix.md](provider-commercial-matrix.md). |
| Backup can be restored | PASS for isolated logical-backup drill | `pg_dump`/`pg_restore` reproduced migration version 19 plus funding, posted-ledger, and audit counts. Production object-storage and managed-PITR recovery have not been evidenced. |
| Payment webhook replay cannot duplicate credit | PASS in sandbox | Signed payment webhook replay and refund replay each retained one durable successful record. No real payment channel was tested. |
| Negative-margin protection | PASS in the commercial decimal path | In-flight price snapshots and minimum-margin admission were tested. **BLOCKED overall** because legacy money/quota/catalog paths still use `float64` and PostgreSQL `::float8`. |
| Legal and Provider contract status is unambiguous | **BLOCKED** | License decision is `blocked / undecided`; all legal drafts are marked `PENDING COUNSEL REVIEW`; Provider contracts are not approved. |
| Release metadata matches requested version | **BLOCKED** | Runtime version is `2.0.0`; the requested version is `0.9.0-beta`; the release verifier accepts stable `MAJOR.MINOR.PATCH` only. |
| Final-image HIGH/CRITICAL vulnerability scan | PASS with operational caveat | Trivy 0.73.0 found 0 HIGH/CRITICAL vulnerabilities in Server, Admin, and Console using a database updated 2026-08-15 and still within its `NextUpdate` time. An attempted online database refresh failed because `mirror.gcr.io:443` refused the connection. |

Any single blocked row makes this review `NO-GO`. No tag, image publication, or
GitHub Release was created.

## End-to-end acceptance scenarios

All rows below passed in a disposable local Docker network with PostgreSQL 17,
Redis 7.4, a local mail capture, a signed sandbox payment channel, and a
deterministic synthetic OpenAI-compatible Provider. No real money, real API
key, or user data was used.

| # | Scenario | Result |
| ---: | --- | --- |
| 1 | User registration and captured-email verification | PASS |
| 2 | Organization, project, and `rdk_test_*` API key creation | PASS |
| 3 | Explicit subscription plan selection | PASS |
| 4 | Signed sandbox recharge | PASS |
| 5 | Exact cash-wallet credit | PASS |
| 6 | Ordinary OpenAI-compatible `/v1` completion | PASS |
| 7 | SSE streaming completion | PASS |
| 8 | Pre-dispatch Provider fallback | PASS |
| 9 | Client disconnect during upstream body streaming | PASS |
| 10 | Reserve, settle, and release accounting | PASS |
| 11 | Provider cost, retail price, promotion, and gross margin | PASS |
| 12 | Current monthly customer statement | PASS |
| 13 | Exact sandbox refund and replay | PASS |
| 14 | Payment/wallet/usage/Provider reconciliation | PASS, zero differences |
| 15 | Provider kill switch blocks new attempts | PASS |
| 16 | Suspended user invalidates API key immediately | PASS |
| 17 | Retention cleanup, deletion pseudonymization, and retained audit evidence | PASS |
| 18 | Logical backup restore with evidence-count comparison | PASS |
| 19 | Hard-killed app instance and replacement instance recovery | PASS |
| 20 | Payment webhook and request idempotency replay | PASS |

## Load and fault injection

| Test | Result | Verified invariant |
| --- | --- | --- |
| 8 concurrent ordinary requests | PASS | All returned 200 and reached settled operations. |
| 8 concurrent SSE requests | PASS | All returned 200 and reached settled operations. |
| 100 concurrent reservations against one wallet | PASS in the dedicated funding suite | Balance constraints, cancellation, recovery, and immutable balanced journals held. |
| Provider timeout, 429, and 500 | PASS | Operations failed terminally with zero settlement and full release. |
| Redis temporary outage | PASS | Request failed safely and no invalid reservation remained. |
| Ledger worker/app hard restart | PASS | Stale reservation recovered with `ESTIMATED_CRASH_RECOVERY`; reserved balance returned to zero. |
| Payment worker restart | PASS | Pending order reached its terminal expiry state without duplicate credit. |
| Price version switched while request was reserved | PASS | Settlement used the immutable request-time snapshot, not the later version. |
| PostgreSQL primary connection interruption | PASS | Readiness failed during pause and recovered after unpause; accounting invariants held. |
| One application instance failed | PASS | Replacement instance became ready and served a new `/v1` request. |

The concurrency numbers above establish deterministic regression coverage, not
a capacity limit or production SLO. Multi-AZ failover and sustained soak tests
remain outside this local test scope.

## Commands and observed results

| Command | Observed result |
| --- | --- |
| `gofmt -w cmd internal migrations tests/backend` | PASS; formatting completed. |
| `go vet ./...` (Go 1.26.6 container) | PASS. |
| `go test -count=1 -timeout=180s ./...` (Go 1.26.6 container) | PASS for all packages. |
| `npm ci`, `npm run lint`, `npm run typecheck`, `npm run build` in `apps/admin-web` | PASS; build warned about one 514.87 kB JavaScript chunk. |
| `npm ci`, `npm run lint`, `npm run typecheck`, `npm test`, `npm run build` in `apps/console-web` | PASS; Vitest 8/8. |
| `npm audit` in both frontends | PASS; 0 vulnerabilities reported. |
| `docker compose --env-file <isolated-env> config --quiet` | PASS for development configuration. |
| `docker compose --env-file <isolated-env> -f docker-compose.production.yml config --quiet` | PASS for production configuration. |
| `powershell -File tests/integration/verify-migrations.ps1 -ConfirmIsolatedTestDatabase` | PASS: empty migrations 1-19, idempotent restart, unknown/checksum rejection, populated V1 and V12 upgrades. |
| `powershell -File tests/integration/verify-funding.ps1 -ConfirmIsolatedTestDatabase` | PASS: concurrent reservation, cancellation, recovery, and ledger invariants. |
| `powershell -File tests/integration/verify-payments.ps1` | PASS: webhook replay, failure isolation, recovery, and traceability. |
| `powershell -File tests/integration/verify-pricing.ps1 -ConfirmIsolatedTestDatabase` | PASS: margin protection, versions, and promotions. |
| `powershell -File tests/integration/verify-subscriptions.ps1 -ConfirmIsolatedTestDatabase` | PASS: concurrency, lifecycle, retention, and ledger separation. |
| `powershell -File tests/integration/verify-financial-close.ps1 -ConfirmIsolatedTestDatabase` | PASS after reconciliation-query corrections. |
| `powershell -File tests/integration/verify-accounts.ps1 -ConfirmIsolatedTestDatabase` | PASS. |
| `powershell -NoProfile -ExecutionPolicy Bypass -File tests/integration/verify-commercial-onboarding.ps1 -ConfirmIsolatedTestDatabase -StartupTimeoutSeconds 120` | PASS twice; final run emitted all eight scenario summaries. |
| `docker build -f deploy/docker/Dockerfile.relaydock -t modeldock/go-live-server:local .` and equivalent Admin/Console builds | PASS for all three final local images. |
| Trivy 0.73.0 `image --skip-db-update --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1` | PASS; 0 findings in all three images using the still-current cached DB. |
| Gitleaks 8.30.1 repository scan | PASS; 3 commits/~1.52 MB scanned, no leaks found. |
| `powershell -File scripts/verify-release.ps1 -Version 0.9.0-beta -RequireApprovedLicense` | EXPECTED FAIL (exit 1): verifier requires stable `MAJOR.MINOR.PATCH`. |
| `powershell -File scripts/verify-release.ps1 -Version 2.0.0 -RequireApprovedLicense` | EXPECTED FAIL (exit 1): repository owner has not approved the licensing decision. |

The literal `<isolated-env>` above denotes a generated, untracked test
environment file; its secret values are intentionally not recorded.

## Required closure before re-review

1. Obtain counsel and owner approval for licensing and all public legal text.
2. Populate and independently approve Provider legal entity, resale contract,
   dates, permitted customer regions, data-processing regions, retention terms,
   and terms version. Keep the Provider excluded until every field is verified.
3. Replace all remaining monetary `float64`/`::float8` paths with exact decimal
   or defined minor-unit representations through new forward migrations where
   schema changes are required; add compatibility and concurrency tests.
4. Resolve the version policy: either authorize a prerelease-aware verifier and
   align runtime/changelog metadata, or choose the release version already
   represented by the source. This requires a separate, explicit release step.
5. Exercise an approved payment sandbox owned by the intended payment partner,
   an approved Provider contract, production SMTP, managed PITR/object backup,
   and deployment-environment failover.
6. Refresh the vulnerability database online and repeat scans on images built
   from the exact immutable release commit.
7. Rerun every command above. Any financial imbalance, authorization bypass,
   duplicate credit, negative-margin admission, backup failure, or contract
   ambiguity remains an automatic `NO-GO`.
