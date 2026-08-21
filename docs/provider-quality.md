# Platform-measured Provider quality

Migration `0021:provider_quality` adds a Provider quality plane without changing
the OpenAI-compatible `/v1` request/response bodies, `rdk_*` keys, legacy
`RELAYDOCK_*` variables, Provider contract fields, or financial ledgers.

## Evidence boundary

Only evidence independently observed or verified by ModelDock is eligible for
grading and routing:

- immutable per-attempt production observations written after an actual
  Provider dispatch;
- scheduled Models and minimal streaming completion probes executed with an
  authorized platform-owned credential from an explicitly configured egress
  region;
- response status, 429 classification, upstream first-token and complete-body
  latency, Provider-reported token counts, token throughput, deterministic
  synthetic output score, and response SHA-256;
- exact-decimal price evidence entered by an administrator after reading an
  official API/document or contract invoice;
- successful probe coverage from every policy-required region.

Supplier applications, Marketplace `uptime`, legacy model `quality_score`, and
other Provider-controlled fields remain declarations. They are shown separately
as `declared_uptime` and never feed the platform grade, intelligent-router score,
traffic cap, or circuit breaker. Prompts and response content from customer
traffic are never copied into quality tables. Synthetic responses are discarded
after a SHA-256 digest and exact-match score are derived.

## Measurement and evaluation

`provider_quality_observations` is append-only and idempotent. Production
observations use the immutable funding Provider-attempt ID as their idempotency
identity. Scheduled probes are leased with `FOR UPDATE SKIP LOCKED`, so replicas
cannot execute the same region/probe claim concurrently.

The evaluator uses a configured time window and exact PostgreSQL NUMERIC / Go
rational arithmetic. It calculates availability, error percentage, 429
percentage, p95 first-token/full-response latency, output-token throughput,
synthetic output quality, price-verification match ratio, and region coverage.
The weighted score produces `A/B/C/D/F`; evidence below `minimum_samples`
remains `UNKNOWN`. Missing output-quality, price, or region evidence does not
become a positive score. Evaluation appends an immutable rollup, updates the
single current state, opens/resolves SLA events, and writes an audit event in
one database transaction.

## Routing, ramp-up, and circuit breaking

Commercial admission remains first: contract/resale approval, dates, allowed
customer/model regions, data processing, Provider/organization policy, price,
margin, budget, kill switch, and credential ownership still fail closed.
Quality is a separate availability gate.

- Intelligent routing ignores legacy catalog quality declarations. It uses the
  platform grade/score, measured latency, and routing multiplier only.
- Automatic downweight maps deteriorating grades to a bounded multiplier.
- An active supplier link starts at `ramp_initial_bps`. A stable hash of the
  trusted request ID, Provider, and route enforces that cap for automatic
  routing. It advances only after the interval when grade is healthy and both
  price truth and region coverage are complete.
- Exact manual aliases and intelligent routes both enforce the supplier traffic
  cap from the trusted request ID. Manual selection can pin a model but cannot
  bypass commercial admission, ramp-up, kill switch, or the quality circuit.
- Consecutive critical availability/error/429 breaches open the circuit. New
  attempts return a generic `503 provider_unavailable`. After the open period,
  measured recovery enters `HALF_OPEN`; the configured recovery count is needed
  to close. An administrator reset only moves to `HALF_OPEN` and is audited.
- In-flight streams are not terminated. No request is replayed and no alternate
  credential or region is used to evade a Provider limit.

## API and UI

The Admin **Provider Quality** page uses:

- `GET /api/admin/provider-quality`;
- `PUT /api/admin/providers/{id}/quality-policy`;
- `POST /api/admin/providers/{id}/quality/evaluate`;
- `POST /api/admin/providers/{id}/quality/circuit-reset`;
- `POST /api/admin/providers/{id}/supplier-link`;
- `GET /api/admin/provider-quality/sla-events`;
- `GET/POST /api/admin/provider-quality/price-verifications`.

Price verification POST requires `Idempotency-Key`, exact decimal strings, an
evidence SHA-256, and an official source reference. Supplier/console sessions
cannot access these endpoints.

## Operations and security

Set `RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION` to the actual ISO alpha-2 egress
location of each probe-capable deployment. An empty value disables scheduled
probes rather than guessing a region. Multiple deployments may use different
regions against the same PostgreSQL database; only regions listed in the policy
are scheduled.

Before enabling a policy, verify Provider commercial/resale approval and region,
an active platform-owned credential, an enabled Provider-owned probe model,
contract permission for recurring synthetic calls, approved small probe cost,
current official price evidence, and actual runners in every required region.
Monitor `relaydock_provider_quality_*`, SLA events, probe leases, audit
transitions, 429 rate, and traffic caps.

Provider endpoint allowlisting, public-network DNS pinning, HTTPS requirements,
encrypted credentials, and adapter policies also apply to probes. The worker
never accepts a supplier-supplied URL, credential, proxy, prompt, or region.
Logs contain Provider/attempt IDs and error classes only—not secrets,
authorization headers, response bodies, or customer prompts. Price evidence
stores a digest and reference, not uploaded contract/invoice content.

## Migration and rollback

Apply migration 0021 with the normal migration job. It creates only new tables,
indexes, triggers, and neutral disabled policy/state rows; existing Providers
remain routable exactly as before until an administrator enables a policy.

Routine rollback is application-first: set every quality policy `enabled=false`,
deploy the previous image, and retain migration 0021 and its evidence. Previous
binaries ignore the additive tables/trigger. Do not delete migration ledger,
observations, price evidence, rollups, SLA, or audit rows.

A destructive rollback requires a separately approved outage: stop all writers,
export every quality/supplier-link record, drop the Provider seed trigger and
quality immutability triggers/function, then drop tables in reverse dependency
order. This destroys operational evidence and is not a normal rollback. It must
not modify existing Provider, request, usage, wallet, funding, journal, or audit
records.
