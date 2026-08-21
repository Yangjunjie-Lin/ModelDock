# Commercial beta compliance review checklist

**Decision: NO-GO**  
**Review date:** 2026-08-17  
**Nature of review:** engineering evidence review, not legal advice

All public legal source files explicitly state `PENDING COUNSEL REVIEW`. No
engineering test or administrator-entered synthetic `APPROVED` fixture can
replace approval by qualified counsel and authorized company representatives.

## Corporate, distribution, and contracting

| Control | Status | Evidence / required closure |
| --- | --- | --- |
| Software licensing decision | **BLOCKED** | `docs/licensing-decision.md` is `blocked / undecided`; approve the exact distribution model and required license/terms artifacts. |
| Company legal identity and authority | **BLOCKED** | Final legal name, registered address/number, authorized contracting entity, and legal contact are not evidenced. |
| Third-party notices and license compatibility | **BLOCKED** | Build dependencies were vulnerability-scanned, but a counsel-approved Go/npm/container license inventory and notices package is not evidenced. |
| Contribution ownership/CLA/DCO policy | **BLOCKED** | Depends on the selected licensing model and must be recorded before distribution. |
| Provider resale/API rights | **BLOCKED** | All standard Providers are pending/not approved. See [provider-commercial-matrix.md](provider-commercial-matrix.md). |
| Payment-provider agreement | **BLOCKED** | Only the local signed sandbox adapter was tested; no intended acquiring entity, merchant account, settlement terms, or chargeback process is evidenced. |

## Public terms and consumer commerce

| Control | Status | Evidence / required closure |
| --- | --- | --- |
| Service terms | **BLOCKED** | Draft remains pending counsel review; finalize governing law, dispute venue, service scope, suspension, liability, and language priority. |
| Acceptable use policy | **BLOCKED** | Draft remains pending review; align prohibited use and enforcement with every admitted Provider contract. |
| Pricing disclosure | ENGINEERING PASS / LEGAL BLOCKED | Public endpoints fail closed before terms/fees are published and expose exact string decimals, region, tax/fee/refund/bonus disclosures in the synthetic run. Real terms need approval. |
| Refund/cancellation policy | ENGINEERING PASS / LEGAL BLOCKED | Exact sandbox refund and replay passed; refund windows, consumer rights, non-refundable items, payment fees, and human review rules remain draft. |
| Tax and invoicing | **BLOCKED** | Applicable VAT/GST/sales tax, invoice issuer/content, tax inclusion, exemptions, and jurisdiction rules are not evidenced. |
| Promotions/bonus credits | ENGINEERING PASS / LEGAL BLOCKED | Promotion separation and non-refundable disclosure were tested; final campaign terms and accounting/tax treatment need approval. |
| Consumer support/complaints | **BLOCKED** | Test used `.invalid` contact addresses; real response channels, escalation SLA, legal service, and complaint authority details are not approved. |
| China filing/licensing applicability | **BLOCKED** | ICP filing/license, public-security filing, algorithm/model filing, telecom and generative-AI obligations must be determined; no number may be fabricated. |
| Minors and age eligibility | **BLOCKED** | Applicable age gate, parental consent, prohibited audience, and deletion process require a final policy. |

## Privacy and data governance

| Control | Status | Evidence / required closure |
| --- | --- | --- |
| Controller/processor roles and lawful bases | **BLOCKED** | Draft privacy/DPA materials do not identify final entities, roles, lawful bases, or customer instructions. |
| Data inventory and purpose limitation | PARTIAL | Request, account, payment, usage, Provider, and audit evidence is modeled; approve the complete production data-flow/ROPA inventory. |
| Cross-border transfer and residency | **BLOCKED** | Provider processing regions are empty and cross-border legal mechanism/impact assessment is not approved. Routing must stay fail-closed. |
| Subprocessors | **BLOCKED** | Final Provider, cloud, email, payment, observability, support, and backup subprocessors plus change-notice terms are not evidenced. |
| Retention and deletion | ENGINEERING PASS / LEGAL BLOCKED | Synthetic retention redaction, deletion pseudonymization, and retained audit evidence passed; approve production schedules, exceptions, legal hold, and backup deletion. |
| Data-subject/customer requests | PARTIAL | Audited lifecycle jobs exist; identity verification, statutory deadlines, export format, appeals, and operator runbook need jurisdictional approval. |
| Security incident/breach notification | PARTIAL | Observability/audit controls exist; final regulator/customer/Provider timelines, decision authority, and contacts are not evidenced. |
| Training/content use disclosure | **BLOCKED** | Must reflect each Provider's actual contract and model behavior before its admission. |

## Security, financial, and operational controls

| Control | Status | Evidence / required closure |
| --- | --- | --- |
| Authentication, CSRF, tenant scope, key revocation | ENGINEERING PASS | Registration/verification, session/CSRF paths, cross-org denial, project key scope, and immediate suspended-user invalidation passed in E2E. Independent penetration testing remains outstanding. |
| Secrets and privacy in repository/logs | ENGINEERING PASS | Gitleaks found no leaks; public/aggregate E2E responses excluded API keys, test secrets, raw email, and anonymous identifier. |
| Exact money, idempotency, audit, concurrency | **BLOCKED overall** | Commercial ledger path passed; legacy `float64`/`::float8` monetary paths violate the release rule and require migration. |
| Financial reconciliation | ENGINEERING PASS in sandbox | Four-way daily close produced zero differences; monthly statement and refund replay passed. Real settlement evidence is absent. |
| Backup/recovery | PARTIAL | Isolated logical restore passed. Managed PITR, object storage, KMS access, production RPO/RTO, and regional disaster exercise remain. |
| Provider governance | ENGINEERING PASS / CONTRACT BLOCKED | Contract/region/kill-switch fail-closed logic passed. No standard Provider has approval evidence. |
| Vulnerability/secret scanning | ENGINEERING PASS with caveat | npm audit, Gitleaks, and still-current cached Trivy database passed. Repeat with online current databases on the immutable release commit. |
| Release/version provenance | **BLOCKED** | Requested prerelease, source version, and stable-only verifier disagree. No Release was created. |

## Required approvals

A new go-live review needs recorded approval from:

- repository owner for licensing and release identity;
- qualified counsel for corporate, product terms, privacy/DPA, consumer,
  Provider, payment, cross-border, filing, export/sanctions, and IP matters;
- finance/tax owner for wallet/ledger accounting, settlement, refund, promotion,
  tax, invoicing, and reconciliation;
- security/privacy owners for threat model, penetration-test disposition,
  incident response, retention/deletion, subprocessors, and backup controls;
- Provider commercial owners for every individually admitted Provider; and
- operations owner for capacity, SLOs, monitoring, on-call, backup/PITR,
  failover, rollback, and immutable artifact provenance.

Until those approvals and all blocker evidence are present, public legal pages
must continue to display the counsel-review warning and commercial production
routing must remain disabled.
