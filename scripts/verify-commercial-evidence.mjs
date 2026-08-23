import { createHash, verify as verifySignature } from "node:crypto";
import { readFileSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import Ajv2020 from "../tools/commercial-evidence/node_modules/ajv/dist/2020.js";
import addFormats from "../tools/commercial-evidence/node_modules/ajv-formats/dist/index.js";

const expectedGates = {
  software_license: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Owner", "Legal"]],
  legal_entity: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Owner", "Legal"]],
  terms_of_service: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Legal"]],
  privacy_policy: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Legal"]],
  refund_policy: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Legal", "Finance"]],
  payment_provider_agreement: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Finance"]],
  provider_commercial_rights: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Commercial", "Legal"]],
  provider_regions: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Commercial", "Legal"]],
  production_smtp: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Operations"]],
  managed_pitr_restore: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Operations"]],
  production_failover: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Operations"]],
  independent_security_assessment: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["IndependentTester", "Security"]],
  current_vulnerability_scan: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Security"]],
  finance_signoff: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Finance"]],
  operations_signoff: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Operations"]],
  security_signoff: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Security"]],
  legal_signoff: [["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"], ["Legal"]],
  supplier_kyb: [["MARKETPLACE_PRODUCTION"], ["Legal", "Finance"]],
  supplier_contract: [["MARKETPLACE_PRODUCTION"], ["Commercial", "Legal"]],
  endpoint_ownership: [["MARKETPLACE_PRODUCTION"], ["Security"]],
  provider_quality_probe: [["MARKETPLACE_PRODUCTION"], ["Operations"]],
  canary_traffic: [["MARKETPLACE_PRODUCTION"], ["Operations"]],
  production_payout: [["MARKETPLACE_PRODUCTION"], ["Finance"]],
  tax_and_invoice: [["MARKETPLACE_PRODUCTION"], ["Finance", "Legal"]],
  supplier_bill_reconciliation: [["MARKETPLACE_PRODUCTION"], ["Finance"]],
  marketplace_dispute_drill: [["MARKETPLACE_PRODUCTION"], ["Finance", "Operations"]],
  payout_idempotency: [["MARKETPLACE_PRODUCTION"], ["Finance", "Operations"]],
  marketplace_second_admin: [["MARKETPLACE_PRODUCTION"], ["Owner"]],
  supplier_exit_drill: [["MARKETPLACE_PRODUCTION"], ["Commercial", "Operations"]]
};

function parseArgs(argv) {
  const parsed = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    if (!key?.startsWith("--") || argv[index + 1] === undefined) throw new Error(`Invalid argument near ${key ?? "<end>"}`);
    parsed[key.slice(2)] = argv[index + 1];
  }
  return parsed;
}

function loadJson(file) {
  return JSON.parse(readFileSync(file, "utf8"));
}

function sameSet(actual, expected) {
  return actual.length === expected.length && [...actual].sort().every((value, index) => value === [...expected].sort()[index]);
}

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  }
  return value;
}

function signedPayload(attestation) {
  const payload = { ...attestation };
  delete payload.signature;
  return Buffer.from(JSON.stringify(canonical(payload)), "utf8");
}

function safeEvidencePath(root, reference) {
  if (isAbsolute(reference)) throw new Error("evidence_reference must be relative to the controlled evidence root");
  const rootPath = resolve(root);
  const candidate = resolve(rootPath, reference);
  const rel = relative(rootPath, candidate);
  if (rel === "" || rel.startsWith("..") || isAbsolute(rel)) throw new Error("evidence_reference escapes the controlled evidence root");
  return candidate;
}

