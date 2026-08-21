# Supplier onboarding and review

Migration `0020:supplier_onboarding` adds an application workflow for third-party model inference suppliers. It is additive: the existing `providers` table, `rdk_*` keys, `RELAYDOCK_*` variables, and OpenAI-compatible `/v1` behavior are unchanged.

## Lifecycle

An authenticated console user creates one supplier organization and submits legal/contact, tax, payout, endpoint, data-residency, model, price, and security-questionnaire evidence. Payout accounts and supplier credentials are encrypted with the existing AES-256-GCM vault; APIs return only masked metadata and last-four values. Model and price applications remain evidence until an administrator explicitly reviews them.

The supplier can move its own application from `DRAFT` to `SUBMITTED` or request `EXIT_REQUESTED`. It cannot set KYB, contract, `APPROVED`, `SUSPENDED`, or `EXITED` status. A database trigger rejects non-administrator status transitions, and administrator review writes the status, review row, status event, and audit row in one transaction. Approval is fail-closed unless KYB is `VERIFIED`, the contract is `ACTIVE`, at least one endpoint is both `VERIFIED` and `PASSED`, and an approved security questionnaire exists.

Approval of an application does not automatically create a production route. A separate administrator action must associate a reviewed supplier with an existing Provider and configure the Provider's commercial contract, resale permission, allowed regions, data-processing regions, credential, model, and price evidence. The data-plane admission gate still requires `COMMERCIAL_APPROVED`, resale `APPROVED`, valid dates, enabled state, no kill switch, region/data residency compatibility, current margin, and an eligible credential.

The association also starts the platform-controlled quality ramp from its
configured basis-point cap. Supplier-declared uptime, output quality, regions,
and prices remain declarations and cannot advance the ramp. Only independent
platform probes/traffic, official price verification, and required-region
coverage can change grade, weight, circuit, or ramp state.

## Endpoint security

Endpoint creation requires HTTPS, no userinfo/query/fragment, and port 443. DNS answers are checked for public global-unicast addresses; loopback, private, link-local, multicast, unspecified, and special addresses are rejected. Verification disables process proxy inheritance and redirects, caps the request, pins a public address, and repeats DNS/public-IP validation immediately before dialing. The endpoint must return the SHA-256 challenge value for the one-time challenge token in the `X-ModelDock-Challenge` header at `/.well-known/modeldock-endpoint-verification`.

This check is an application-layer SSRF control. Production deployments must also run the application in a network namespace/security group that cannot reach PostgreSQL, Redis, metadata services, or other private control-plane networks. Do not set the existing Provider mock-network exception for supplier endpoints.

## Operations and rollback

Apply migration 0020 with the normal migration runner, then verify the `schema_migrations` checksum and that supplier status protection triggers exist. Monitor audit actions beginning with `supplier.` and alert on repeated endpoint isolation failures, status suspensions, or exit requests.

Rollback is application-first: stop exposing supplier UI/routes and run the previous application image while retaining migration 0020 and all evidence. Do not delete migration rows or supplier evidence during a routine rollback. A destructive schema rollback requires stopping all writers, exporting supplier applications, encrypted-secret metadata, endpoint checks, questionnaires, reviews, status events, and audit rows, then dropping dependent tables/triggers in reverse order under a separately approved change. Existing Provider and `/v1` data must be preserved.
