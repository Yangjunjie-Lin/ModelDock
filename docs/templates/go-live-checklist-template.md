# Go-live Checklist Template

> **NOT RELEASE EVIDENCE**

This file documents the shape of the generated Artifact only. A valid report
is created by the commercial-readiness verifier after Schema, exact-commit
tests, Runtime Attestation, external signed Attestations, financial
reconciliation, image scans, security Gates, and same-Digest checks.

Required identity fields:

- repository, full Commit SHA, actual Git Tree SHA;
- branch or Tag, Version, latest Migration;
- Workflow Run ID and attempt;
- immutable Server/Admin/Console image Digests;
- generated time and every Gate result;
- Attestation source, issuer role, signature status, and expiry.

Any BLOCKED or NOT RUN prerequisite yields NO-GO. Hand-writing a GO decision
in this template grants no authority.
