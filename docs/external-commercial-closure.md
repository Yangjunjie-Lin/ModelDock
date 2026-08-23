# External commercial closure

This runbook is the hand-off boundary between repository engineering and real
commercial approval. A coding agent may maintain templates, validators, and
instructions, but cannot approve any row. Sensitive contracts, identity/KYB
documents, merchant or bank credentials, tax files, legal opinions, and
penetration-test bodies stay in the controlled evidence system and must not be
committed. CI receives only the controlled evidence export needed to recompute
its SHA-256; the repository stores the signed Attestation and opaque reference.

| Gate | 所需真实负责人 | 所需证据 | 系统录入方式 | 当前状态 |
| --- | --- | --- | --- | --- |
| License | Owner + Legal | 许可证决定与第三方 License Inventory | Signed Attestation | BLOCKED |
| Legal Entity | Owner + Legal | 主体注册与授权 | Signed Attestation | BLOCKED |
| Terms/Privacy/Refund | Legal + Finance | 最终批准文本 | Signed Attestation | BLOCKED |
| Payment | Finance | 商户协议、Sandbox/Production 验证、结算与拒付 | Runtime + Signed Attestation | BLOCKED |
| Provider | Commercial + Legal | 商业分发权、地区与数据处理条款 | Provider Attestation | BLOCKED |
| SMTP | Operations | 域名、投递、退信和声誉验证 | Runtime Attestation | BLOCKED |
| PITR/Failover | Operations | Managed Restore 与 RPO/RTO Drill | Signed Attestation | BLOCKED |
| Security | Independent Tester + Security | 渗透测试与问题处置 | Signed Attestation | BLOCKED |
| Supplier | Legal + Finance + Security | KYB、合同、税务、Payout | Supplier Attestation | BLOCKED |

## Controlled closure procedure

1. The accountable owner places the real evidence in an access-controlled
   evidence store and exports the exact evidence object into the CI-controlled
   evidence directory. The `evidence_reference` is a relative opaque export
   name, not a secret URL or credential.
2. The responsible role calculates the evidence SHA-256 and signs Evidence
   Attestation V2 with an Ed25519 private key held outside the repository, or
   supplies an equivalent GitHub Artifact Attestation/Sigstore identity that a
   future schema version explicitly supports.
3. A security administrator separately adds the issuer public key and allowed
   roles to `release/trusted-attestation-issuers.json` through protected review.
   The private key is never placed in Git, Actions variables, logs, or images.
4. Operations generates Runtime Attestation V2 from the target environment.
   It contains only digests, booleans, counts derived from the listed masked
   IDs, and hashed configuration/query summaries—never credentials, contract
   bodies, personal details, merchant secrets, or payout destinations.
5. Protected CI validates Schema, required Gate IDs/roles, evidence existence
   and hash, issuer allowlist, signature, validity window, exact Commit/Tree,
   Version/Migration, Workflow Run ID, and all three candidate image digests.
6. `docs/go-live-checklist.md` is never an input. The validator computes the
   Decision and writes the exact report artifact. Any absent, invalid, expired,
   future-dated, stale, wrong-role, or wrong-digest item remains `BLOCKED`.

Current `trusted_issuers` and every Attestation array are empty. This is the
intentional fail-closed state until real accountable parties complete the work.

