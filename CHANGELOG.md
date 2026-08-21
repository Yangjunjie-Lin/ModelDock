# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Yangjunjie-Lin/RelayDock/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/Yangjunjie-Lin/RelayDock/releases/tag/v2.0.0
