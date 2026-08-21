# Security release report

**Decision: NO-GO**  
**Review date:** 2026-08-17  
**Candidate:** requested `v0.9.0-beta`; no Release/tag created

The tested security controls behaved correctly in the isolated environment,
but legal/Provider approval, release identity, legacy exact-money migration,
and production-environment assurance are incomplete. Security scan success does
not override those mandatory blockers.

## Tested controls

| Area | Result | Evidence |
| --- | --- | --- |
| Registration and verification | PASS | Synthetic user registered; token was read from protected local mail capture and accepted once. |
| Session and request integrity | PASS | Admin/Console authentication and CSRF-protected mutations completed; secrets were not returned in aggregate/public payloads. |
| Tenant isolation | PASS in tested path | Console user read of a different organization returned `404`; project route/key scope remained restricted. |
| API key compatibility and revocation | PASS | `rdk_test_*` key accessed `/v1`; suspending its user made `/v1/models` return `401` immediately. |
| Provider governance | PASS in synthetic path | Contract/region admission, fallback, and kill switch worked; after disable, request returned `403` and Provider attempt count did not increase. |
| Request idempotency | PASS | Replayed gateway idempotency key retained one funding operation and returned conflict. |
| Payment/refund replay | PASS in sandbox | Signed webhook retained one event and one top-up; refund replay retained one successful refund row. |
| Financial concurrency/integrity | PASS in exact commercial path | Concurrent wallet/request suites, terminal reserve equations, immutable journals, price snapshots, and zero-difference reconciliation passed. |
| Client interruption | PASS | Cancellation during upstream streaming was classified as `client_disconnected` and reached terminal accounting. |
| Data minimization and deletion | PASS in synthetic path | Raw anonymous ID was not persisted; retention redacted old content; deletion pseudonymized user data while retaining audit evidence. |
| Backup restoration | PASS for isolated logical backup | Restored schema version, funding, posted journal, and audit counts matched live synthetic database. |
| Application/database/Redis failure handling | PASS in local Docker injection | Readiness, recovery, terminal funding, and replacement-instance request assertions passed. |

## Secret and dependency scanning

### Repository secret scan

Gitleaks 8.30.1 scanned 3 commits and approximately 1.52 MB. Result: no leaks
found. The E2E fixtures use only synthetic values, `.invalid` addresses, and
`rdk_test_*` material generated inside disposable environments. No real API
key, payment secret, or personal data was added to code, logs, examples, or
reports.

### Frontend dependency audit

`npm ci` and `npm audit` completed in both frontend workspaces with 0 reported
vulnerabilities. Lint, TypeScript checks, and builds passed. Console Vitest
reported 8/8 passing tests. Admin build emitted a performance warning for one
514.87 kB JavaScript chunk; it was not a security finding.

### Container vulnerability scan

Final local images were built from the current source:

| Image | OS/application targets | HIGH/CRITICAL fixed findings |
| --- | --- | ---: |
| `modeldock/go-live-server:local` | Alpine 3.22.5 and Go binary | 0 |
| `modeldock/go-live-admin:local` | Alpine 3.24.1 | 0 |
| `modeldock/go-live-console:local` | Alpine 3.24.1 | 0 |

The successful command used Trivy 0.73.0 with vulnerability scanners,
`--severity HIGH,CRITICAL`, `--ignore-unfixed`, and `--exit-code 1`. Its cached
database metadata was:

```text
UpdatedAt:   2026-08-15T18:50:52.961407859Z
DownloadedAt: 2026-08-15T21:02:17.775498127Z
NextUpdate:  2026-08-16T18:50:52.961407658Z
```

The scans ran at approximately 2026-08-16 16:16 UTC, before `NextUpdate`.
Before using the cache, three online scan attempts tried to refresh the database
and failed because `https://mirror.gcr.io/v2/` refused the TCP connection. That
failure is recorded rather than represented as a clean online scan. Repeat with
a newly downloaded database on the exact immutable release images.

