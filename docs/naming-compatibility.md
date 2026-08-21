# ModelDock and RelayDock naming compatibility

## Decision

ModelDock is the product and release brand. RelayDock remains a compatibility
identifier for existing technical surfaces in the current major version.
This is deliberate because the canonical Git remote is still
`github.com/Yangjunjie-Lin/RelayDock` and the Go module is
`github.com/relayedock/relayedock`. Changing the module to a repository path
that does not exist would make source provenance and `go install` behavior
misleading.

## Compatibility matrix

| Surface | Current value | Policy |
| --- | --- | --- |
| Product/UI/release name | ModelDock | Canonical for new material |
| Go module/import path | `github.com/relayedock/relayedock` | Retained until a real canonical repository and migration release exist |
| Command, binary, Compose service | `relaydock` | Retained for scripts, volumes, health checks, and operator automation |
| Existing image defaults | `relaydock/*` | Retained; production Compose accepts new `MODELDOCK_*_IMAGE` repository overrides |
| API keys | `rdk_live_*`, `rdk_test_*` | Stable; no reissue required |
| Runtime environment | `RELAYDOCK_*` | Stable; no rename or fallback precedence change |
| Existing API identity fields | `RelayDock` / `relaydock` | Stable; additive `product: ModelDock` and build metadata are allowed |
| `/v1` behavior | OpenAI-compatible | Stable |

The release workflow publishes canonical ModelDock image repository names and
also records the full commit SHA. It does not rename persisted data, Redis key
prefixes, log event names, or database identifiers.

## Future module-path migration prerequisites

A module rename requires a separate compatibility prompt and release. It may
proceed only after all of these are true:

1. the canonical ModelDock repository and organization exist;
2. release tags and provenance resolve to that repository;
3. the old repository/module publishes a maintained forwarding notice or
   compatibility release for `go install` consumers;
4. documentation and automation provide an explicit transition window;
5. all internal imports, source links, SBOM identifiers, and signing identities
   move atomically; and
6. `/v1`, `rdk_*`, `RELAYDOCK_*`, database fields, and operational aliases stay
   compatible unless a separately approved major-version plan says otherwise.

Until then, retaining the Go module is the lower-risk and more truthful option.
