# Account lifecycle, invitations, MFA, and mail outbox

This document is the production contract introduced by migration `7:accounts`.
It is additive: `/v1`, `rdk_*` keys, RelayDock cookies, `RELAYDOCK_*`
configuration names, existing user fields, and administrator-created users are
unchanged.

## Registration policy and account states

`RELAYDOCK_REGISTRATION_MODE` is evaluated server-side on every registration
attempt:

| Mode | New account creation |
| --- | --- |
| `CLOSED` | Administrator `POST /api/admin/users` only. Public registration and invitation-backed creation are denied. Existing users may still accept an organization invitation. |
| `INVITE_ONLY` | A valid bounded registration code or unexpired organization invitation is required. |
| `PUBLIC` | Email/password registration is available; organization invitations also work. |

User states are `PENDING_VERIFICATION`, `ACTIVE`, `SUSPENDED`, and `CLOSED`.
Only `ACTIVE` users can log in, refresh sessions, or pass control-plane
middleware. Migration 7 maps the former `DISABLED` user state to `SUSPENDED`;
the API continues to accept `DISABLED` as an input compatibility alias.

```text
register -> PENDING_VERIFICATION -> verify once -> ACTIVE
ACTIVE -> administrator suspend -> SUSPENDED -> administrator restore -> ACTIVE
ACTIVE/SUSPENDED -> administrator close -> CLOSED
```

An organization invitation proves control of the invited email address and can
activate an invited account. The invitation row is locked and consumed in the
same transaction that creates membership, preventing concurrent replay.

## Tokens and session revocation

Verification, reset, registration-invite, and organization-invite plaintexts
are generated from 256 bits of randomness and returned or emailed once. Only an
HMAC-SHA-256 digest keyed by `RELAYDOCK_JWT_SECRET` is stored. Account tokens
have an expiry and `consumed_at`; consumption, state change, workspace creation,
and audit insertion share one PostgreSQL transaction.

Every access and refresh JWT contains `session_version`. Password reset,
password change, account suspend/restore/close, MFA enrollment/disable, and
"logout other sessions" advance the database version. Middleware and refresh
both compare it, so previously issued refresh tokens cannot mint new sessions.

Login, registration, email verification/resend, and password reset use
independent Redis counters. Counter identities are SHA-256 hashed before being
placed in Redis. A Redis error fails these protections closed. Login performs
the same adaptive password-hash work for unknown addresses and returns the same
credential error for unknown, pending, suspended, closed, or wrong-password
accounts. Forgot-password and resend-verification always return a generic `202`.

## Administrator TOTP MFA

Administrators can enroll RFC 6238 TOTP from Admin Settings. Pending and active
TOTP secrets are AES-256-GCM encrypted with `RELAYDOCK_MASTER_KEY` and user ID
authenticated as encryption context. Codes accept one 30-second step of clock
skew. The matched step is updated with compare-and-swap, so a code cannot be
replayed.

Set `RELAYDOCK_ADMIN_MFA_REQUIRED=true` in production. An administrator without
MFA receives a restricted session that can call only the MFA status/setup/
confirm endpoints; other administrator endpoints return `mfa_required`.
Maintain at least two independently controlled administrator accounts before
enforcing MFA and document an offline database-restore break-glass procedure.

## Email outbox

HTTP transactions never connect to SMTP. They insert an `email_outbox` row in
the same transaction as the token or invitation. The complete message,
including its one-time URL, is encrypted with `RELAYDOCK_MASTER_KEY`; list APIs
never return ciphertext. Workers claim rows using `FOR UPDATE SKIP LOCKED`, a
unique claim token, and a lease. Delivery failures use bounded exponential
backoff and move to `DEAD` after `RELAYDOCK_MAIL_MAX_ATTEMPTS`. An administrator
can requeue only dead rows, and that action is audited.

`RELAYDOCK_MAIL_PROVIDER=local` writes mode-`0600` JSON captures under
`RELAYDOCK_MAIL_CAPTURE_DIR` for development. Treat captures as secrets because
they contain live links; keep the directory out of source control and delete it
on a short schedule. Production should use `smtp` with `starttls` or implicit
`tls`; credentials come only from environment/secret-manager injection.

Monitor `email_outbox` counts by status, oldest `available_at`, attempts, and
worker error logs. Alert on any `DEAD` row or a growing ready queue. Rotating
`RELAYDOCK_MASTER_KEY` requires re-encrypting undelivered outbox and TOTP
secrets before the old key is retired.

## Audit events

Security events are written without password, token, TOTP secret, email-body,
or session plaintext. Events cover registration, verification, password-reset
request/completion, password changes, session revocation, invitation lifecycle,
MFA enrollment/use failures, login success/failure, rate-limit rejection,
account suspension/restoration/closure, and dead-letter requeue.

## Migration and rollback

Migration 7 is forward-only and runs transactionally through the existing
embedded migration ledger. Before upgrade, back up PostgreSQL and escrow the
master key. Validate both a fresh database and an upgrade copy.

Application rollback to a binary that knows only migrations 1-6 is not safe:
that binary rejects the newer migration ledger and writes the obsolete user
status `DISABLED`. The preferred rollback is restore the pre-upgrade database
backup and old image together. If data created after upgrade must be retained,
build a compatibility rollback release that understands ledger version 7 and
the new statuses; do not simply delete the ledger row.

For a full schema reversal in an isolated maintenance window: stop all
application and mail workers; export `account_tokens`, invitations,
`email_outbox`, and new user security columns for audit retention; map
`SUSPENDED` users back to `DISABLED`; restore the original user status check;
then drop the new tables and columns only after confirming no required account,
membership, reset, or MFA data will be lost. Finally remove ledger version 7
and start the version-6 binary. This reversal is destructive and must be tested
against a restored copy, never improvised on the production primary.

## Operational verification

Run from the repository root:

```powershell
docker compose --env-file .env config --quiet
.\tests\integration\verify-migrations.ps1 -ConfirmIsolatedTestDatabase
.\tests\integration\verify-accounts.ps1 -ConfirmIsolatedTestDatabase
```

The account integration verifies PUBLIC registration through first `rdk_test_`
API use, CLOSED bypass rejection, password-reset replay rejection, generic
forgot-password behavior, organization invitation transitions, session-version
revocation, concurrent token consumption, and administrator MFA enforcement.

