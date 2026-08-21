# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
