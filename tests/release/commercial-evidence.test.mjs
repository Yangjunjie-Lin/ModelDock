import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(import.meta.dirname, "../..");
const cache = resolve(root, ".cache/release-gate-tests-v2");
const evidenceRoot = resolve(cache, "evidence");
mkdirSync(evidenceRoot, { recursive: true });
const sourceManifest = JSON.parse(readFileSync(resolve(root, "release/commercial-gates.yaml"), "utf8"));
const commit = "1".repeat(40);
const tree = "2".repeat(40);
const digest = (character) => `sha256:${character.repeat(64)}`;
const now = new Date("2026-08-23T12:00:00.000Z");
const approved = new Date(now.getTime() - 60_000).toISOString();
const expires = new Date(now.getTime() + 86_400_000).toISOString();
const roles = ["Owner", "Legal", "Finance", "Commercial", "Security", "Operations", "IndependentTester"];
const { publicKey, privateKey } = generateKeyPairSync("ed25519");
const policy = {
  schema_version: 1,
  repository: "Yangjunjie-Lin/ModelDock",
  trusted_issuers: [{
    issuer: "fixture-authority",
    roles,
    public_key_pem: publicKey.export({ type: "spki", format: "pem" }),
    not_before: "2026-01-01T00:00:00Z",
    not_after: "2027-01-01T00:00:00Z"
  }]
};
const policyPath = resolve(cache, "trust.json");
writeFileSync(policyPath, JSON.stringify(policy));

function clone(value) { return structuredClone(value); }
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  return value;
}
function signObject(value) {
  const payload = { ...value };
  delete payload.signature;
  value.signature = sign(null, Buffer.from(JSON.stringify(canonical(payload))), privateKey).toString("base64");
  return value;
}
function evidenceFile(name, body = name) {
  writeFileSync(resolve(evidenceRoot, name), body);
  return createHash("sha256").update(body).digest("hex");
}
function externalAttestation(gate, role) {
  const reference = `${gate.id}-${role}.txt`;
  return signObject({
    schema_version: "2.0.0", gate_id: gate.id, release_profile: "COMMERCIAL_BETA",
    repository: "Yangjunjie-Lin/ModelDock", reviewed_commit: commit, reviewed_tree: tree,
    version: "3.0.0-beta.1", migration_version: "0024_exact_money_and_release_evidence",
    evidence_reference: reference, evidence_sha256: evidenceFile(reference), issuer: "fixture-authority",
    issuer_role: role, approved_at: approved, expires_at: expires, workflow_run_id: "123",
    signature: "", signature_type: "ed25519"
  });
}
function runtimeAttestation() {
  return signObject({
    schema_version: "2.0.0", release_profile: "COMMERCIAL_BETA", repository: "Yangjunjie-Lin/ModelDock",
    deployment_environment_id: "prod-fixture", commit, tree, version: "3.0.0-beta.1",
    image_digests: { server: digest("a"), admin: digest("b"), console: digest("c") },
    configuration_sha256: "3".repeat(64), payment_adapter: { type: "contracted_acquirer", production_ready: true },
    payout_adapter: { type: "contracted_payout", production_ready: true }, smtp: { provider_id_hash: "4".repeat(64), verified: true },
    commercially_approved_providers: [{ id_hash: "5".repeat(64), commercial_status: "COMMERCIAL_APPROVED", resale_approved: true,
      contract_valid_from: "2026-01-01T00:00:00Z", contract_valid_to: "2027-01-01T00:00:00Z", region_approved: true,
      data_processing_region_approved: true, current_price_verified: true, kill_switch_enabled: false }],
    production_ready_suppliers: [], database_migration: "0024_exact_money_and_release_evidence", database_query_sha256: "6".repeat(64),
    generated_at: approved, expires_at: expires, workflow_run_id: "123", issuer: "fixture-authority", issuer_role: "Operations",
    signature: "", signature_type: "ed25519"
  });
}
function validManifest() {
  const manifest = clone(sourceManifest);
  manifest.runtime.attestations = [runtimeAttestation()];
  for (const gate of manifest.gates.filter((item) => item.profiles.includes("COMMERCIAL_BETA"))) {
    gate.attestations = gate.required_roles.map((role) => externalAttestation(gate, role));
  }
  return manifest;
}
function validEvidence() {
  const check = (imageDigest) => ({ status: "PASS", image_digest: imageDigest, commit });
  return {
    schema_version: "2.0.0", evidence_level: "RELEASE_CANDIDATE", repository: "Yangjunjie-Lin/ModelDock", commit_sha: commit,
    tree_sha: tree, migration_version: "0024_exact_money_and_release_evidence", version: "3.0.0-beta.1", workflow_run_id: "123",
    ref_type: "tag", ref_name: "v3.0.0-beta.1", image_digests: { server: digest("a"), admin: digest("b"), console: digest("c") },
    gateway_tested_server_digest: digest("a"), build_digests: { server: digest("a"), admin: digest("b"), console: digest("c") },
    security_scans: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    sboms: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    provenance: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    candidate_tags: { server: "candidate-123-1", admin: "candidate-123-1", console: "candidate-123-1" },
    candidate_tag_digests: { server: digest("a"), admin: digest("b"), console: digest("c") },
    started_at: approved, completed_at: now.toISOString(), status: "PASS", suites: ["verify-migrations"]
  };
}

