import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(import.meta.dirname, "../..");
const cache = resolve(root, ".cache/release-gate-tests-v2");
const evidenceRoot = resolve(cache, "evidence");
mkdirSync(evidenceRoot, { recursive: true });
const sourceManifest = JSON.parse(readFileSync(resolve(root, "release/commercial-gates.json"), "utf8"));
const commit = "1".repeat(40);
const tree = "2".repeat(40);
const migration = "0025_commercial_attestation_and_decimal_hardening";
const digest = (character) => `sha256:${character.repeat(64)}`;
const now = new Date("2026-08-24T12:00:00.000Z");
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
    certificate_identity: "fixture-identity",
    certificate_issuer: "fixed-ed25519-test-ca",
    not_before: "2026-01-01T00:00:00Z",
    not_after: "2027-01-01T00:00:00Z"
  }]
};

function clone(value) { return structuredClone(value); }
function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  }
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
    schema_version: "2.0.0",
    gate_id: gate.id,
    release_profile: "COMMERCIAL_BETA",
    repository: "Yangjunjie-Lin/ModelDock",
    reviewed_commit: commit,
    reviewed_tree: tree,
    version: "3.0.0-beta.1",
    migration_version: migration,
    evidence_reference: reference,
    evidence_sha256: evidenceFile(reference),
    issuer: "fixture-authority",
    issuer_role: role,
    approved_at: approved,
    expires_at: expires,
    workflow_run_id: "123",
    workflow_run_attempt: "1",
    signature_type: "ed25519",
    signature: "",
    certificate_identity: "fixture-identity",
    certificate_issuer: "fixed-ed25519-test-ca"
  });
}
function runtimeAttestation() {
  return signObject({
    schema_version: "2.0.0",
    release_profile: "COMMERCIAL_BETA",
    repository: "Yangjunjie-Lin/ModelDock",
    environment_id: "prod-fixture",
    commit_sha: commit,
    tree_sha: tree,
    version: "3.0.0-beta.1",
    migration_version: migration,
    server_image_digest: digest("a"),
    admin_image_digest: digest("b"),
    console_image_digest: digest("c"),
    payment_adapter: { type: "contracted_acquirer", production_ready: true },
    payout_adapter: { type: "contracted_payout", production_ready: true },
    smtp: {
      provider_id_hash: "3".repeat(64),
      delivery_verified: true,
      delivery_proof_sha256: "4".repeat(64)
    },
    commercially_approved_providers: [{
      id_hash: "5".repeat(64),
      commercial_status: "COMMERCIAL_APPROVED",
      resale_approved: true,
      contract_valid_from: "2026-01-01T00:00:00Z",
      contract_valid_to: "2027-01-01T00:00:00Z",
      customer_region_approved: true,
      data_processing_region_approved: true,
      current_price_verified: true,
      kill_switch_enabled: false
    }],
    production_ready_suppliers: [],
    database_query_summary: {
      commercially_approved_provider_count: 1,
      production_ready_supplier_count: 0,
      invalid_decimal_rows: 0,
      summary_sha256: "6".repeat(64)
    },
    configuration_summary: {
      sha256: "7".repeat(64),
      redacted_keys: [
        "PAYMENT_ADAPTER", "PAYMENT_PRODUCTION_READY", "PAYOUT_ADAPTER",
        "PAYOUT_PRODUCTION_READY", "SMTP_PROVIDER"
      ]
    },
    generated_at: approved,
    expires_at: expires,
    workflow_run_id: "123",
    workflow_run_attempt: "1",
    issuer: "fixture-authority",
    issuer_role: "Operations",
    signature_type: "ed25519",
    signature: "",
    certificate_identity: "fixture-identity",
    certificate_issuer: "fixed-ed25519-test-ca"
  });
}
function validBundle(manifest) {
  const external = [];
  for (const gate of manifest.gates.filter((item) => item.profiles.includes("COMMERCIAL_BETA"))) {
    for (const role of gate.required_roles) external.push(externalAttestation(gate, role));
  }
  return {
    schema_version: "2.0.0",
    repository: "Yangjunjie-Lin/ModelDock",
    reviewed_commit: commit,
    reviewed_tree: tree,
    version: "3.0.0-beta.1",
    migration_version: migration,
    evidence_origin: "GITHUB_ACTIONS_ARTIFACT",
    external_attestations: external,
    runtime_attestations: [runtimeAttestation()]
  };
}
function validEvidence() {
  const check = (imageDigest) => ({ status: "PASS", image_digest: imageDigest, commit });
  const suiteIds = [
    "verify-migrations", "verify-accounts", "verify-pricing", "verify-funding", "verify-payments",
    "verify-subscriptions", "verify-financial-close", "verify-commercial-onboarding",
    "verify-supplier-settlement", "verify-marketplace-launch", "verify-release-metadata",
    "verify-exact-money", "verify-release-negative-tests"
  ];
  return {
    schema_version: "2.0.0",
    evidence_level: "RELEASE_CANDIDATE",
    evidence_origin: "GITHUB_ACTIONS_ARTIFACT",
    repository: "Yangjunjie-Lin/ModelDock",
    commit_sha: commit,
    tree_sha: tree,
    branch_or_tag: "tag/v3.0.0-beta.1",
    version: "3.0.0-beta.1",
    migration_version: migration,
    workflow_run_id: "123",
    workflow_run_attempt: "1",
    server_image_digest: digest("a"),
    admin_image_digest: digest("b"),
    console_image_digest: digest("c"),
    started_at: approved,
    completed_at: now.toISOString(),
    suite_results: suiteIds.map((id) => ({ id, status: "PASS" })),
    status: "PASS",
    gateway_tested_server_digest: digest("a"),
    build_digests: { server: digest("a"), admin: digest("b"), console: digest("c") },
    security_scans: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    sboms: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    provenance: { server: check(digest("a")), admin: check(digest("b")), console: check(digest("c")) },
    candidate_tags: { server: "candidate-123-1", admin: "candidate-123-1", console: "candidate-123-1" },
    candidate_tag_digests: { server: digest("a"), admin: digest("b"), console: digest("c") }
  };
}

