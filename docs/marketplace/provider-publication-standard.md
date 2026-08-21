# Marketplace Provider publication standard

Policy version: `marketplace-launch-2026-08-21`

## Publication identity

Each listing identifies exactly one operator-owned Provider row and controlled HTTPS endpoint. The Provider has explicit contract, resale, allowed/prohibited customer regions, processing regions, retention policy, credential ownership, cost/rate limits, settlement currency, disabled switch, and emergency kill switch. Endpoint redirects, DNS, IP ranges, TLS, ownership challenge, and tenant isolation are reviewed before canary.

## Models and prices

Every published model maps an approved supplier model application to an enabled platform model. Capability, context, safety, data-region, deprecation, and version information must be accurate. Every price maps an approved supplier application to an approved platform cost book and a current independent `MATCH` verification from an official API, official document, or contract invoice.

Prices and money are exact decimal strings with an explicit three-letter currency and integer unit. Listing JSON and legacy model scores are declarations only; the current approved platform books determine charging and margin.

## Quality and canary

The platform quality policy defines probe model, regions, sample minimum, latency, availability, error/429, throughput, output-quality, price-truth, ramp, downweight, and circuit thresholds. Foundation gates must pass before `CANARY`. Canary traffic is limited to 1–2,000 basis points and remains subject to deterministic platform ramp admission.

Production `ACTIVE` requires all 23 versioned gates and a different approving administrator. User call, charge, commission, payable, refund, settlement, reconciliation, and dispute gates use platform database evidence. Suspension, emergency cutover, and exit gates require referenced operator drill reports.

Each review stores a SHA-256 fingerprint of Provider ID, normalized endpoint,
supported-model JSON, listing-price JSON, and publication metadata. PostgreSQL rejects canary or active
material changes that no longer match that fingerprint; changes require a new
revision even though listing prices remain declarations.

## Changes and retirement

Endpoint, owner, contract, model, material capability, price, region, retention, subprocesser, or credential changes require re-verification and may require a new release revision. Critical security or legal changes immediately suspend eligibility. Deprecation must follow the signed notice period, preserve compatible `/v1` error behavior, and provide a tested traffic-removal plan.