function run(name, mutate, expected, mutateEvidence = null, mutatePolicy = null) {
  const manifest = validManifest();
  const evidence = validEvidence();
  const trust = clone(policy);
  mutate?.(manifest);
  mutateEvidence?.(evidence);
  mutatePolicy?.(trust);
  const prefix = name.replaceAll(/[^a-z0-9]+/gi, "-").toLowerCase();
  const manifestPath = resolve(cache, `${prefix}-manifest.json`);
  const evidencePath = resolve(cache, `${prefix}-evidence.json`);
  const trustPath = resolve(cache, `${prefix}-trust.json`);
  const outputPath = resolve(cache, `${prefix}-output.json`);
  writeFileSync(manifestPath, JSON.stringify(manifest));
  writeFileSync(evidencePath, JSON.stringify(evidence));
  writeFileSync(trustPath, JSON.stringify(trust));
  const result = spawnSync(process.execPath, [resolve(root, "scripts/verify-commercial-evidence.mjs"),
    "--manifest", manifestPath, "--schema", resolve(root, "release/commercial-gates.schema.json"),
    "--test-schema", resolve(root, "release/commercial-test-evidence.schema.json"), "--trust-schema", resolve(root, "release/trusted-attestation-issuers.schema.json"), "--trust-policy", trustPath,
    "--profile", "COMMERCIAL_BETA", "--repository", "Yangjunjie-Lin/ModelDock", "--commit", commit, "--tree", tree,
    "--version", "3.0.0-beta.1", "--migration", "0024_exact_money_and_release_evidence", "--evidence-root", evidenceRoot,
    "--test-evidence", evidencePath, "--now", now.toISOString(), "--output", outputPath], { encoding: "utf8" });
  const output = JSON.parse(readFileSync(outputPath, "utf8"));
  const text = JSON.stringify(output);
  if (expected === "PASS") {
    if (result.status !== 0 || output.results.some((item) => item.result !== "PASS")) throw new Error(`${name} should pass: ${text}`);
  } else if (result.status === 0 || !text.includes(expected)) {
    throw new Error(`${name} did not fail with ${expected}: status=${result.status}; ${text}; stderr=${result.stderr}`);
  }
}

