# Security release report

**Decision: NO-GO**
**Review date:** 2026-08-21 (Asia/Shanghai)
**Candidate:** `3.0.0-beta.1`; no Tag, Release, or deployment created
**Scope:** current uncommitted `fix/commercial-readiness-closure` working tree

The isolated security and financial regression controls passed, but no
independent assessment, production-environment review, or current vulnerability
database was available. A local working-tree test is not exact-release-commit
evidence. These limitations remain machine-blocking in
`release/commercial-gates.yaml`.

## Tested controls

| Area | Result | Evidence |
| --- | --- | --- |
| Registration, verification, MFA, reset, and invitation | PASS in isolated tests | Account integration suite exercised lifecycle, single-use tokens, mandatory Admin TOTP enrollment, and revocation. |
| Session/request integrity | PASS in tested paths | CSRF-protected mutations, session invalidation, and public/aggregate redaction assertions passed. |
| Tenant isolation and administrator authority | PASS in tested paths | Cross-organization reads were denied; supplier self-approval and same-admin four-eyes approval were rejected. |
| API key compatibility/revocation | PASS | `rdk_test_*` and `/v1` compatibility remain; disabling the owning user/provider stopped new access. |
| Provider and supplier admission | PASS in synthetic paths | Contract/region/quality/canary/kill-switch and payout-readiness gates fail closed. No real Provider or supplier approval exists. |
| Payment, refund, request, and payout replay | PASS in sandbox | Replays retained one durable financial effect and stable idempotency evidence. No production channel was tested. |
| Wallet double-spend and ledger integrity | PASS | 100 concurrent reservations, crash recovery, reversals, refund allocation, payable settlement, and zero-difference reconciliation passed. |
| SSRF and upstream boundary controls | PASS in repository tests | Endpoint ownership, public/HTTPS host constraints, redirect/header restrictions, Provider fault handling, and bounded upstream behavior were exercised. |
| Input/output and data handling | PASS in tested paths | Oversized/error paths, CSV formula protection, structured redacted diagnostics, retention cleanup, and deletion pseudonymization have regression coverage. |
| Local recovery behavior | PASS with scope limit | Redis/PostgreSQL interruption, app/worker restart, logical backup/restore, and emergency cutover passed locally; managed production recovery is unproven. |
| Audit and evidence preservation | PASS in tested paths | Append-only financial/Marketplace evidence and historical retention after supplier suspension/exit were verified. |

Passing rows are regression evidence, not an independent penetration test.

## Secret, dependency, and supply-chain results

### Repository secrets

Gitleaks 8.30.1 ran in both directory and Git-history modes with redaction:

- working tree: approximately 437.85 MB scanned, no leaks found;
- history: 4 commits / approximately 4.91 MB scanned, no leaks found.

Test credentials use synthetic values and `.invalid` addresses. No real API
key, payment secret, contract content, personal document, or production log was
added by this work.

### Dependencies and static checks

| Check | Result |
| --- | --- |
| `go vet ./...` | PASS |
| Full Go tests | PASS |
| Admin and Console `npm audit --audit-level=high` | PASS; 0 reported vulnerabilities |
| Exact-money static gate | PASS |
| `govulncheck ./...` | **NOT RUN**: vulnerability service TLS handshake timeout; no usable local vulnerability database |
| `gosec ./...` | **NOT RUN**: module/tool retrieval TLS handshake timeout; no cached executable |

The unavailable checks remain unresolved; they are not represented as clean.

### Images and SBOM

The local images used for validation were inspected as non-root:

| Image | Configured user | SBOM |
| --- | --- | --- |
| `relaydock/server:local` | `10001:10001` | SPDX JSON generated and parsed; 60 packages |
| `relaydock/admin-web:local` | `101:101` | SPDX JSON generated and parsed; 72 packages |
| `relaydock/console-web:local` | `101:101` | SPDX JSON generated and parsed; 72 packages |

Syft 1.50.0 generated all three SBOMs. Its optional update check timed out, but
SBOM generation and JSON validation completed successfully.

Trivy 0.73.0 could not perform a vulnerability scan. The local Trivy volume did
not contain a vulnerability database. Downloads from both the default
`mirror.gcr.io/aquasec/trivy-db:2` source and
`ghcr.io/aquasecurity/trivy-db:2` failed with TLS handshake timeouts. Therefore
the result is **NOT RUN / BLOCKED**, not zero findings.

## Open security and assurance risks

1. No independent penetration test covers the complete public, Console, Admin,
   payment webhook, payout, supplier, or `/v1` attack surface.
2. Production TLS, DNS, WAF, cloud IAM/network policy, KMS/Secret Manager,
   managed PostgreSQL/Redis, observability access, and administrator network
   controls were not inspected.
3. Production log sampling/privacy review and prompt/response leakage review
   are absent.
4. Provider/payment/payout/supplier contracts and incident obligations are
   absent, so security and data-handling terms are not approved.
5. Managed PITR, multi-zone failover, Redis promotion, backup-key recovery, and
   measured production RPO/RTO are unproven.
6. Current Go vulnerability, SAST, and image vulnerability evidence is missing
   because the required tools/data could not be obtained in this environment.
7. Tests ran on an uncommitted working tree. Protected CI must reproduce them on
   the exact reviewed commit and immutable image digests.

The external work and acceptable evidence fields are listed in
[external-security-assessment-required.md](external-security-assessment-required.md).
Until those controlled references, hashes, approvers, expiry dates, and reviewed
commit are present, the security release gate remains `BLOCKED`.

## Automatic stop conditions

Any cross-organization access, unauthorized approval, secret disclosure,
duplicate financial posting, wallet double-spend, unexplained ledger
difference, unapproved Provider/supplier attempt, payout replay with a new
idempotency key, negative-margin admission, audit-history mutation, or failed
restore is an automatic `NO-GO` and must not be manually converted to success.
