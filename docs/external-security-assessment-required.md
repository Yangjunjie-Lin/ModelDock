# External security and production assessment required

Repository tests are regression evidence, not an independent assessment. The
following gates remain **BLOCKED** until an authorized external reviewer or the
responsible production owner records a controlled evidence reference, SHA-256,
approver, approval/expiry time, and exact reviewed commit in
`release/commercial-gates.json`.

| Required assessment | Minimum evidence | Current state |
| --- | --- | --- |
| Independent penetration test | Scope, tested commit/image digests, findings and disposition | BLOCKED |
| Cloud IAM review | Human/workload roles, break-glass, least privilege and audit exports | BLOCKED |
| TLS, WAF and DNS review | Public endpoints, certificate lifecycle, WAF policy and DNS ownership | BLOCKED |
| KMS and Secret Manager review | Key custody, rotation, recovery, access logs and separation of duties | BLOCKED |
| Managed PostgreSQL PITR | Encrypted backup/WAL policy plus timed restore to an isolated environment | BLOCKED |
| Redis failover | Promotion/fencing behavior, data-loss analysis and recovery evidence | BLOCKED |
| Multi-AZ service failover | Load balancer, instance/zone loss, in-flight settlement and readiness evidence | BLOCKED |
| RPO/RTO exercise | Approved targets, observed timings, gaps and remediation owners | BLOCKED |
| Production logging/privacy review | Prompt/response/header/secret sampling, retention and deletion validation | BLOCKED |

The external assessment must include cross-organization BOLA/IDOR, admin
authorization, session fixation, CSRF, JWT rotation, API-key enumeration/leak
freeze, webhook replay, duplicate payment credit, wallet double-spend, funding
replay, SSRF/DNS rebinding/redirects, malicious headers, oversized JSON/SSE,
slow upstreams, SQL injection, CSV formula injection, log injection, payout
destination redaction, and audit-log tamper resistance. Existing automated tests
may be cited as supporting evidence, but cannot self-approve this gate.

Never store contracts, identity documents, payment credentials, personal data,
or raw penetration-test exploit material in this repository. Store only the
internal controlled reference and cryptographic digest required by the gate.