run("valid signed evidence chain", null, "PASS");
run("deleted gate", (m) => m.gates.pop(), "manifest_schema");
run("unknown field", (m) => { m.gates[0].unknown = true; }, "additionalProperties");
run("wrong profile", (m) => { m.gates[0].profiles = ["ENGINEERING_PREVIEW"]; }, "enum");
run("wrong time", (m) => { m.gates[0].attestations[0].approved_at = "yesterday"; }, "format");
run("duplicate ID", (m) => { m.gates[1].id = m.gates[0].id; }, "mandatory_gate_catalog");
run("wrong SHA syntax", (m) => { m.gates[0].attestations[0].evidence_sha256 = "not-a-sha"; }, "pattern");
run("missing Runtime", (m) => { delete m.runtime; }, "required");
run("wrong schema version", (m) => { m.schema_version = 1; }, "const");
run("unknown gate", (m) => { m.gates[0].id = "unknown_gate"; }, "enum");

run("forged 64 digit hash", (m) => { const a=m.gates[0].attestations[0]; a.evidence_sha256="f".repeat(64); signObject(a); }, "evidence SHA-256 mismatch");
run("nonexistent evidence", (m) => { const a=m.gates[0].attestations[0]; a.evidence_reference="missing.txt"; signObject(a); }, "ENOENT");
run("wrong signature", (m) => { m.gates[0].attestations[0].signature = "A".repeat(86) + "=="; }, "invalid Ed25519 signature");
run("untrusted approver", (m) => { const a=m.gates[0].attestations[0]; a.issuer="intruder"; signObject(a); }, "not allowlisted");
run("wrong role", (m) => { const a=m.gates[0].attestations[0]; a.issuer_role="Finance"; signObject(a); }, "attestation is missing");
run("wrong commit", (m) => { const a=m.gates[0].attestations[0]; a.reviewed_commit="9".repeat(40); signObject(a); }, "commit mismatch");
run("wrong tree", (m) => { const a=m.gates[0].attestations[0]; a.reviewed_tree="9".repeat(40); signObject(a); }, "tree mismatch");
run("expired approval", (m) => { const a=m.gates[0].attestations[0]; a.expires_at="2026-08-23T11:59:00Z"; signObject(a); }, "expired");
run("future approval", (m) => { const a=m.gates[0].attestations[0]; a.approved_at="2026-08-24T12:00:00Z"; a.expires_at="2026-08-25T12:00:00Z"; signObject(a); }, "future-dated");
run("copied gate attestation", (m) => { const target=m.gates[1]; target.attestations[0]=clone(m.gates[0].attestations[0]); }, "copied from another gate");

run("sandbox payment", (m) => { const r=m.runtime.attestations[0]; r.payment_adapter.type="sandbox"; signObject(r); }, "payment adapter is not ready");
run("manual payment", (m) => { const r=m.runtime.attestations[0]; r.payment_adapter.type="manual_transfer"; signObject(r); }, "payment adapter is not ready");
run("count only runtime forgery", (m) => { const r=m.runtime.attestations[0]; r.commercially_approved_providers=[]; r.commercially_approved_provider_count=1; signObject(r); }, "additionalProperties");
run("invalid provider contract window", (m) => { const r=m.runtime.attestations[0]; r.commercially_approved_providers[0].contract_valid_to="2026-01-02T00:00:00Z"; signObject(r); }, "contract window");

run("server digest mismatch", null, "Gateway does not match", (e) => { e.gateway_tested_server_digest=digest("d"); });
run("admin digest missing", null, "required", (e) => { delete e.image_digests.admin; });
run("console digest missing", null, "required", (e) => { delete e.image_digests.console; });
run("digest changed after rebuild", null, "build digest mismatch", (e) => { e.build_digests.server=digest("d"); });
run("scanner binds other digest", null, "scanner binds another digest", (e) => { e.security_scans.server.image_digest=digest("d"); });
run("SBOM binds other commit", null, "SBOM binds another commit", (e) => { e.sboms.admin.commit="8".repeat(40); });
run("candidate tag points elsewhere", null, "candidate tag points to another digest", (e) => { e.candidate_tag_digests.console=digest("d"); });

console.log("PASS commercial evidence V2 schema, signature, runtime, exact-commit, and same-digest negative tests (31 scenarios).");