## Security-sensitive implementation observations

- `/v1` OpenAI-compatible shapes and existing `rdk_*`/`RELAYDOCK_*`
  compatibility remain intact.
- Provider admission is checked before scheduling and again when the attempt is
  recorded. Contract state, resale approval, region, data-processing region,
  budget/margin, technical state, and emergency kill switch fail closed.
- Official Provider price ingestion is constrained to configured HTTPS hosts,
  public pinned addresses, bounded time/size, and same-host redirects. It does
  not accept caller authorization headers or return upstream bodies.
- BYOK credentials are encrypted, organization-bound, redacted in lists, and
  never pooled across organizations. Customer credential settlement separates
  Provider cost from the explicit platform fee.
- Finance reconciliation failures are emitted as structured event/date/error
  fields. The E2E diagnostic additionally redacts database/Redis URLs and
  `rdk_*` keys before surfacing container logs; log-sink redaction still needs
  production configuration review.
- Audit and price/usage/funding evidence is append-only where required by the
  commercial path; corrections require new evidence rather than in-place
  changes.
- The E2E run deliberately set Provider HTTP/private-network allowances so the
  mock could run in an isolated Docker network. Those are test-only relaxations
  and are prohibited in production configuration.

## Open security and assurance risks

1. **No independent penetration test.** The tested cross-organization denial is
   strong regression evidence, not exhaustive BOLA/IDOR, privilege escalation,
   session fixation, SSRF, injection, or business-logic assessment.
2. **No production attack-surface evidence.** TLS termination, DNS, WAF/rate
   limits, cloud IAM/security groups, KMS, managed database/Redis policies,
   observability access, and administrator network controls were not inspected
   in the deployed target environment.
3. **Provider/payment contracts are absent.** Security, incident, data-use,
   residency, key custody, and webhook expectations cannot be considered final
   without the selected partners' agreements.
4. **Legal privacy status is incomplete.** Controller/processor roles,
   cross-border mechanism, retention schedule, subprocessors, breach notices,
   and user rights remain pending counsel review.
5. **Legacy financial floats remain.** This is primarily financial correctness,
   but rounding inconsistencies can become an abuse and authorization-boundary
   issue for limits/budgets; it blocks release.
6. **Production recovery is unproven.** Local container pause/kill and logical
   restore do not establish multi-AZ failover, KMS recovery, WAL/PITR, or RPO/RTO.
7. **Vulnerability registry availability failed.** The cached DB was current at
   scan time, but release automation needs reliable online database/artifact
   access and should fail closed when it cannot obtain required evidence.
8. **Release provenance mismatch.** Runtime `2.0.0`, requested
   `0.9.0-beta`, and a stable-only release verifier cannot yield an unambiguous,
   signed artifact provenance chain.

## Required security sign-off evidence

Before a new release decision:

- close every item in [compliance-review-checklist.md](compliance-review-checklist.md)
  and [provider-commercial-matrix.md](provider-commercial-matrix.md);
- complete the exact-money compatibility migration and rerun concurrency and
  reconciliation tests;
- obtain and disposition an independent penetration test covering all Admin,
  Console, public, payment webhook, and `/v1` tenant/resource boundaries;
- validate production TLS/IAM/network/KMS/secrets/rate-limit/log-retention and
  incident-response configuration without recording secret values;
- run current secret, dependency, license, SBOM, provenance/signature, and
  HIGH/CRITICAL image scans on the exact commit/digests;
- execute production-like payment replay, Provider kill switch, backup/PITR,
  database/Redis/app failover, and rollback drills; and
- require security, privacy, finance, operations, legal, and repository-owner
  approval in the protected release workflow.

Any cross-organization bypass, duplicate financial posting, unexplained ledger
difference, unapproved Provider attempt, negative-margin admission, or
unrestorable backup is an automatic `NO-GO`.
