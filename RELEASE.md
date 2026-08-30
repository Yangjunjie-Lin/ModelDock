# ModelDock release process

Release workflows run only from an annotated Semantic Version 2.0 Tag such as
`v3.0.0-beta.2`. A Tag push is always a source-only Engineering Preview;
commercial profiles require an explicit manual dispatch against that Tag.
Manually created GitHub Releases and mutable `latest` image tags are not part of
the supported process.

## Commercial release blockers

All of the following must be resolved before dispatching a commercial profile:

1. The target commit is on the protected default branch and its required CI
   checks pass.
2. `VERSION` and `internal/version.Current` equal the tag without the leading
   `v`.
3. `CHANGELOG.md` has a dated entry for that exact version.
4. `docs/licensing-decision.md` records an approved owner decision. The current
   `blocked` status intentionally prevents a formal release.
5. Empty-database and populated V1 upgrade migrations pass.
6. Go, frontend, Compose, Docker, secret, dependency, vulnerability, exact-money,
   and all commercial/Marketplace integration gates pass with no bypass.
7. `scripts/verify-commercial-readiness.ps1` passes the selected profile using
   evidence bound to the exact commit, latest migration, and immutable image
   digests. The current external evidence intentionally keeps commercial
   profiles at `NO-GO`.

## Versioning and changelog

ModelDock uses Semantic Versioning:

- MAJOR: an approved breaking change, including removal of a deprecated
  compatibility surface;
- MINOR: backward-compatible capability;
- PATCH: backward-compatible correction or security fix.

Update `CHANGELOG.md` using Keep a Changelog categories. Do not move an item
out of `Unreleased` until its release commit is final. Run:

```powershell
pwsh ./scripts/verify-release.ps1 -Version 3.0.0-beta.2
```

The workflow accepts `ENGINEERING_PREVIEW`, `COMMERCIAL_BETA`, and
`MARKETPLACE_PRODUCTION`. Engineering Preview uploads a source-only artifact
explicitly marked non-production and does not push images or create a GitHub
Release. Commercial profiles build unique candidate Digests first, then stop
before promotion unless the same-Digest tests/scans and every signed
External/Runtime Artifact Gate pass. Prerelease SemVer values create GitHub
Prereleases; stable SemVer values create stable releases.

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
- requires the final decision job to run in the reviewed commercial-beta or
  marketplace-production GitHub Environment;
- attaches the generated Commercial, Go-live, Security, Financial, and Image
  Digest reports to the GitHub Release.

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