function hashFile(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function trustEntry(policy, issuer, role, now) {
  const entry = policy.trusted_issuers.find((candidate) => candidate.issuer === issuer && candidate.roles?.includes(role));
  if (!entry) throw new Error(`issuer ${issuer} is not allowlisted for role ${role}`);
  if (entry.not_before && Date.parse(entry.not_before) > now) throw new Error("issuer trust is not active yet");
  if (entry.not_after && Date.parse(entry.not_after) <= now) throw new Error("issuer trust has expired");
  if (!entry.public_key_pem?.includes("BEGIN PUBLIC KEY")) throw new Error("trusted issuer has no Ed25519 public key");
  return entry;
}

function verifySigned(attestation, context, policy, evidenceRoot, expectedRole, isRuntime = false) {
  const now = context.now;
  const issued = Date.parse(isRuntime ? attestation.generated_at : attestation.approved_at);
  const expires = Date.parse(attestation.expires_at);
  if (!Number.isFinite(issued) || issued > now) throw new Error("attestation is future-dated");
  if (!Number.isFinite(expires) || expires <= now || expires <= issued) throw new Error("attestation is expired or has an invalid validity window");
  if (attestation.repository !== context.repository) throw new Error("repository mismatch");
  if ((isRuntime ? attestation.commit : attestation.reviewed_commit) !== context.commit) throw new Error("commit mismatch");
  if ((isRuntime ? attestation.tree : attestation.reviewed_tree) !== context.tree) throw new Error("tree mismatch");
  if (attestation.version !== context.version) throw new Error("version mismatch");
  if ((isRuntime ? attestation.database_migration : attestation.migration_version) !== context.migration) throw new Error("migration mismatch");
  if (attestation.release_profile !== context.profile) throw new Error("release profile mismatch");
  if (attestation.issuer_role !== expectedRole) throw new Error(`role mismatch: expected ${expectedRole}`);
  const trusted = trustEntry(policy, attestation.issuer, expectedRole, now);
  const validSignature = verifySignature(null, signedPayload(attestation), trusted.public_key_pem, Buffer.from(attestation.signature, "base64"));
  if (!validSignature) throw new Error("invalid Ed25519 signature");
  if (!isRuntime) {
    const evidencePath = safeEvidencePath(evidenceRoot, attestation.evidence_reference);
    if (hashFile(evidencePath) !== attestation.evidence_sha256) throw new Error("evidence SHA-256 mismatch");
  }
}

function verifyTestEvidence(evidence, validate, context, requireReleaseCandidate) {
  if (!validate(evidence)) throw new Error(`test evidence schema: ${JSON.stringify(validate.errors)}`);
  for (const [field, expected] of [["repository", context.repository], ["commit_sha", context.commit], ["tree_sha", context.tree], ["version", context.version], ["migration_version", context.migration]]) {
    if (evidence[field] !== expected) throw new Error(`${field} mismatch`);
  }
  if (evidence.status !== "PASS") throw new Error("commercial integration status is not PASS");
  if (Date.parse(evidence.started_at) > Date.parse(evidence.completed_at) || Date.parse(evidence.completed_at) > context.now) throw new Error("test evidence time window is invalid");
  if (evidence.gateway_tested_server_digest !== evidence.image_digests.server) throw new Error("server digest tested by Gateway does not match candidate");
  for (const component of ["server", "admin", "console"]) {
    const digest = evidence.image_digests[component];
    if (evidence.build_digests[component] !== digest) throw new Error(`${component} build digest mismatch (possible rebuild)`);
    if (evidence.candidate_tag_digests[component] !== digest) throw new Error(`${component} candidate tag points to another digest`);
    for (const [kind, records] of [["scanner", evidence.security_scans], ["SBOM", evidence.sboms], ["provenance", evidence.provenance]]) {
      if (records[component].image_digest !== digest) throw new Error(`${component} ${kind} binds another digest`);
      if (records[component].commit !== context.commit) throw new Error(`${component} ${kind} binds another commit`);
      if (requireReleaseCandidate && records[component].status !== "PASS") throw new Error(`${component} ${kind} is NOT RUN`);
    }
  }
  if (requireReleaseCandidate && evidence.evidence_level !== "RELEASE_CANDIDATE") throw new Error("formal commercial release requires RELEASE_CANDIDATE evidence");
}

function verifyRuntime(attestation, context, policy, testEvidence) {
  verifySigned(attestation, context, policy, context.evidenceRoot, "Operations", true);
  if (!testEvidence) throw new Error("runtime attestation cannot be checked without exact candidate evidence");
  for (const component of ["server", "admin", "console"]) {
    if (attestation.image_digests[component] !== testEvidence.image_digests[component]) throw new Error(`${component} runtime digest mismatch`);
  }
  if (!attestation.payment_adapter.production_ready || ["sandbox", "manual_transfer"].includes(attestation.payment_adapter.type)) throw new Error("production payment adapter is not ready");
  if (!attestation.smtp.verified) throw new Error("production SMTP verification is absent");
  if (attestation.commercially_approved_providers.length < 1) throw new Error("no commercially approved Provider exists");
  for (const provider of attestation.commercially_approved_providers) {
    if (Date.parse(provider.contract_valid_from) > context.now || Date.parse(provider.contract_valid_to) <= context.now) throw new Error("Provider contract window is not currently valid");
  }
  if (context.profile === "MARKETPLACE_PRODUCTION") {
    if (!attestation.payout_adapter.production_ready || attestation.payout_adapter.type === "sandbox") throw new Error("production payout adapter is not ready");
    if (attestation.production_ready_suppliers.length < 1) throw new Error("no production-ready supplier exists");
  }
}

const args = parseArgs(process.argv.slice(2));
for (const required of ["manifest", "schema", "test-schema", "trust-schema", "trust-policy", "profile", "repository", "commit", "tree", "version", "migration", "evidence-root", "output"]) {
  if (!args[required]) throw new Error(`Missing --${required}`);
}

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const manifestSchema = loadJson(args.schema);
const testSchema = loadJson(args["test-schema"]);
const trustSchema = loadJson(args["trust-schema"]);
const validateManifest = ajv.compile(manifestSchema);
const validateTest = ajv.compile(testSchema);
const validateTrust = ajv.compile(trustSchema);
const manifest = loadJson(args.manifest);
const policy = loadJson(args["trust-policy"]);
const results = [];
const add = (id, title, passed, detail, metadata = {}) => results.push({ id, title, result: passed ? "PASS" : "BLOCKED", detail, ...metadata });

const trustSchemaValid = validateTrust(policy);
add("trust_policy_schema", "Trusted issuer policy schema", trustSchemaValid, trustSchemaValid ? `issuers=${policy.trusted_issuers.length}` : JSON.stringify(validateTrust.errors));
if (args["require-trust-anchor"] === "true") {
  const actualTrustHash = createHash("sha256").update(readFileSync(args["trust-policy"])).digest("hex");
  const expectedTrustHash = args["trust-policy-sha256"];
  add("trust_policy_anchor", "Out-of-repository trusted issuer policy anchor", Boolean(expectedTrustHash) && expectedTrustHash === actualTrustHash,
    expectedTrustHash ? `actual=${actualTrustHash}; expected=${expectedTrustHash}` : "MODELDOCK_TRUST_POLICY_SHA256 is not configured");
}

if (!validateManifest(manifest)) {
  add("manifest_schema", "Commercial evidence manifest schema", false, JSON.stringify(validateManifest.errors));
} else {
  add("manifest_schema", "Commercial evidence manifest schema", true, `schema_version=${manifest.schema_version}; gates=${manifest.gates.length}`);
}

let catalogValid = validateManifest(manifest);
if (catalogValid) {
  const ids = manifest.gates.map((gate) => gate.id);
  catalogValid = ids.length === new Set(ids).size && Object.keys(expectedGates).length === ids.length;
  for (const gate of manifest.gates) {
    const expected = expectedGates[gate.id];
    catalogValid &&= Boolean(expected) && sameSet(gate.profiles, expected[0]) && sameSet(gate.required_roles, expected[1]);
  }
  catalogValid &&= Object.keys(expectedGates).every((id) => ids.includes(id));
}
add("mandatory_gate_catalog", "Mandatory gate IDs, profiles, and roles", catalogValid, catalogValid ? "exact gate set v1" : "deleted, renamed, duplicate, unknown, or weakened gate");

const context = {
  profile: args.profile,
  repository: args.repository,
  commit: args.commit,
  tree: args.tree,
  version: args.version,
  migration: args.migration,
  evidenceRoot: args["evidence-root"],
  now: args.now ? Date.parse(args.now) : Date.now()
};

let testEvidence;
if (args["test-evidence"]) {
  try {
    testEvidence = loadJson(args["test-evidence"]);
    verifyTestEvidence(testEvidence, validateTest, context, args.profile !== "ENGINEERING_PREVIEW");
    add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", true, `commit=${context.commit}; server=${testEvidence.image_digests.server}`);
  } catch (error) {
    add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", false, error.message);
  }
} else if (args.profile !== "ENGINEERING_PREVIEW") {
  add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", false, "evidence is missing");
}

if (args.profile !== "ENGINEERING_PREVIEW" && validateManifest(manifest)) {
  const runtime = manifest.runtime.attestations.find((item) => item.release_profile === args.profile);
  try {
    if (!runtime) throw new Error("signed Runtime Attestation is missing");
    verifyRuntime(runtime, context, policy, testEvidence);
    add("runtime_attestation", "Signed target-environment Runtime Attestation", true, `issuer=${runtime.issuer}; expires=${runtime.expires_at}`, { signature_status: "VALID", expires_at: runtime.expires_at, commit: runtime.commit, digests: runtime.image_digests });
  } catch (error) {
    add("runtime_attestation", "Signed target-environment Runtime Attestation", false, error.message, { signature_status: runtime ? "INVALID" : "MISSING" });
  }

  for (const gate of manifest.gates.filter((item) => item.profiles.includes(args.profile))) {
    const roleErrors = [];
    const used = [];
    for (const role of gate.required_roles) {
      const attestation = gate.attestations.find((item) => item.release_profile === args.profile && item.issuer_role === role);
      try {
        if (!attestation) throw new Error(`${role} attestation is missing`);
        if (attestation.gate_id !== gate.id) throw new Error("attestation was copied from another gate");
        verifySigned(attestation, context, policy, context.evidenceRoot, role, false);
        used.push(attestation);
      } catch (error) {
        roleErrors.push(`${role}: ${error.message}`);
      }
    }
    const passed = roleErrors.length === 0;
    const first = used[0];
    add(gate.id, gate.title, passed, passed ? `roles=${gate.required_roles.join(",")}; issuer=${used.map((item) => item.issuer).join(",")}` : roleErrors.join("; "), {
      source: first?.evidence_reference ?? "none",
      signature_status: passed ? "VALID" : used.length ? "PARTIAL" : "MISSING",
      commit: first?.reviewed_commit ?? "",
      expires_at: first?.expires_at ?? ""
    });
  }
}

const output = { schema_version: "2.0.0", profile: context.profile, repository: context.repository, commit: context.commit, tree: context.tree, version: context.version, migration: context.migration, results };
await import("node:fs").then(({ writeFileSync }) => writeFileSync(args.output, `${JSON.stringify(output, null, 2)}\n`, "utf8"));
if (results.some((result) => result.result === "BLOCKED")) process.exitCode = 1;
