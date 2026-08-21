# Provider Marketplace service agreement

Policy version: `marketplace-launch-2026-08-21`

## Scope and authority

The supplier authorizes ModelDock to list, route to, meter, resell where permitted, invoice for, and support the approved models and regions recorded in the release review. The supplier warrants that it controls the submitted endpoint and credentials and has all rights needed to serve the approved models. No unapproved model, region, subprocesser, or credential source is included.

The commercial contract must be `ACTIVE`, resale permission must be `APPROVED`, its effective dates must be current, and both parties' identified legal entities must match the reviewed records. Technical availability never substitutes for commercial authorization.

## Measurement, ranking, and charging

Platform request records, token measurements, quality observations, price verifications, wallet operations, and append-only ledgers are authoritative for routing, customer charging, commission, supplier payable, and settlement. Supplier uptime, price, usage, or quality submissions are declarations for review only. They cannot directly change ranking, charge a user, or authorize payout.

ModelDock may downweight, cap, circuit-break, suspend, or cut traffic when platform measurements or policy checks fail. Canary traffic is deterministically limited by the platform quality state.

## Fees and settlement

Commission, risk reserve, minimum payout, currency, cycle, refund allocation, statement matching, tax status, invoice status, and payout adapter are governed by the approved supplier settlement policy and the [settlement policy](settlement-policy.md). A payout contains only platform-settled, charged, undisputed usage matched to reviewed Provider statement evidence.

## Service and support

The supplier will maintain the [SLA rules](sla-rules.md), a monitored security contact, a billing contact, and an incident channel. ModelDock may publish platform-measured availability and quality grades. Service credits or commercial remedies are calculated only from platform evidence and the signed order form.

## Security, data, and compliance

The supplier will follow the [supplier policy](supplier-policy.md), [data processing addendum](data-processing-addendum.md), applicable law, sanctions and export restrictions, and Provider/model terms. Credentials are write-only encrypted inputs and may not be shared, sold, scraped, farmed, or obtained through registration automation, verification bypass, proxy evasion, or regional circumvention.

## Changes, suspension, and exit

Material endpoint, ownership, model, price, region, retention, subprocesser, or contract changes require a new review before production use. ModelDock may immediately cut traffic for security, legal, payment, quality, or customer-harm risk. Exit ends new traffic and revokes release approval but preserves audit, financial, tax, dispute, and legal-hold records. Outstanding eligible amounts remain subject to reconciliation and the payout readiness gate.

## Audit and precedence

Both parties retain the records required by the signed order form and applicable law. The signed order form prevails over this policy where it explicitly identifies the conflicting clause; the security and fail-closed release controls remain mandatory unless replaced by an equally protective written control.
