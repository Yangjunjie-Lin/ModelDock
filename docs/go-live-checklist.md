# ModelDock commercial readiness go-live checklist

**Decision: NO-GO**
**Review date:** 2026-08-21 (Asia/Shanghai)
**Candidate version:** `3.0.0-beta.1`
**Baseline commit:** `9ef82a669b704028f1919e094b0bd82a11dba866`
**Latest migration:** `0024_exact_money_and_release_evidence`
**Release created:** no

This is an engineering revalidation record, not a release announcement. The
tests below ran against the current uncommitted working tree on
`fix/commercial-readiness-closure`; therefore the local result is useful
engineering evidence but is not an exact-clean-commit release artifact. The
protected CI and Release workflows must rerun the same suites after review and
commit. No Tag, GitHub Release, production image promotion, or deployment is
authorized by this record.

## Decision by release profile

| Profile | Result | Reason |
| --- | --- | --- |
| `ENGINEERING_PREVIEW` | **GO** | Exact-money, migrations, synthetic commercial/Marketplace tests, version metadata, and fail-closed gate tests pass. Preview artifacts are explicitly non-production. |
| `COMMERCIAL_BETA` | **NO-GO** | License, legal entity/text, payment agreement/adapter, Provider rights, production SMTP/DR, current vulnerability scan, independent security assessment, and owner approvals are blocked. |
| `MARKETPLACE_PRODUCTION` | **NO-GO** | Commercial Beta blockers remain, and no real supplier KYB/contract, tax/invoice, production payout, or two-administrator launch evidence exists. |

## Mandatory gates

| Gate | Result | Evidence and disposition |
| --- | --- | --- |
| Exact money across monetary code paths | **PASS (engineering)** | Migration 0024 adds `NUMERIC(30,12)` compatibility columns and reconciliation; `scripts/verify-exact-money.ps1` passes. Retained floating-point entries are explicitly non-monetary telemetry/routing values. |
| Migration and compatibility contract | **PASS (engineering)** | Empty 1–24 migration, idempotent restart, unknown/checksum rejection, V1/V12/V21/V22 upgrades, 12-place writes, legacy writes, and zero migration differences passed. |
| Commercial and Marketplace integration | **PASS (engineering)** | `scripts/verify-commercial-integration.ps1` completed 14/14 suites and recorded schema 24 plus one local image digest. Because the tree is uncommitted, the artifact is not release evidence for the baseline SHA. |
| Wallet, ledger, usage, and Provider records balance | **PASS in synthetic tests** | Concurrent reserve/settle/release, reversals, refunds, Supplier payable, and four-way close completed with zero unexplained differences. No real acquirer or Provider statement was tested. |
| Version and image identity | **PASS (engineering)** | `VERSION`, runtime, Docker labels, OpenAPI, Changelog, and release metadata use `3.0.0-beta.1`; SemVer 2.0 prerelease validation passes. |
| Release cannot be bypassed by one approval | **PASS (engineering)** | Nine negative tests reject license-only approval, stale Migration 19 evidence, another commit, sandbox payment/payout, missing Provider rights, expired/future-dated approval, and approval for another commit. |
| Production payment and refund channel | **BLOCKED** | Only signed sandbox and administrator-reviewed manual-transfer adapters exist; no approved production acquirer or real settlement/chargeback evidence exists. |
| Commercially approved Provider | **BLOCKED** | Approved Provider count is zero. Standard Providers remain fail-closed without contract, resale, customer-region, and data-processing-region evidence. |
| Production supplier payout | **BLOCKED** | The payout adapter is permanently sandbox-only; no bank/payout institution or real idempotent payout evidence exists. |
| License, entity, legal text, tax, and invoice approval | **BLOCKED** | Repository evidence remains `BLOCKED`; the coding agent did not select a license or invent legal/tax approval. |
| Production SMTP, PITR, failover, and multi-zone recovery | **BLOCKED** | Local capture, logical restore, pause/unpause, and container replacement passed but do not prove managed production recovery. |
| Independent security assessment | **BLOCKED** | No independent penetration test or production TLS/WAF/DNS/IAM/KMS/logging assessment was supplied. |
| Current HIGH/CRITICAL image scan | **NOT RUN / BLOCKED** | Trivy 0.73.0 was available, but both `mirror.gcr.io` and GHCR vulnerability-database downloads failed with TLS handshake timeouts and the local cache was empty. No clean result is claimed. |
| Required human sign-off | **BLOCKED** | Finance, operations, security, legal, and repository-owner evidence remains absent. |

