# Marketplace dispute policy

Policy version: `marketplace-launch-2026-08-21`

## Scope

A supplier may dispute a platform-measured usage accrual, settlement batch, supplier-bill reconciliation, or financial reconciliation case. A dispute must identify exactly one supported resource, state the reason, and provide bounded non-secret evidence. Credentials, full customer prompts, personal data, and payout destinations must not be submitted as dispute evidence.

## Filing and preservation

The signed order form defines the filing period; where it is silent, the operational default is 30 calendar days from the relevant statement or batch notice. ModelDock preserves the request identifier, usage snapshot, funding status, Provider statement match, payable entries, and audit history. Supplier declarations remain separate from platform evidence.

An open or under-review appeal blocks an unpaid related payout. Appeals cannot reverse an already transmitted payout by editing ledger history; an upheld post-payment dispute is handled through a new, referenced adjustment or recovery entry.

## Review and decision

An administrator who is not relying solely on the supplier declaration compares platform request/usage records, official Provider statement evidence, exact decimal amounts, refunds, and prior decisions. Outcomes are `UPHELD`, `REJECTED`, or supplier `WITHDRAWN`, each with a reason, actor, and timestamp. A material conflict or security concern is escalated to legal/security review.

## Appeals and service continuity

The supplier may appeal through the contracted support channel with new evidence. ModelDock may keep traffic reduced, suspended, or cut over while a dispute presents customer, financial, contractual, or security risk. Resolving the commercial dispute does not automatically close a quality circuit; measured recovery remains required.
