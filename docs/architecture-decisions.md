# Architecture decisions

Each decision applies to V1 unless superseded by a later numbered record.

## ADR-001: modular monolith with two listeners

**Status:** Accepted

RelayDock is built as one Go binary with a gateway listener on port 8080 and a
control-plane listener on port 8081. Packages enforce domain boundaries.

This provides atomic deployment and a small operational footprint while still
allowing separate reverse-proxy policies and network exposure. A later split is
possible if profiling demonstrates a real scaling or isolation need.

## ADR-002: official provider APIs and authorized credentials only

**Status:** Accepted, non-negotiable

V1 integrates only OpenAI's documented public API and only credentials an
administrator is authorized to possess. Consumer web sessions, account
registration, CAPTCHA handling, trial acquisition, browser identity
manipulation, and evasive proxy rotation are outside the domain model and API.

## ADR-003: Responses is primary; Chat Completions remains compatible

**Status:** Accepted

New examples lead with `/v1/responses`. `/v1/chat/completions` remains available
because official SDKs and existing applications rely on it. Both use provider
adapter methods and preserve public wire shapes; neither is implemented through
private web endpoints.

## ADR-004: PostgreSQL is authoritative; Redis is disposable coordination

**Status:** Accepted

Users, providers, credentials, groups, routes, API keys, pricing, usage, logs,
and audits live in PostgreSQL. Redis stores rate-limit windows, concurrency
counters, short cooldown state, locks, and caches. Redis can be rebuilt without
losing authoritative configuration.

## ADR-005: distinct cryptographic purposes and keys

**Status:** Accepted

`RELAYDOCK_MASTER_KEY` encrypts provider secrets,
`RELAYDOCK_API_KEY_HMAC_SECRET` hashes RelayDock API keys, and
`RELAYDOCK_JWT_SECRET` signs control-plane sessions. Key reuse is forbidden.
Downstream keys are never encrypted for recovery because plaintext recovery is
not a product requirement.

## ADR-006: explainable priority/weight/load scheduling

**Status:** Accepted

Scheduling must expose a stable reason object and use atomic concurrency
leases. Opaque machine-learning selection is rejected for V1. Health,
cooldown, priority, weight, and active load are sufficient and auditable.

## ADR-007: failover is bounded by replay safety

**Status:** Accepted

A different credential may be tried only before response bytes are emitted and
only for a failure classified as transient and replay-safe. No failover occurs
after an SSE stream begins, for authentication failures, or to evade an
upstream policy/limit. V1 takes the stricter implementation: a fallback group
may be selected only when the primary group has no eligible credential, before
an upstream attempt; it never replays the same request after an upstream
attempt has started.

## ADR-008: inference and organization administration are separate

**Status:** Accepted

Inference credentials belong to schedulable credential groups. Any future
OpenAI Admin API credential is a distinct management resource, is never placed
in a route, and receives only the minimum organization scope needed.

## ADR-009: prompt content logging is off by default

**Status:** Accepted

Request logs store metadata, usage, error classification, timing, and route
decisions. `LOG_PROMPT_CONTENT=false` is the V1 default. Future content logging
requires explicit governance, retention, access control, and user notice.

## ADR-010: project-local bind mounts

**Status:** Accepted

Compose uses `./data/postgres`, `./data/redis`, and `./logs`, not anonymous or
Docker-managed named volumes. This satisfies the Windows D-drive placement
requirement, makes backup scope visible, and avoids silently writing durable
project state to Docker's default C-drive volume location.

## ADR-011: production containers run without root privileges

**Status:** Accepted

Application and static web images have explicit unprivileged users, drop Linux
capabilities, enable `no-new-privileges`, and use read-only root filesystems
where practical. Official database/cache images run under their service users.

## ADR-012: deterministic offline provider mock

**Status:** Accepted

An optional Compose profile provides a small deterministic mock of Models,
Responses, Chat Completions, streaming, and Embeddings. It tests wire
compatibility without a real provider key or billable request. It is clearly
labeled non-production and implements no account or administration functions.

## ADR-013: API specification documents stable RelayDock behavior

**Status:** Accepted

`docs/openapi.yaml` specifies authentication, common errors, RelayDock control
resources, and the compatible inference entry points. OpenAI-compatible bodies
remain intentionally extensible so additive official fields can pass through
without requiring a RelayDock release.