function findAttestation(bundle, gate, role) {
  return bundle.external_attestations.find((item) => item.gate_id === gate && item.issuer_role === role);
}

function run(name, mutate, expected) {
  const manifest = clone(sourceManifest);
  const bundle = validBundle(manifest);
  const evidence = validEvidence();
  const trust = clone(policy);
  mutate?.({ manifest, bundle, evidence, trust });
  const prefix = name.replaceAll(/[^a-z0-9]+/gi, "-").toLowerCase();
  const manifestPath = resolve(cache, `${prefix}-manifest.json`);
  const bundlePath = resolve(cache, `${prefix}-bundle.json`);
  const evidencePath = resolve(cache, `${prefix}-evidence.json`);
  const trustPath = resolve(cache, `${prefix}-trust.json`);
  const outputPath = resolve(cache, `${prefix}-output.json`);
  writeFileSync(manifestPath, JSON.stringify(manifest));
  writeFileSync(bundlePath, JSON.stringify(bundle));
  writeFileSync(evidencePath, JSON.stringify(evidence));
  writeFileSync(trustPath, JSON.stringify(trust));
  const result = spawnSync(process.execPath, [
    resolve(root, "scripts/verify-commercial-evidence.mjs"),
    "--manifest", manifestPath,
    "--schema", resolve(root, "release/commercial-gates.schema.json"),
    "--attestation-schema", resolve(root, "release/commercial-attestation-bundle.schema.json"),
    "--test-schema", resolve(root, "release/commercial-test-evidence.schema.json"),
    "--trust-schema", resolve(root, "release/trusted-attestation-issuers.schema.json"),
    "--trust-policy", trustPath,
    "--profile", "COMMERCIAL_BETA",
    "--repository", "Yangjunjie-Lin/ModelDock",
    "--commit", commit,
    "--tree", tree,
    "--version", "3.0.0-beta.1",
    "--migration", migration,
    "--evidence-root", evidenceRoot,
    "--test-evidence", evidencePath,
    "--attestation-bundle", bundlePath,
    "--workflow-run-id", "123",
    "--workflow-run-attempt", "1",
    "--branch-or-tag", "tag/v3.0.0-beta.1",
    "--trust-policy-sha256", createHash("sha256").update(readFileSync(trustPath)).digest("hex"),
    "--now", now.toISOString(),
    "--output", outputPath
  ], { encoding: "utf8" });
  const output = JSON.parse(readFileSync(outputPath, "utf8"));
  const text = JSON.stringify(output);
  if (expected === "PASS") {
    if (result.status !== 0 || output.results.some((item) => item.result !== "PASS")) {
      throw new Error(`${name} should pass: ${text}; stderr=${result.stderr}`);
    }
  } else if (result.status === 0 || !text.includes(expected)) {
    throw new Error(`${name} did not fail with ${expected}: status=${result.status}; ${text}; stderr=${result.stderr}`);
  }
}

