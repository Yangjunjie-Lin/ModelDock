# Public operations governance

This step adds user and organization risk state, registration/login/key and
recharge controls, content-policy hooks, reports, manual review, and privacy
lifecycle jobs. It is an operational control layer, not a legal determination.

Risk state is monotonic within a transaction for each event and is attributed
to an idempotency key. `FROZEN` or `BLOCKED` states deny gateway access and key
creation; `RESTRICTED` states deny high-risk operations without disabling other
organizations. A suspected leaked key is disabled with all versions revoked,
and the audit record excludes key material. Gateway bodies are checked in
memory for complete `rdk_*` key shapes; only a database HMAC match triggers an
automatic freeze, and candidate material is never logged or persisted.

Content policy phases are pre-request, provider-native, and post-response. The
provider interface is pluggable. `RELAYDOCK_CONTENT_POLICY_FAILURE_MODE`
controls provider failure behavior and defaults to fail-closed. Manual-review
decisions are durable queue records with due times. `REDACT` is held for review
unless an external provider returns a replacement body; post-response checks run
after bytes have been sent and therefore flag/freeze rather than recall bytes.
The failure mode and this streaming boundary must be part of the legal and
security review for each deployment.

Privacy settings default to not saving content. Lifecycle jobs are idempotent
and retain evidence/audit metadata. Legal hold must be set before a destructive
job; cleanup workers should delete eligible content while retaining required
audit records. Operators must set retention and cross-border routing only after
legal review.

IP and device anomaly signals are HMAC-SHA256 digests keyed by
`RELAYDOCK_API_KEY_HMAC_SECRET`; raw signals are not persisted. Ordinary request
logs store classification and routing metadata only, never prompt or response
content.

Migration `0016_public_operations_governance` is forward-only. Rollback is an
operational restore to a pre-migration backup or a follow-up migration that
disables new routes and policies; columns/tables are not dropped because that
would destroy audit and deletion evidence.
