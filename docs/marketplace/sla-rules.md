# Marketplace SLA rules

Policy version: `marketplace-launch-2026-08-21`

## Authoritative measurements

SLA results use only ModelDock platform traffic, scheduled health probes, synthetic quality probes, region probes, and independently captured official-price evidence. The observation window, sample minimum, regions, thresholds, and probe model are administrator-owned. Supplier dashboards and declarations are not ranking or SLA inputs.

The measured signals are availability, non-429 error rate, 429 rate, first-token latency, full-response latency, output throughput, sampled output quality, price truth, and required-region coverage. All rollups and SLA events are timestamped and append-only.

## Grades and response

| State | Operational response |
|---|---|
| Grade A/B, circuit closed | Continue within the current ramp cap |
| Grade C | Downweight and hold or reduce ramp while investigating |
| Grade D/F | Strong downweight; open an SLA event |
| Circuit open | Stop new attempts; preserve in-flight and audit evidence |
| Region or price-truth failure | Remove the affected region/model from eligibility |

The configured policy is the contractual threshold schedule for a supplier. Changing thresholds requires an audit event; changing a production supplier's material SLA requires a new policy version or order-form amendment.

## Incidents

Critical incidents include confirmed cross-customer data exposure, invalid endpoint ownership, systemic authentication failure, sustained unavailability, or materially false price evidence. ModelDock may issue an emergency cutover immediately. The supplier must acknowledge through the contracted incident channel, provide containment evidence, and complete a root-cause report on the timetable in the signed order form.

Recovery moves from open to half-open and then closed only after platform-measured recovery thresholds are met. An administrator reset cannot force the circuit closed.

## Credits and exclusions

Any credit is calculated from platform-measured affected eligible traffic and the signed commercial schedule. Planned maintenance counts as excluded only when it was approved, announced within the contracted notice period, and did not exceed the approved window. Customer misuse, ModelDock-originated failures, and force majeure are excluded only when evidence identifies the cause and affected interval.