run("valid signed evidence chain", null, "PASS");

run("deleted gate", ({ manifest }) => manifest.gates.pop(), "manifest_schema");
run("empty gate array", ({ manifest }) => { manifest.gates = []; }, "minItems");
run("duplicate ID", ({ manifest }) => { manifest.gates[1].id = manifest.gates[0].id; }, "mandatory_gate_catalog");
run("unknown field", ({ manifest }) => { manifest.gates[0].unknown = true; }, "additionalProperties");
run("missing required field", ({ manifest }) => { delete manifest.gates[0].title; }, "required");
run("wrong profile", ({ manifest }) => { manifest.gates[0].profiles = ["ENGINEERING_PREVIEW"]; }, "enum");
run("wrong schema version", ({ manifest }) => { manifest.schema_version = 1; }, "const");
run("unknown gate", ({ manifest }) => { manifest.gates[0].id = "unknown_gate"; }, "enum");
run("deleted Provider Contract gate", ({ manifest }) => {
  manifest.gates = manifest.gates.filter((gate) => gate.id !== "provider_commercial_rights");
}, "manifest_schema");
run("deleted Payment gate", ({ manifest }) => {
  manifest.gates = manifest.gates.filter((gate) => gate.id !== "payment_provider_agreement");
}, "manifest_schema");
run("deleted Security gate", ({ manifest }) => {
  manifest.gates = manifest.gates.filter((gate) => gate.id !== "independent_security_assessment");
}, "manifest_schema");

run("wrong date", ({ bundle }) => {
  findAttestation(bundle, "software_license", "Owner").approved_at = "yesterday";
}, "format");
run("wrong SHA", ({ bundle }) => {
  findAttestation(bundle, "software_license", "Owner").evidence_sha256 = "not-a-sha";
}, "pattern");
run("wrong Runtime type", ({ bundle }) => {
  bundle.runtime_attestations[0].payment_adapter.production_ready = "yes";
}, "type");
run("wrong bundle schema version", ({ bundle }) => { bundle.schema_version = "1"; }, "const");

