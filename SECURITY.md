# Security policy

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability and do not include
secrets, customer data, or exploit data in normal logs. Use the repository's
[private security advisory form](https://github.com/Yangjunjie-Lin/ModelDock/security/advisories/new).
If that channel is unavailable, contact the repository owner through the
private contact method shown on the GitHub profile and request an encrypted
reporting channel before sending sensitive details.

Include the affected commit or version, impact, minimum reproduction, and any
suggested mitigation. Use synthetic credentials and data. The maintainer will
coordinate validation, remediation, disclosure timing, and CVE assignment
where appropriate.

## Supported versions

Until the first formal ModelDock release, only the current `main` branch is
supported. After release, the latest minor line receives security fixes;
older lines are unsupported unless a release note explicitly states otherwise.

## Security release requirements

A formal release is blocked unless CI, migration tests, secret scanning,
dependency checks, image vulnerability scans, SBOM generation, and provenance
attestation succeed. The licensing decision is an additional non-security
release blocker. Signing is optional and keyless by default; enabling it must
not weaken the other gates.

Runtime invariants and operator responsibilities are documented in
`docs/security.md`. Provider credentials must be authorized by their owner and
must never be used to bypass provider contract, account, geographic, or rate
limits.
