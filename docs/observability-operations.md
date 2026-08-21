# Observability, support, and incident operations

This release adds structured correlation, OpenTelemetry trace export,
Prometheus metrics, SLO evidence, alert rules, a public-safe status API, and an
audited support workflow. It does not change any `/v1` request or response
contract, `rdk_*` key format, or existing `RELAYDOCK_*` compatibility setting.

## Signal flow

Every HTTP response carries `X-Request-Id`, `X-RelayDock-Request-Id`, and a W3C
`traceparent`. JSON logs include `service.name`, `service.version`,
`request_id`, `trace_id`, `span_id`, component, status, and latency. The trusted
server request ID and trace ID are persisted in `request_logs`; prompt content,
responses, authorization headers, API keys, and Provider secrets are not.

Set `RELAYDOCK_OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318` and
`RELAYDOCK_OTEL_EXPORTER_OTLP_INSECURE=true` only on an authenticated private
network. The application exports OTLP/HTTP spans and forwards W3C context to
Provider adapters. With no endpoint, W3C propagation and structured-log/database
correlation remain active without an exporter. The bundled Collector uses a
debug exporter so a clean deployment has no external credential; production
must replace that exporter with an approved trace backend and a separately
mounted secret.

The control listener exposes `/metrics`. Keep it on the internal network.
Prometheus scrapes every 15 seconds and sends evaluated alerts to Alertmanager.
The `observability` Compose profile is optional so application rollback does
not depend on the monitoring stack.

## Core metrics

| Signal | Metric or calculation |
| --- | --- |
| Requests, total latency, first token latency | `relaydock_requests_total`, `relaydock_request_latency_ms_*`, `relaydock_first_token_latency_ms_*` |
| Input/output tokens and fallback | `relaydock_input_tokens_total`, `relaydock_output_tokens_total`, `relaydock_fallback_total` |
| Provider success and limiting | `relaydock_provider_{requests,success,failures,rate_limited}_total{provider}` |
| Provider independent quality | `relaydock_provider_quality_{score,availability_percent,error_percent,429_percent,throughput_tps,routing_multiplier,traffic_cap_bps,circuit_open}` |
| Wallet reservation and settlement | `relaydock_wallet_reservation_failures_total`, `relaydock_settlement_*`, `relaydock_settlement_backlog` |
| Payments and reconciliation | `relaydock_payment_attempts_total{status}`, `relaydock_payment_webhook_failures`, `relaydock_reconciliation_difference_total`, `relaydock_reconciliation_open_differences` |
| Model economics | `relaydock_model_{revenue,cost,gross_margin}_total{model}`, `relaydock_model_gross_margin_ratio{model}`, `relaydock_negative_margin_requests_total` |
| Dependencies and capacity | `relaydock_dependency_*_up`, PostgreSQL/Redis pool gauges, and node-exporter disk gauges |

Money metrics are accumulated as decimal rational values and emitted as
decimal text. The database remains authoritative and uses `NUMERIC`; monitoring
metrics are operational aggregates, never financial ledger evidence. Payment
success rate is successful webhook outcomes divided by all terminal webhook
outcomes. Provider success rate is successes divided by requests; rate-limited
requests are a distinct outcome and are not counted as success.

Provider quality metrics are projections of immutable database observations
and rollups. `published_uptime` is platform measured; Marketplace/Provider
`declared_uptime` is displayed separately and cannot affect routes. See
[provider-quality.md](provider-quality.md).

## SLOs

Migration 0017 seeds these operational targets. They are evidence windows, not
contractual customer SLAs.

| SLO | Target/window | Evidence |
| --- | --- | --- |
| Gateway availability | 99.90% / 30m | Gateway control success / requests |
| Control-plane availability | 99.90% / 30m | Control-plane success / requests |
| Payment webhook processing | 99.50% / 60m | Successful verified webhook outcomes / terminal outcomes |
| Ledger settlement latency | 99.00% / 60m | Settlement success/latency counters plus backlog gauge |
| Provider routing success | 99.00% / 30m | Provider successes / Provider requests; fallback is separate |

Use multi-window burn-rate alerting when connecting an external SLO platform.
`GET /api/admin/observability` exposes current in-process evidence and database
targets. Process restarts reset counters; Prometheus retains time series.

## Alerts and ownership

`deploy/observability/modeldock-alerts.yml` covers Provider fleet failures,
settlement failure/backlog, payment webhook failure, PostgreSQL/Redis outage,
negative margin, wallet anomalies, suspected API Key leakage, database/Redis
pool pressure, and disk exhaustion. Application alerts dedupe on an open-alert
key and resolve on a recovery signal where one exists. Alertmanager groups
repeats. Configure a real paging receiver through a secret-mounted replacement
file before production; the empty local receiver is deliberate and must not be
represented as external paging.

## Request investigation and support

Given a customer request ID, an administrator calls:

```text
GET /api/admin/observability/requests/{requestID}
```

The response joins route alias, Provider, attempt/fallback state, token usage,
timing, exact-decimal price evidence, funding reservation and settlement,
wallet transaction, and posted journal ID. Use `trace_id` for the trace backend
and `request_id` for JSON logs. Do not ask for an API Key or prompt.

