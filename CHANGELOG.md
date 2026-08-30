# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Added Migration 0026 for official enterprise/BYOK/manual Provider account
  bindings and replay-safe provisioning/allocation jobs; consumer signup,
  CAPTCHA, trial farming, and shared consumer sessions remain unsupported.
- Added Migration 0027 with Workspace Provider policy, request-level routing,
  ordered BYOK/shared capacity, free-model daily admission, BYOK shadow spend,
  append-only Provider capability documents, encrypted OIDC configuration, and
  organization-scoped SCIM resource links.
- Added ordered cross-model/Provider fallback, `auto:free`, price/privacy/
  region/capability routing gates, routing explanation headers and funding
  snapshots, plus Console/Admin management surfaces.
- Isolated provisioned platform-managed credentials to their owning
  organization/member and made `auto:free` require an exact zero total quote
  while remaining usable by active zero-balance prepaid wallets.
- Added public minimum-sample Provider quality metrics and region-filtered
  capability declarations while preserving the boundary between Provider
  declarations and platform measurements.

- Replaced self-declared commercial and runtime fields with signed Evidence
  Attestation V2 and target-environment Runtime Attestation V2.
- Added repository-locked Draft 2020-12 schema validation, mandatory Gate ID /
  profile / role catalogs, and 44 signature, schema, runtime, and same-digest
  negative scenarios.
- Added Migration 0025 with append-only Attestation verification metadata,
  database-derived Runtime readiness views, and a transaction-aborting
  commercial Decimal integrity scan.
- Made Go-live documents output-only and moved exact Commit/Tree/Workflow/Image
  identity into clean-checkout CI artifacts.
- Reordered formal release execution so Server, Admin, and Console candidates
  are built once, scanned and attested, tested by immutable digest, and promoted
  without rebuilding.
- Changed Decimal arithmetic and comparison to return errors, added strict
  `ParseDecimal`, restricted `MustDecimal` to constants/tests, and made money
  database scans and funding settlement fail on invalid values.

### Security

- Updated the server runtime OpenSSL packages to Alpine 3.22's fixed
  `3.5.8-r0` release for CVE-2026-14456.
- Added private-network-safe OIDC discovery, encrypted client-secret storage,
  display-once hashed SCIM tokens, organization-domain restrictions, last-owner
  protection, and tenant-scoped SCIM User/Team deprovisioning.
- Added request and Workspace controls that may tighten but cannot relax ZDR,
  data-collection, processing-region, fallback, shared-capacity, or price limits.

- Added Ed25519 signature verification, trusted issuer/role allowlists anchored
  by an out-of-repository policy hash, controlled evidence SHA verification,
  expiry/future-date checks, and exact Commit/Tree binding.
- Kept all external commercial gates and runtime claims fail-closed; no legal,
  license, payment, Provider, supplier, tax, security, or operations approval
  is asserted by this change.
- Moved signed External/Runtime Attestations out of the source Manifest and
  into an exact-commit GitHub Actions Artifact Bundle, eliminating a second
  source-Commit self-reference.

## [3.0.0-beta.1] - 2026-08-21

### Changed

- Migrated legacy budgets, catalog prices, usage estimates, reports, API
  responses, and CSV exports to exact `NUMERIC(30,12)`-compatible decimals.
- Added fail-closed commercial and marketplace evidence gates, exact-commit
  test binding, full SemVer prerelease validation, and mandatory commercial CI.
- Revalidated migrations 20–24 and separated engineering readiness from legal,
  payment, Provider, supplier, and production-operation approval.

### Security

- Prevented sandbox payment or payout adapters, expired or future-dated
  approvals, stale test reports, and approvals for another commit from
  satisfying a commercial gate.

## [2.0.0] - 2026-08-10

### Added

- ModelDock multi-project gateway, administration, usage, wallet, audit, and
  OpenAI-compatible API baseline with RelayDock compatibility.
- Required pull-request CI for Go, both web applications, database migrations,
  Docker/Compose, secret scanning, and dependency vulnerability scanning.
- Gated semantic release workflow with immutable commit tags, OCI source
  metadata, SPDX SBOMs, image vulnerability scans, provenance attestations,
  and an optional keyless signing interface.
- Repository contribution, security, release, ownership, issue, pull-request,
  licensing-decision, and naming-compatibility governance.

### Security

- Pinned application image build stages by digest and made build version,
  commit SHA, and commit timestamp auditable through image labels and the
  version endpoint.
- Upgraded frontend build/router dependencies to versions without known npm
  audit findings at the time of this release engineering baseline.
- Upgraded the pinned Go toolchain and reachable Go dependencies to remove the
  HIGH/CRITICAL findings detected by govulncheck and final-image scanning.

[Unreleased]: https://github.com/Yangjunjie-Lin/ModelDock/compare/v3.0.0-beta.1...HEAD
[3.0.0-beta.1]: https://github.com/Yangjunjie-Lin/ModelDock/releases/tag/v3.0.0-beta.1
[2.0.0]: https://github.com/Yangjunjie-Lin/ModelDock/releases/tag/v2.0.0
