# Marketplace data processing addendum

Policy version: `marketplace-launch-2026-08-21`

## Roles and instructions

For customer content routed through the Marketplace, the customer is the controller or business, ModelDock is its processor/service provider, and the supplier is a subprocesser unless the signed order form specifies another lawful role. The supplier processes data only to serve the approved model request, secure the service, meet legal obligations, and perform documented support.

## Data and subjects

Processed data may include API request content, model output, token counts, request identifiers, account/project identifiers, IP-derived region, safety metadata, and technical logs. Data subjects depend on customer workloads and may include customer personnel and end users. Secrets, payout destinations, and tax identifiers are not sent to model endpoints.

## Location, retention, and subprocessors

Processing and storage are limited to the approved regions in the release review. Cross-border transfer, retention days, and subprocessors must match the approved supplier declarations and contract mechanism. Any material change requires prior notice and a new review; an unapproved region or subprocesser fails routing eligibility.

The supplier must delete or return customer content at the end of the approved retention period and may retain only legally required, access-controlled records. Customer content may not be used for model training or unrelated profiling without explicit controller authorization.

## Security and incidents

The supplier maintains encryption in transit, tenant isolation, least privilege, credential rotation, vulnerability management, secure development, logging, incident response, and personnel controls represented in its approved questionnaire. It must notify ModelDock through the contracted security channel without undue delay after discovering a relevant incident and provide containment, affected scope, timeline, and remediation evidence.

## Assistance, deletion, and audit

The supplier reasonably assists with data-subject requests, regulatory inquiries, impact assessments, breach response, export, and deletion. ModelDock may verify compliance using questionnaires, reports, certifications, targeted tests, and contractually scoped audits. Exit revokes routing and credentials but preserves only records required for finance, disputes, security, audit, or legal hold.
