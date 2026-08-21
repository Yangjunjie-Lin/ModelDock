# ModelDock V3

ModelDock evolves RelayDock without replacing its data plane or compatibility
contracts. Existing `/v1` clients, `rdk_live_*` and `rdk_test_*` keys, project
route grants, provider credentials, balances, usage, and audit history continue
to work after migration `6:modeldock`.

## Capability map

| Module | V3 behavior |
| --- | --- |
| Model Registry | Dynamic models, capabilities, context window, quality score, latency penalty, and effective versioned price |
| Providers | Runtime registry; OpenAI, Anthropic, Gemini, DeepSeek adapter types; Qwen, Kimi, GLM, OpenRouter compatible surfaces |
| Intelligent Router | Manual aliases plus cost, quality, and balanced selection across project-granted routes |
| Marketplace | Provider endpoint, supported models, price metadata, status, verification, and uptime foundation |
| Enterprise | Organizations, projects, teams, memberships, API quotas, budgets, and audit log |
| Billing | Organization wallet, prepaid/postpaid mode, idempotent transaction ledger, and request-level usage records |
| Dashboard | Tokens and cost today, estimated intelligent-routing savings, model distribution, latency, and provider health |

## Routing

Manual routing is unchanged:

```json
{ "model": "gpt-5" }
```

Built-in aliases enable intelligent selection without creating a rule:

- `auto:cost`
- `auto:quality`
- `auto:balanced` (also `auto`)

Administrators can create project-specific aliases in **Routing Rules**. Only
enabled models behind enabled physical routes already granted to that project
are candidates. Optional rule config can constrain `model_type`, `providers`,
`required_capabilities`, `max_input_price`, and `max_output_price`.

Balanced routing normalizes the exact catalog price within the candidate set
and uses platform-measured Provider quality/latency. Legacy model quality and
Marketplace uptime fields remain compatibility declarations and are ignored:

```text
score = quality_weight × quality
      - price_weight × normalized_price
      - latency_weight × latency_penalty
```

Selection is deterministic on equal scores. The selected strategy, score,
candidate count, route, model, and provider type are stored in the redacted
request decision metadata.

New approved suppliers enter automatic routing through a basis-point traffic
cap. Platform measurements can advance or reduce the cap; critical consecutive
breaches open a Provider circuit for all new attempts. See
[provider-quality.md](provider-quality.md).

## Billing semantics

Every organization receives one wallet. Existing organizations migrate as
`POSTPAID`, which prevents an upgrade from blocking established traffic.
Administrators may switch a wallet to `PREPAID`; prepaid admission fails with
HTTP `402` when available balance plus credit is not positive.

Successful priced requests resolve an immutable commercial price version before
dispatch, then create `usage_price_snapshot`, `billing_usage_records`, and one
negative `CHARGE` transaction in the same PostgreSQL transaction as request,
daily/hourly usage, budget event, and Webhook outbox persistence. The wallet
charge uses only `final_user_amount`; the legacy `estimated_cost` field remains
for compatibility and budget dashboards. `request_id` and the wallet transaction
idempotency key prevent duplicate charges. See [pricing.md](pricing.md).

Dashboard savings are estimates for intelligent routes only. The reference is
the highest current price among enabled models granted to the project, not a
provider invoice.

## Upgrade

1. Back up PostgreSQL and the master-key escrow.
2. Build the new images and run `docker compose config --quiet`.
3. Start the ModelDock binary; embedded migration `6:modeldock` is transactional
   and protected by the existing PostgreSQL advisory migration lock.
4. Confirm `/readyz`, wallet creation, seeded providers, and existing manual
   aliases before enabling intelligent routes or prepaid billing.
5. Run `tests/integration/verify-migrations.ps1` against an isolated database.

Runtime names such as the Go module, Compose project, `RELAYDOCK_*` variables,
and key prefixes intentionally remain stable for upgrade compatibility.
