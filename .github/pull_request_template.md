## Outcome

Describe the production outcome and why this is the smallest safe increment.

## Compatibility and data

- [ ] `/v1` OpenAI-compatible behavior is preserved or explicitly documented.
- [ ] `rdk_*` keys and `RELAYDOCK_*` variables remain compatible.
- [ ] No existing API or database field was deleted.
- [ ] Schema changes append an immutable migration with roll-forward/rollback notes, or this PR explains why no schema change is required.
- [ ] Money uses `NUMERIC`/`DECIMAL` or integer minor units end to end.
- [ ] Funds operations are transactional, idempotent, audited, and concurrency-tested.

## Security and providers

- [ ] No real secret, personal data, prompt/response content, or production log is included.
- [ ] Provider contract status, allowed regions, and disable controls are enforced where provider behavior changes.
- [ ] The change does not enable account farming, CAPTCHA/proxy/region evasion, credential trading, or provider-terms violations.
- [ ] Threats, authorization boundaries, and sensitive logging were reviewed.

## Verification

List exact commands and results. Do not mark a command complete if it was not run.

- [ ] Go format check
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] Both frontend `npm ci`, lint, typecheck, and build
- [ ] Empty and upgrade migration integration tests
- [ ] Development and production Compose validation
- [ ] Docker image build and security scans
- [ ] API and operator documentation updated

## Rollback and residual risk

Describe image rollback, schema roll-forward/restore behavior, and risks that remain after merge.
