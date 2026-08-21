# Current commercial-readiness revalidation

**Baseline commit:** `9ef82a669b704028f1919e094b0bd82a11dba866`  
**Candidate version:** `3.0.0-beta.1`  
**Latest migration:** `0024_exact_money_and_release_evidence`  
**Commercial decision:** **NO-GO**

This record supersedes the Migration-19 scope of the 2026-08-17 review. It
separates implemented controls from external commercial evidence. The generated
source of truth is [generated/commercial-readiness-report.md](generated/commercial-readiness-report.md).
No Tag, GitHub Release, production image promotion, or deployment is authorized
by this document.

| Prompt | Status | Current code/database evidence | Reverification and external evidence |
| ---: | --- | --- | --- |
| 1 | PASS | CI baseline, repository policy files, pinned Actions in `.github/workflows/ci.yml` | Local Go and frontend checks rerun; hosted GitHub result still requires the exact committed revision. |
| 2 | PASS | `internal/auth`, account lifecycle stores/handlers, account integration suite | Synthetic account suite remains mandatory in `commercial-integration`. |
| 3 | PASS | TOTP MFA, invitations, verification/reset flows, secret/dependency/image gates | Production email delivery remains externally blocked. |
| 4 | PASS (engineering) | Migration 0024; `domain.Decimal`; exact model prices, budgets, request logs, aggregates, API and CSV | `scripts/verify-exact-money.ps1` passes; retained floats are explicit telemetry/routing allowlist entries. |
| 5 | PASS (engineering) | Immutable wallet/journal/funding and exact pricing snapshots | Existing concurrency, reversal, replay, and reconciliation suites remain mandatory. |
| 6 | PARTIAL | Recharge orders, signed webhook verification, sandbox/manual adapters | No production acquirer agreement or adapter; external gate BLOCKED. |
| 7 | PARTIAL | Subscription versions, charges, refunds, invoice applications | Legal refund, tax, and invoice approval BLOCKED. |
| 8 | PARTIAL | Financial close and multi-source reconciliation | Synthetic zero-difference evidence is not a real channel settlement file. |
| 9 | PARTIAL | Provider commercial admission is fail-closed by contract, region, resale, pricing, and kill switch | Approved Provider count is zero; gate BLOCKED. |
| 10 | PARTIAL | Provider governance, regional/data-processing controls, audit evidence | Contract and data-processing approval absent. |
| 11 | PARTIAL | Prometheus, OTLP, alerts, SLOs, support/status functions | Production observability ownership and environment evidence absent. |
| 12 | PARTIAL | Compose, Kubernetes examples, canary and backup/restore scripts | Managed PITR, multi-zone failover, KMS/IAM/WAF review absent. |
| 13 | PASS (engineering) | Public site, pricing/docs pages, onboarding, complaint/content/data controls | Public legal text and production SMTP remain BLOCKED. |
| 14 | BLOCKED | Release workflow now consumes exact-commit tests and commercial evidence before image construction | License, legal, Provider, payment, security, and production gates remain BLOCKED; `NO-GO`. |
| 15 | PASS (engineering) | Migration 0020 supplier onboarding, KYB states, endpoint ownership and four-eyes controls | No real supplier KYB/contract evidence. |
| 16 | PASS (engineering) | Migration 0021 quality probes, canary, routing multiplier and circuit controls | No contracted real-supplier probe/canary evidence. |
| 17 | PASS (engineering) | Migration 0022 payable, reserve, commission, bills, disputes, batches and payout idempotency | Sandbox payout is permanently test-only; tax and bank/payout evidence absent. |
| 18 | PASS (engineering) / BLOCKED (production) | Migrations 0023–0024 launch gate, exact money, suspension/cutover/exit and negative release-gate tests | Marketplace production requires all supplier and commercial evidence; current decision `NO-GO`. |

## Migration 20–24 coverage

- 0020: supplier onboarding and separation of applicant/approver authority.
- 0021: platform quality evidence, probes, canary and fail-closed routing.
- 0022: exact supplier accrual, reserve, bill, dispute, settlement and payout
  replay controls.
- 0023: Marketplace launch acceptance, second approval, emergency cutover and
  lifecycle evidence.
- 0024: legacy exact-money companions, rollback-compatible synchronization,
  deterministic rounding and zero-difference reconciliation view.

The CI `commercial-integration` job runs every migration, account, pricing,
funding, payment, subscription, close, onboarding, supplier-settlement,
Marketplace-launch, release-metadata, exact-money, and release-gate suite. Its
artifact binds status to the commit SHA, latest migration and local image
digest. A report for Migration 19 or another commit cannot satisfy the gate.

On 2026-08-21 the local orchestrator completed 14/14 suites, including populated
V21/V22 upgrades through Migration 24. This run used the uncommitted working
tree, so it is engineering regression evidence only and is deliberately not
installed as the manifest's exact-commit release evidence. Protected CI must
repeat it after the reviewed changes are committed.

## Remaining risks

The external items in `release/commercial-gates.yaml` are intentionally
`BLOCKED`. A coding agent must not change them to `APPROVED`. The repository is
an engineering preview until authorized humans attach controlled evidence
references/hashes and rerun the workflow on the exact release commit.

Current vulnerability scanning is also blocked: Gitleaks and SBOM generation
passed, but Trivy could not download a vulnerability database, while
`govulncheck` and `gosec` had no usable local tool/data after TLS timeouts.
