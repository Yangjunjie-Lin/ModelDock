# Marketplace launch acceptance and operations

Migration `0023_marketplace_launch_acceptance` adds a fail-closed release layer without changing first-party `/v1` behavior or the existing listing API. It preserves existing `ACTIVE` declarations during upgrade, but any Provider with a non-ended supplier link must have a valid canary or approved release review to pass route selection and the dispatch recheck.

## Gate sequence

1. Complete supplier registration, KYB/qualification, contract, endpoint ownership/isolation, model application, price application, and security questionnaire.
2. Link the approved supplier to an enabled Provider quality policy. Linkage starts the configured low traffic cap.
3. Create a listing in `DRAFT`/`REVIEW` and open an idempotent release review.
4. Review contract, tax, encrypted payment destination, and security evidence in payout readiness. These statuses never come from supplier self-approval.
5. Evaluate platform evidence. The six foundation gates must pass before `CANARY_START`.
6. Run controlled `/v1` calls and the financial rehearsal. Re-evaluate until user call, charge, commission, payable, refund allocation, paid sandbox settlement, reconciliation, and resolved dispute evidence pass.
7. Attach reviewed drill-report references for supplier suspension, emergency cutover, and supplier exit.
8. A second administrator approves the release. PostgreSQL refuses `ACTIVE` without all gates.

## Operations

Use `GET /api/admin/marketplace/launch-reviews` and the admin Marketplace page for the queue. Evaluation is repeatable and each result appends a gate event. Do not paste credentials, payout destinations, tax identifiers, prompts, or personal data into evidence references or reasons.

`SUSPEND` disables the Provider, suspends its link/listing, and suspends the supplier. `EMERGENCY_CUTOVER` also opens the Provider kill switch immediately. `RESUME` requires the approved review and restores only reviewed state. `EXIT` is rejected while a payout is processing, ends links, disables Providers, revokes release approvals, and preserves finance/audit evidence.

Alerts should page on emergency cutover, failed production payout readiness transition, open quality circuit, and repeated payout failure. Review gate age, canary duration, current price verification, contract expiry, and open disputes daily.

## Rollback

The V23 schema is additive, but a pre-V23 binary running its own migration mode
will intentionally reject a newer migration ledger. An older binary may be used
only with the externally owned migration/verification deployment mode after
compatibility review, and every supplier-linked Provider must remain disabled
because that binary does not enforce the Marketplace gate. To disable the new
operational surface, block the new admin routes, use `SUSPEND` or
`EMERGENCY_CUTOVER`, return production payout readiness to `PENDING`, and
preserve all evidence. Physical schema removal requires a separately reviewed
maintenance migration after all V23 binaries are retired.
