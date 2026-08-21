# ModelDock release process

Formal releases are created only by `.github/workflows/release.yml` from an
annotated semantic-version tag such as `v2.0.0`. Manually created GitHub
Releases and mutable `latest` image tags are not part of the supported process.

## Release blockers

All of the following must be resolved before tagging:

1. The target commit is on the protected default branch and its required CI
   checks pass.
2. `internal/version.Current` equals the tag without the leading `v`.
3. `CHANGELOG.md` has a dated entry for that exact version.
4. `docs/licensing-decision.md` records an approved owner decision. The current
   `blocked` status intentionally prevents a formal release.
5. Empty-database and populated V1 upgrade migrations pass.
6. Go, frontend, Compose, Docker, secret, dependency, and vulnerability gates
   pass with no permitted bypass.

## Versioning and changelog

ModelDock uses Semantic Versioning:

- MAJOR: an approved breaking change, including removal of a deprecated
  compatibility surface;
- MINOR: backward-compatible capability;
- PATCH: backward-compatible correction or security fix.

Update `CHANGELOG.md` using Keep a Changelog categories. Do not move an item
out of `Unreleased` until its release commit is final. Run:

```powershell
pwsh ./scripts/verify-release.ps1 -Version 2.0.0
```

## Automated release artifacts

After the reusable CI workflow succeeds, the release workflow:

- builds the server, admin, and console images for `linux/amd64` from the exact
  tag commit;
- sets OCI version, full Git commit SHA, repository, and deterministic commit
  timestamp labels;
- first pushes a unique `candidate-<run>-<attempt>` reference, then completes
  every scan, SBOM, provenance, and optional signature against that digest;
- promotes only those verified digests to `sha-<40-character-commit>` and
  semantic-version tags, refusing any conflicting existing tag;
- attaches BuildKit SBOM/provenance attestations and GitHub build provenance;
- emits downloadable SPDX JSON SBOMs;
- scans each final image for HIGH and CRITICAL vulnerabilities before creating
  the GitHub Release;
- optionally signs each image digest with keyless Cosign when the repository
  variable `MODELDOCK_SIGN_IMAGES` is exactly `true`.

The promotion and GitHub Release are the last jobs. If CI, build, SBOM,
provenance, signing, or scanning fails, no formal tag promotion or Release is
performed. An idempotent retry may reuse an existing formal tag only when it
already resolves to the exact verified digest. Registry administrators must
also enable tag immutability for semantic and `sha-*` tags and configure
retention for candidate tags. Consumers should deploy by digest, not by a
moving tag.

Reproducibility is evaluated on the canonical platform image manifest with
`SOURCE_DATE_EPOCH` and timestamp rewriting. Provenance and SBOM attestations
are detached evidence about a particular build run and may themselves contain
run-specific metadata; they do not change the verified platform filesystem.

## Rollback

Application rollback means selecting the prior verified image digest. Database
schema is forward-only: never delete migration ledger rows or edit released
SQL. Follow the migration-specific rollback notes and restore a verified
backup only when forward repair is impossible. Preserve audit, request, usage,
and wallet ledger data.
