# Contributing to ModelDock

ModelDock is developed incrementally on top of the RelayDock compatibility
surface. Changes must preserve `/v1`, `rdk_*` keys, `RELAYDOCK_*` environment
variables, existing database fields, and published API behavior unless an
approved compatibility migration explicitly says otherwise.

## Development setup

Use Go 1.26.6, Node.js 22.22.3, npm, Docker with Compose v2, and PowerShell 7 for the
integration suites. Never commit `.env`, credentials, customer data, provider
responses, or production logs.

```bash
go mod download
npm --prefix apps/admin-web ci
npm --prefix apps/console-web ci
```

Create local configuration from `.env.example` and replace every
`CHANGE_ME` value with development-only random material.

## Required checks

Run the same gates as CI before opening a pull request:

```bash
test -z "$(gofmt -l cmd internal migrations tests/backend)"
go vet ./...
go test ./...
npm --prefix apps/admin-web run lint
npm --prefix apps/admin-web run typecheck
npm --prefix apps/admin-web run build
npm --prefix apps/console-web run lint
npm --prefix apps/console-web run typecheck
npm --prefix apps/console-web run build
docker compose --env-file .env config --quiet
docker compose --env-file deploy/production/.env -f docker-compose.production.yml config --quiet
```

Database work must append an immutable migration, document roll-forward and
rollback behavior, and pass the disposable empty/upgrade migration contract:

```powershell
pwsh ./tests/integration/verify-migrations.ps1 `
  -EnvFile .env -ConfirmIsolatedTestDatabase
```

Never edit a released SQL migration. The runtime verifies every recorded
migration checksum and rejects unknown future schema versions.

## Pull requests

- Keep one production concern per pull request and include tests and operator
  documentation in the same change.
- Explain compatibility impact, transaction/idempotency behavior, security
  boundaries, migration rollback, and commands actually executed.
- Use Conventional Commit subjects (`feat:`, `fix:`, `docs:`, `ci:`,
  `security:`) so release notes remain auditable.
- Do not add account farming, CAPTCHA bypass, proxy or region evasion,
  credential trading, or behavior that violates provider terms.
- Do not add a repository license without the project owner's recorded
  decision described in `docs/licensing-decision.md`.

PRs require passing CI and CODEOWNERS review. A green PR is necessary but not
sufficient for release; `RELEASE.md` defines the additional release gates.
