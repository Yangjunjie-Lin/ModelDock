# Current commercial-readiness revalidation

**Candidate version:** `3.0.0-beta.1`

**Latest migration:** `0025_commercial_attestation_and_decimal_hardening`

**Engineering implementation:** **CANDIDATE — protected CI required**

**Commercial decision:** **NO-GO**

**Marketplace decision:** **NO-GO**

This is a tracked status snapshot, not exact-commit Release Evidence. Git cannot
embed a commit's final SHA inside a tracked file that contributes to that same
commit. Protected CI therefore generates the authoritative reports after a
clean checkout and records Commit SHA, Tree SHA, Workflow Run ID, repository,
branch/tag, VERSION, Migration, all three image digests, and start/end times in
the uploaded artifact. README and tracked reports are regenerated and checked
for hand edits.

## 18-prompt status

| Prompt | Latest status | Engineering evidence | Remaining real blocker |
| ---: | --- | --- | --- |
| 1 | PASS (engineering) | Protected CI definition includes Go, web, dependency, secret, Compose, image, workflow and commercial jobs | The new PR's Required Checks must complete green. |
| 2 | PASS (engineering) | Account lifecycle and isolation suites remain in full commercial integration | None beyond exact-commit CI evidence. |
| 3 | PASS (engineering) / BLOCKED (production) | MFA, invitation, verification/reset, secret and dependency controls | Signed production SMTP/runtime evidence. |
| 4 | PASS (engineering) | Strict NUMERIC(30,12) `ParseDecimal`; error-return Add/Subtract/Multiply/Compare; scan and settlement fail-closed tests | None beyond exact-commit CI evidence. |
| 5 | PASS (engineering) | Wallet, journal, funding, replay and reconciliation suites | Real channel statements remain external evidence. |
| 6 | PARTIAL / BLOCKED | Sandbox and manual payment behavior is tested | Finance-signed acquirer agreement plus signed production Runtime Attestation. |
| 7 | PARTIAL / BLOCKED | Subscription, refunds, invoice application and exact amount paths | Legal/Finance refund, tax and invoice approval. |
| 8 | PARTIAL / BLOCKED | Synthetic financial close and zero-difference reconciliation | Real acquirer/Provider/supplier settlement evidence. |
| 9 | PARTIAL / BLOCKED | Provider admission requires commercial status, resale, contract window, regions, current price and kill switch off | Commercial + Legal signed Provider approval and runtime provider list. |
| 10 | PARTIAL / BLOCKED | Regional/data-processing governance is fail-closed | Contract-backed Legal/Commercial Attestations. |
| 11 | PARTIAL / BLOCKED | Metrics, tracing, alerts, support and status controls exist | Signed production operations/runtime evidence. |
| 12 | PARTIAL / BLOCKED | Local Compose, backup/restore and failure tests | Managed PITR, multi-zone failover and measured RPO/RTO Attestations. |
| 13 | PASS (engineering) / BLOCKED (production) | Public onboarding, complaint/content/data controls | Approved legal text and verified production SMTP. |
| 14 | PASS (engineering) / BLOCKED (commercial) | Evidence Chain V2, out-of-repository trust anchor, pure-output decision and same-digest release workflow | All applicable signed external/runtime Attestations and green exact-commit scans. |
| 15 | PASS (engineering) / BLOCKED (production) | Supplier onboarding, authority separation and default-deny states | Real KYB, contract, tax/invoice, payout and security approval. |
| 16 | PASS (engineering) / BLOCKED (production) | Quality probes, canary, circuit and kill-switch controls | Contracted real-provider probe/canary evidence. |
| 17 | PASS (engineering) / BLOCKED (production) | Exact accrual, reserve, bills, dispute, batches and replay controls | Production payout institution and real reconciliation. |
| 18 | PASS (engineering) / BLOCKED (production) | Launch, second-admin, suspension/cutover/exit and 44 fail-closed negative scenarios | Complete Marketplace Gate set and real accountable sign-off. |

## Evidence Chain V2

`release/commercial-gates.json` is now only the required immutable Gate catalog.
Signed Attestations live in a GitHub Actions Artifact Bundle, not the source
Commit. The catalog no longer self-declares payment/payout/SMTP
mode or Provider/Supplier counts. Draft 2020-12 validation rejects missing,
duplicate, renamed or unknown Gates, unknown fields, wrong profiles, invalid
times and malformed SHAs. The verifier then checks evidence existence/hash,
Ed25519 signature, issuer and role allowlists, validity window, exact source
identity and target-runtime predicates. The production issuer list is empty and
its expected SHA must be anchored outside Git, so a repository edit alone
cannot approve external work.

The tracked Go-live contract is never read as an input. Any `BLOCKED` or `NOT
RUN` result computes `NO-GO`. See
[external-commercial-closure.md](external-commercial-closure.md) for the real
owner/evidence hand-off.