Users create and reply under `/api/console/support/tickets`; admins use
`/api/admin/support/tickets`. A ticket can link user, organization, request ID,
recharge order, and ledger journal. Messages are `PUBLIC` or `INTERNAL`;
console reads never return internal notes. Writes are transactional and audited.
Bodies redact `rdk_*`, common Provider keys, bearer tokens, assigned secrets,
and email addresses. Context uses an explicit correlation-field allowlist.
Redaction is defense in depth: operators must not paste credentials or content.

## Public status privacy

`GET /status` and `GET /api/status` require no authentication. They return only
Gateway, Dashboard, Billing, Provider slug/status, public incident text, and
timestamps. Credentials, contract/region details, customer IDs, request IDs,
internal metadata, and support data are never serialized. Review every
`public_message` for customer-safe wording before publication.

## Migration and rollback

Migration `0017_observability_support` is additive: it adds `trace_id` to
`request_logs`, dedupe/resolution metadata to `alerts`, four new tables, and
indexes. It neither rewrites financial evidence nor removes an old field.

Preferred rollback: stop the observability profile, clear the OTLP endpoint,
and deploy the prior application image while leaving migration 0017 in place.
Old binaries ignore nullable columns and new tables. Do not delete status,
ticket, alert, request, or audit evidence during an incident.

Destructive rollback is permitted only after export and restore rehearsal:
drop `support_ticket_messages`, `support_tickets`, `observability_slos`, and
`status_events`; drop the 0017 indexes; then drop only the new nullable alert
and request-log columns and remove schema ledger version 17. This loses support
and incident evidence and requires an approved maintenance window. Restore a
backup instead if later migrations or application data must be retained.

## Incident runbooks

### Provider outage

**Stop loss:** enable the affected Provider emergency kill switch, retain only
contracted/region-allowed fallback routes, lower admission if fallback capacity
is uncertain, and publish a Provider status incident.

**Diagnose/recover:** inspect failure/429 metrics, request investigation output,
Provider attempts, contract status, allowed regions, and official Provider
status. Restore only after a canary succeeds without bypassing Provider terms.

**Rollback:** revert the route or credential-group change to the last audited
version, disable a harmful fallback, and resolve the event after metrics recover.

### Duplicate charge

**Stop loss:** pause the affected settlement/recovery path and payment manual
approvals; do not edit wallet balances or posted journals.

**Diagnose/recover:** locate operations by idempotency key/request ID, compare
immutable usage snapshots, wallet transactions, and journals, then use the
existing idempotent reversal API with named operator and reason.

**Rollback:** reverse only the erroneous journal with a new balanced journal.
Deploy the prior image while retaining migration/evidence; never delete source
transactions.

### Payment succeeded but balance missing

**Stop loss:** keep the order pending and block manual duplicate topups. Preserve
the signed webhook digest and query the contracted payment adapter.

**Diagnose/recover:** verify Provider event ID, signature/timestamp, amount,
currency, replay key, processing state, and reconciliation record. Run the
normal idempotent credit recovery only with independent evidence.

**Rollback:** if recovery credit was wrong, use the approved refund/reversal
workflow and prior application image; never change an order by direct SQL.

### Database outage

**Stop loss:** fail readiness, stop Gateway admission when funding state cannot
be read, pause workers, and protect disk space. Do not promote an unverified
replica.

**Diagnose/recover:** check reachability, pool saturation, locks, disk,
replication, and the last verified backup. Restore in isolation and verify
migration checksums and ledger balance before reopening traffic.

**Rollback:** roll back the application image while preserving additive schema,
or fail over to a verified recovery point. Declare the data-loss window and
reconcile payments received afterward.

### API Key leakage

**Stop loss:** freeze the matching logical key and all active versions, revoke
it at the customer boundary, rate-limit the source, and preserve only hashes
and correlation metadata. Never echo the reported key.

**Diagnose/recover:** use hash/prefix, audit logs, request IDs, IP/device HMACs,
route usage, and user confirmation. Rotate only after the client is clean.

**Rollback:** unfreeze only a reviewed false positive; otherwise issue a new
key and leave the exposed version revoked. Revert an overbroad rule after
evidence review, not by disabling leak alerts.

### Incorrect cost price

**Stop loss:** suspend approval/publication for the affected Provider/model,
disable negative-margin routes if needed, and preserve effective-dated versions.

**Diagnose/recover:** compare the approved official source, immutable snapshot,
currency/unit/exchange rate, and affected request IDs. Publish a corrected
forward-effective price through two-person approval.

**Rollback:** disable or supersede the erroneous version; do not mutate an
immutable snapshot. Use audited adjustments/reversals for affected charges.

### Mass negative margin

**Stop loss:** disable affected model routes or cap admission, stop promotional
overrides, and publish an incident if availability changes.

**Diagnose/recover:** group negative requests by model, Provider, price version,
currency, fallback, and organization. Validate totals against financial reports
and Provider statement evidence.

**Rollback:** return routes/prices to the last approved profitable combination,
or keep the model disabled. Reverse only proven incorrect charges and retain
all evidence for reconciliation.

### Data security event

**Stop loss:** isolate affected services, revoke credentials/sessions, freeze
exports and retention jobs, restrict logs/status text, and engage security/legal
owners.

**Diagnose/recover:** preserve audit/trace evidence, determine data classes,
tenants, regions, and time bounds, and follow notification obligations. Do not
copy sensitive payloads into tickets or public status.

**Rollback:** redeploy the last verified image/configuration, restore only from
a clean backup, rotate possibly exposed secrets, and reopen in stages. Never
delete audit, ticket, or incident history as rollback.
