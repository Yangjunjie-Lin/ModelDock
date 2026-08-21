# Provider commercial governance and BYOK

Migrations `14:provider_commercial_governance` and `15:provider_pricing_hardening` add a fail-closed commercial admission plane without changing the OpenAI-compatible `/v1` request or response shapes, RelayDock API-key format, or existing environment variable names.

## Admission order

Every exact and intelligent route is filtered before credential scheduling by commercial approval and resale permission, contract dates, kill switch, customer/model regions, organization Provider policy, data-processing regions, aggregate RPM, exact-decimal Provider budget, current margin, and credential ownership.

Technical health is only a scheduler signal. `TECHNICALLY_AVAILABLE` and `CONTRACT_PENDING` never carry paid production traffic. Intelligent routing removes ineligible candidates before scoring. Dispatch rechecks commercial state and the kill switch in the transaction that records the Provider attempt.

Region failures use `provider_region_unavailable` and a generic message. The response does not reveal contract terms, prohibited-region lists, Provider IDs, budget balances, or credential metadata.

## Cost synchronization and approval

Manual entry, Provider API input, and CSV input use the same `provider_cost_change_requests` workflow (`MANUAL`, `API`, `CSV`). Each input requires an idempotency key and exact decimal values. Approval must be performed by a different administrator, appends a `provider_cost_price_book` row, and emits an operator alert; rejection publishes nothing. `effective_at` controls when the row becomes eligible.

The fetch endpoint accepts only HTTPS hosts explicitly listed in the Provider's `config.pricing_api_hosts`. It resolves and pins public IP addresses, disables proxy use, rejects private/loopback/link-local/multicast addresses and cross-host redirects, and caps the request at five seconds and 1 MiB. The JSON contract is `{ "prices": [...] }` using the same columns as CSV. It never accepts a caller-supplied authorization header or returns upstream response bodies.

CSV is posted as `text/csv`, is capped at 1 MiB and 500 data rows, and requires `model_id,input_token_cost,cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at`; `expires_at` and `idempotency_key` are optional. Imports are atomic and reject exponent notation, signs, formulas, values exceeding `NUMERIC(30,12)`, or malformed RFC3339 timestamps. Only a SHA-256 content reference is retained.

Published price books, resolved `model_price_version`, and `usage_price_snapshot` rows remain append-only. A later cost never changes a historical quote, funding operation, wallet journal, or invoice amount.

CSV/API ingestion is data ingestion only. It never creates Provider accounts, buys or exchanges keys, automates verification, bypasses regions, or bypasses Provider safety controls. API sources must be official contract-authorized price feeds.

## BYOK boundary

Customer credentials are marked `credential_owner=CUSTOMER`, encrypted with the existing AES-256-GCM vault, and bound to one immutable `owner_organization_id`. An authenticated user needs Developer access to an active project in that organization and must explicitly accept the recorded ownership terms version. List APIs return only redacted metadata.

The scheduler admits a customer credential only for its owner organization. Database checks prevent owner transfer. BYOK never pools credentials across organizations. BYOK settlement records `credential_owner`, sets Provider cost to zero for the platform, and makes the immutable platform service fee—not the ordinary retail rate—the customer sale and final amount in both funding and usage snapshots. The applied service-fee policy ID is retained.

Administrators publish append-only policies at `GET/POST /api/admin/pricing/byok-service-fee-policies` and may disable one with `DELETE /api/admin/pricing/byok-service-fee-policies/{id}`. Terms cannot be edited, deleted, or re-enabled; publish a new future-effective policy for changes. BYOK fails closed if no applicable organization/provider policy exists or if its currency differs from the funding operation.

## Kill-switch operation

`POST /api/admin/providers/{id}/kill-switch` with `{ "enabled": true }` stops new selections and new Provider attempts immediately. In-flight calls are not force-terminated, preserving streaming safety and accounting. State and audit commit in one transaction; enabling it also creates a critical alert.

## Migration and rollback

Upgrade is forward-only. Back up PostgreSQL; apply migrations 14 and 15; confirm both ledger rows; then review all Provider records. Only legacy records with an existing `contract_reviewed_at` become approved; all others become `CONTRACT_PENDING`. Set legal entity, resale permission, dates, regions, residency, limits, currency and terms before approval. Publish a BYOK service-fee policy before enabling BYOK traffic.

Routine rollback uses the previous application image while retaining migrations 14 and 15. Do not delete migration ledger rows. Migration 15 adds one nullable snapshot foreign key and immutability triggers; an older binary tolerates them. Destructive schema rollback requires stopping writers, exporting all audit/price/funding/usage evidence, proving no snapshots reference a fee policy, then dropping the 15 triggers/function/column before considering migration 14 in reverse dependency order. This destroys commercial evidence and is not a routine rollback.