Any blocked mandatory row keeps the commercial decision `NO-GO`.

## Marketplace Prompt 15–18 revalidation

The former Migration-19 report is not used as Marketplace acceptance evidence.
The current suites explicitly upgraded populated V21 and V22 fixtures through
Migration 24 and verified:

- supplier registration, KYB pending/default-deny, separate approvers, endpoint
  ownership, SSRF defenses, model/price applications, and suspension/exit;
- platform quality probes, canary routing, Provider faults, kill switch, and
  emergency cutover;
- platform-measured settled usage as the only payable source, commission,
  reserve, refunds, bills, disputes, settlement batches, and payout replay by a
  stable idempotency key; and
- historical ledger/usage/supplier evidence retention after suspension and
  exit, while sandbox payout remains ineligible for production readiness.

These are synthetic engineering assertions. They do not prove that any real
supplier has completed KYB, signed a contract, received traffic, or been paid.

## Commands and observed results

| Command or suite | Observed result |
| --- | --- |
| `gofmt`, `go vet ./...`, `go test -count=1 -timeout=300s ./...` | PASS on Go 1.26.6. |
| Admin `npm ci`, lint, typecheck, build, audit | PASS; audit reported 0 vulnerabilities. No Admin test script is defined. |
| Console `npm ci`, lint, typecheck, Vitest, build, audit | PASS; Vitest 11/11 and audit 0 vulnerabilities. |
| Development, mock-Provider, and production Compose validation | PASS. |
| `scripts/verify-commercial-integration.ps1` | PASS; 14/14 suites, schema 24, one local server image digest. |
| `scripts/verify-exact-money.ps1` | PASS. |
| `tests/release/verify-commercial-readiness.ps1` | PASS; 9 negative scenarios. |
| Workflow YAML parse and Actionlint 1.7.12 | PASS for CI, Release, and Nightly Soak workflows. |
| Gitleaks 8.30.1 directory and Git-history scans | PASS; about 437.85 MB working tree and 4 commits scanned, no leaks found. |
| Syft 1.50.0 SBOM generation | PASS for Server, Admin, and Console; all three SPDX JSON files parsed successfully. |
| Container non-root inspection | PASS: Server `10001:10001`; Admin/Console `101:101`. |
| Trivy 0.73.0 HIGH/CRITICAL scan | **NOT RUN**; vulnerability database unavailable after two TLS handshake timeouts. |
| `govulncheck ./...` | **NOT RUN**; vulnerability data retrieval timed out and no usable local database was available. |
| `gosec ./...` | **NOT RUN**; installation/module verification timed out and no cached binary was available. |

The local commercial evidence JSON intentionally remains outside the release
manifest. A future release run must create evidence on the exact clean commit,
with the latest migration and immutable image digests.

## Required closure before commercial re-review

1. Obtain owner/counsel approval for licensing, legal entity, terms, privacy,
   refunds, data processing, tax, and invoice policy.
2. Execute a production payment agreement and validate the production adapter,
   settlement file, webhook replay, refunds, chargebacks, and fee reconciliation.
3. Approve at least one Provider's commercial distribution rights, regions,
   processing terms, contract window, and kill-switch operations.
4. Complete production SMTP, managed PostgreSQL PITR, Redis/database/application
   failover, multi-zone RPO/RTO, IAM/KMS/WAF/DNS, and logging/privacy drills.
5. Obtain and disposition an independent penetration test; refresh current
   vulnerability data and scan exact immutable release images.
6. For Marketplace production, complete real supplier KYB/contract, endpoint
   verification, canary, tax/invoice, payout institution, bill reconciliation,
   dispute/refund allocation, second-admin approval, suspension/cutover, and
   exit evidence.
7. Commit the reviewed engineering changes and rerun the protected CI/Release
   workflows. Any stale migration, other-commit report, sandbox adapter, expired
   evidence, or unexplained financial difference must fail closed.

Until every applicable gate is genuinely approved, the final decision remains
`NO-GO`.
