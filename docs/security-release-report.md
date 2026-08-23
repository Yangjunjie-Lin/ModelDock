# Security release report

**Decision: NO-GO**

**Candidate:** `3.0.0-beta.1`

**Latest migration:** `0024_exact_money_and_release_evidence`

**Release/Tag/production promotion created:** no

This tracked document records engineering observations and stop conditions; it
is not exact-commit release evidence. Protected CI must regenerate the signed
machine report against a clean checkout and the immutable Server/Admin/Console
candidate digests.

## Current engineering observations

| Check | Local observation on 2026-08-23 | Formal release status |
| --- | --- | --- |
| `gofmt` | PASS | Must repeat on PR HEAD |
| `go vet ./...` | PASS | Must repeat on PR HEAD |
| `go test -count=1 -timeout=300s ./...` | PASS | Must repeat on PR HEAD |
| `govulncheck ./...` v1.6.0 | PASS; 0 reachable vulnerabilities | Must repeat with current vulnerability data on PR HEAD |
| `gosec -exclude-generated` v2.22.10 | PASS; 0 issues after explicit source scan | Must repeat on PR HEAD |
| Admin locked install/lint/typecheck/build/audit | PASS; no test script; audit 0 vulnerabilities | Must repeat on PR HEAD |
| Console locked install/lint/typecheck/test/build/audit | PASS; 11/11 tests; audit 0 vulnerabilities | Must repeat on PR HEAD |
| Evidence validator locked install/audit | PASS; audit 0 vulnerabilities | Must repeat on PR HEAD |
| Gitleaks 8.30.1 directory and Git history | PASS; no leaks in working tree or 25 commits | Must repeat with protected PR history |
| Development/Mock/Production Compose configuration | PASS | Runtime deployment remains unproven |
| Actionlint 1.7.7 and workflow permission audit | PASS | Must repeat on PR HEAD |
| Docker candidate build, non-root, SBOM, Provenance, Trivy HIGH/CRITICAL | Not release evidence until the clean committed candidate run | **BLOCKED** |
| Independent penetration test and production surface assessment | No evidence supplied | **BLOCKED** |

Passing engineering rows do not approve a license, legal entity/text, payment
or payout provider, Provider resale/data-processing rights, supplier KYB,
production SMTP/PITR/failover, tax, or independent security assessment.

## Evidence-chain security controls

- Draft 2020-12 schemas reject unknown properties, invalid profiles/times/SHAs,
  missing Runtime, wrong schema versions and a modified mandatory Gate set.
- Evidence Attestation V2 verifies the controlled evidence file SHA-256,
  Ed25519 signature, exact Gate/profile/repository/Commit/Tree/Version/Migration,
  workflow run, issuer allowlist, required role and validity window.
- The production issuer policy is empty and its SHA-256 must be anchored in a
  protected out-of-repository variable. Editing YAML cannot create trust.
- Runtime claims are signed target-environment output. Sandbox/manual payment,
  sandbox payout, count-only claims, invalid Provider contract windows and
  incomplete supplier predicates fail closed.
- Commercial evidence binds Gateway tests, Admin/Console builds, scanners,
  SBOM, Provenance and candidate tags to the same three immutable digests.
- The Go-live report is output-only. Any BLOCKED/NOT RUN item computes NO-GO.

## Remaining security blockers

1. No independent tester and Security-signed penetration-test disposition.
2. No signed production TLS/DNS/WAF/IAM/KMS/network/logging/privacy review.
3. No signed managed PITR, failover, key recovery or measured RPO/RTO drill.
4. No production Runtime Attestation for payment, payout, SMTP, Providers,
   suppliers, database migration/query summary and candidate image digests.
5. No clean protected PR/Release run has yet supplied the final Trivy, SBOM,
   Provenance and exact-commit artifacts for this change.

Any tenant escape, secret disclosure, unauthorized approval, invalid Decimal,
duplicate posting, wallet double-spend, unexplained reconciliation difference,
unapproved Provider/supplier traffic, payout replay, digest mismatch, stale
source identity, failed scan or failed restore is an automatic `NO-GO`.
