# Provider commercial admission matrix

**Decision:** no standard Provider is approved for commercial production
routing.  
**Evidence date:** 2026-08-21 (Migration-24 revalidation; no production approval evidence)

The routing implementation is fail-closed: a technical `enabled=true` value is
insufficient. New attempts require `COMMERCIAL_APPROVED`, resale `APPROVED`, a
valid contract window, permitted customer/model region, compatible processing
region, valid price/margin state, and a disabled kill switch. Dispatch repeats
the admission check transactionally before recording the Provider attempt.

## Current standard Provider records

| Provider slug | Technical flag | Contract state | Resale state | Legal entity | Contract type / terms | Customer regions | Processing regions | Production route decision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `openai` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `anthropic` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `gemini` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `deepseek` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `openrouter` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `qwen` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `kimi` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |
| `glm` | enabled | `CONTRACT_PENDING` | `NOT_APPROVED` | missing | `UNSPECIFIED` / missing | `*` inherited legacy value | missing | **BLOCKED** |

The legacy wildcard does not constitute a legal region approval. Empty
data-processing regions and missing contract evidence independently fail
admission. For operational clarity, operators should also turn off the
technical flag or enable the emergency kill switch until review is complete;
that change was not made during this read-only release decision.

The database also contains local integration/mock rows from earlier disposable
verification. They have the same pending/not-approved state and are not
commercial Providers. Test-created approved fixtures existed only inside the
isolated E2E database and were destroyed during cleanup.

## Evidence required per Provider

Approval must be Provider-specific and signed by authorized commercial/legal
owners. At minimum, retain and review:

1. exact contracting legal entity and contracting ModelDock entity;
2. contract type, signed date, effective/end dates, renewal and termination;
3. explicit API access and commercial resale/managed-service permission;
4. product/model scope and whether end-user disclosure is required;
5. allowed and prohibited customer countries/regions;
6. processing and storage locations, subprocessors, retention, deletion, and
   training/data-use terms;
7. security, abuse, incident, audit, export-control, and sanctions obligations;
8. current official pricing source, settlement currency, taxes/fees, rate and
   spending limits, and price-change notice process;
9. credential ownership and prohibition on pooled customer BYOK credentials;
10. approved terms version, evidence reference, reviewer identities, and
    auditable two-person approval.

## Admission procedure

1. Keep `commercial_status=CONTRACT_PENDING` and resale not approved while any
   evidence is missing or ambiguous.
2. Populate exact permitted/prohibited customer regions and processing regions;
   do not use `*` as a substitute for counsel review.
3. Publish exact Provider cost through the reviewed, append-only price-change
   workflow. The submitter must not approve their own change.
4. Validate margin protection for every sellable model/currency/region and
   publish the customer price version before admitting traffic.
5. Run contract-window, region, kill-switch, timeout, 429, 500, fallback,
   streaming, reconciliation, and deletion/retention tests for that Provider.
6. Record the approval audit entry, then change the commercial and resale
   statuses. Technical enablement is last.

CSV/API price ingestion is data ingestion only. It must use official,
contract-authorized sources and never creates accounts, bypasses verification,
trades API keys, evades regional controls, or overrides Provider safety terms.

## Emergency control

`POST /api/admin/providers/{id}/kill-switch` with `{"enabled":true}` blocks new
selections and attempts immediately and writes an audit/critical alert in the
same transaction. It does not terminate in-flight calls. After activation,
verify that the Provider-attempt count does not increase and reconcile every
in-flight request before considering re-enable.