run("forged 64 digit hash", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.evidence_sha256 = "f".repeat(64); signObject(a);
}, "evidence SHA-256 mismatch");
run("nonexistent evidence", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.evidence_reference = "missing.txt"; signObject(a);
}, "referenced evidence does not exist");
run("wrong signature", ({ bundle }) => {
  findAttestation(bundle, "software_license", "Owner").signature = "A".repeat(86) + "==";
}, "invalid Ed25519 signature");
run("untrusted approver", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.issuer = "intruder"; signObject(a);
}, "not allowlisted");
run("wrong role", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.issuer_role = "Finance"; signObject(a);
}, "Owner attestation is missing");
run("wrong certificate identity", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.certificate_identity = "other-identity"; signObject(a);
}, "certificate identity");
run("wrong commit", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.reviewed_commit = "9".repeat(40); signObject(a);
}, "commit mismatch");
run("wrong tree", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.reviewed_tree = "9".repeat(40); signObject(a);
}, "tree mismatch");
run("expired approval", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.expires_at = "2026-08-24T11:59:00Z"; signObject(a);
}, "expired");
run("future approval", ({ bundle }) => {
  const a = findAttestation(bundle, "software_license", "Owner");
  a.approved_at = "2026-08-25T12:00:00Z";
  a.expires_at = "2026-08-26T12:00:00Z";
  signObject(a);
}, "future-dated");
run("copied Gate Attestation", ({ bundle }) => {
  const index = bundle.external_attestations.findIndex((item) =>
    item.gate_id === "legal_entity" && item.issuer_role === "Owner");
  bundle.external_attestations[index] = clone(findAttestation(bundle, "software_license", "Owner"));
}, "Owner attestation is missing");

run("sandbox payment", ({ bundle }) => {
  const runtime = bundle.runtime_attestations[0];
  runtime.payment_adapter.type = "sandbox"; signObject(runtime);
}, "payment adapter is not ready");
run("manual payment", ({ bundle }) => {
  const runtime = bundle.runtime_attestations[0];
  runtime.payment_adapter.type = "manual_transfer"; signObject(runtime);
}, "payment adapter is not ready");
run("count only Runtime forgery", ({ bundle }) => {
  const runtime = bundle.runtime_attestations[0];
  runtime.commercially_approved_providers = [];
  signObject(runtime);
}, "no database-derived commercially approved Provider");
run("invalid Provider contract window", ({ bundle }) => {
  const runtime = bundle.runtime_attestations[0];
  runtime.commercially_approved_providers[0].contract_valid_to = "2026-01-02T00:00:00Z";
  signObject(runtime);
}, "contract window");
run("Runtime wrong digest", ({ bundle }) => {
  const runtime = bundle.runtime_attestations[0];
  runtime.server_image_digest = digest("d"); signObject(runtime);
}, "server Runtime Attestation digest mismatch");

run("server digest mismatch", ({ evidence }) => {
  evidence.gateway_tested_server_digest = digest("d");
}, "Gateway does not match");
run("admin digest missing", ({ evidence }) => { delete evidence.admin_image_digest; }, "required");
run("console digest missing", ({ evidence }) => { delete evidence.console_image_digest; }, "required");
run("commercial test binds other digest", ({ evidence }) => {
  evidence.gateway_tested_server_digest = digest("d");
}, "Gateway does not match");
run("Trivy binds other digest", ({ evidence }) => {
  evidence.security_scans.server.image_digest = digest("d");
}, "scanner binds another digest");
run("SBOM binds other commit", ({ evidence }) => {
  evidence.sboms.admin.commit = "8".repeat(40);
}, "SBOM binds another commit");
run("candidate tag replaced", ({ evidence }) => {
  evidence.candidate_tag_digests.console = digest("d");
}, "candidate tag points to another digest");
run("same label points to new digest", ({ evidence }) => {
  evidence.candidate_tag_digests.server = digest("d");
}, "candidate tag points to another digest");
run("Release Evidence missing component", ({ evidence }) => {
  delete evidence.provenance.console;
}, "required");
run("missing required suite", ({ evidence }) => {
  evidence.suite_results = evidence.suite_results.filter((suite) => suite.id !== "verify-payments");
}, "required suite verify-payments is missing");
run("NOT RUN suite", ({ evidence }) => {
  evidence.suite_results[0].status = "NOT_RUN";
}, "FAIL or NOT_RUN");
run("wrong workflow run", ({ evidence }) => { evidence.workflow_run_id = "999"; }, "workflow run identity");
run("local file as release evidence", ({ evidence }) => {
  evidence.evidence_origin = "LOCAL_DEVELOPMENT";
}, "official readiness requires a GitHub Actions Artifact");

console.log("PASS commercial Evidence Chain V2 schema, signature, Runtime, exact-commit, and same-digest negative tests (44 scenarios).");
