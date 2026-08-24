import { createHash, verify as verifySignature } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import Ajv2020 from "../tools/commercial-evidence/node_modules/ajv/dist/2020.js";
import addFormats from "../tools/commercial-evidence/node_modules/ajv-formats/dist/index.js";

const BOTH = ["COMMERCIAL_BETA", "MARKETPLACE_PRODUCTION"];
const expectedGates = {
  software_license: [BOTH, ["Owner", "Legal"]],
  legal_entity: [BOTH, ["Owner", "Legal"]],
  terms_of_service: [BOTH, ["Legal"]],
  privacy_policy: [BOTH, ["Legal"]],
  refund_policy: [BOTH, ["Finance", "Legal"]],
  payment_provider_agreement: [BOTH, ["Finance", "Legal"]],
  provider_commercial_rights: [BOTH, ["Commercial", "Legal"]],
  provider_regions: [BOTH, ["Commercial", "Legal"]],
  production_smtp: [BOTH, ["Operations"]],
  managed_pitr_restore: [BOTH, ["Operations"]],
  production_failover: [BOTH, ["Operations"]],
  independent_security_assessment: [BOTH, ["IndependentTester", "Security"]],
  current_vulnerability_scan: [BOTH, ["Security"]],
  finance_signoff: [BOTH, ["Finance"]],
  operations_signoff: [BOTH, ["Operations"]],
  security_signoff: [BOTH, ["Security"]],
  legal_signoff: [BOTH, ["Legal"]],
  supplier_kyb: [["MARKETPLACE_PRODUCTION"], ["Commercial", "Legal"]],
  supplier_contract: [["MARKETPLACE_PRODUCTION"], ["Commercial", "Legal"]],
  endpoint_ownership: [["MARKETPLACE_PRODUCTION"], ["Security"]],
  provider_quality_probe: [["MARKETPLACE_PRODUCTION"], ["Operations"]],
  canary_traffic: [["MARKETPLACE_PRODUCTION"], ["Operations"]],
  production_payout: [["MARKETPLACE_PRODUCTION"], ["Finance", "Legal"]],
  tax_and_invoice: [["MARKETPLACE_PRODUCTION"], ["Finance", "Legal"]],
  supplier_bill_reconciliation: [["MARKETPLACE_PRODUCTION"], ["Finance"]],
  marketplace_dispute_drill: [["MARKETPLACE_PRODUCTION"], ["Finance", "Operations"]],
  payout_idempotency: [["MARKETPLACE_PRODUCTION"], ["Finance", "Operations"]],
  marketplace_second_admin: [["MARKETPLACE_PRODUCTION"], ["Owner"]],
  supplier_exit_drill: [["MARKETPLACE_PRODUCTION"], ["Commercial", "Operations"]],
  final_go_live: [BOTH, ["Owner", "Legal", "Finance", "Security", "Operations"]]
};

const requiredSuites = [
  "verify-migrations", "verify-accounts", "verify-pricing", "verify-funding", "verify-payments",
  "verify-subscriptions", "verify-financial-close", "verify-commercial-onboarding",
  "verify-supplier-settlement", "verify-marketplace-launch", "verify-release-metadata",
  "verify-exact-money", "verify-release-negative-tests"
];

function parseArgs(argv) {
  const parsed = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    if (!key?.startsWith("--") || argv[index + 1] === undefined) {
      throw new Error(`Invalid argument near ${key ?? "<end>"}`);
    }
    parsed[key.slice(2)] = argv[index + 1];
  }
  return parsed;
}

function loadJson(file) {
  return JSON.parse(readFileSync(file, "utf8"));
}

function sameSet(actual, expected) {
  const left = [...actual].sort();
  const right = [...expected].sort();
  return left.length === right.length && left.every((value, index) => value === right[index]);
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
  if (rel === "" || rel.startsWith("..") || isAbsolute(rel)) {
    throw new Error("evidence_reference escapes the controlled evidence root");
  }
  return candidate;
}

function hashFile(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function trustEntry(policy, attestation, role, now) {
  const entry = policy.trusted_issuers.find((candidate) =>
    candidate.issuer === attestation.issuer && candidate.roles?.includes(role));
  if (!entry) throw new Error(`issuer ${attestation.issuer} is not allowlisted for role ${role}`);
  if (Date.parse(entry.not_before) > now) throw new Error("issuer trust is not active yet");
  if (Date.parse(entry.not_after) <= now) throw new Error("issuer trust has expired");
  if (attestation.certificate_identity !== entry.certificate_identity ||
      attestation.certificate_issuer !== entry.certificate_issuer) {
    throw new Error("certificate identity or issuer is not allowlisted");
  }
  if (!entry.public_key_pem?.includes("BEGIN PUBLIC KEY")) {
    throw new Error("trusted issuer has no Ed25519 public key");
  }
  return entry;
}

function verifySigned(attestation, context, policy, expectedRole, isRuntime = false) {
  const issued = Date.parse(isRuntime ? attestation.generated_at : attestation.approved_at);
  const expires = Date.parse(attestation.expires_at);
  if (!Number.isFinite(issued) || issued > context.now) throw new Error("attestation is future-dated");
  if (!Number.isFinite(expires) || expires <= context.now || expires <= issued) {
    throw new Error("attestation is expired or has an invalid validity window");
  }
  if (attestation.repository !== context.repository) throw new Error("repository mismatch");
  if ((isRuntime ? attestation.commit_sha : attestation.reviewed_commit) !== context.commit) {
    throw new Error("commit mismatch");
  }
  if ((isRuntime ? attestation.tree_sha : attestation.reviewed_tree) !== context.tree) {
    throw new Error("tree mismatch");
  }
  if (attestation.version !== context.version) throw new Error("version mismatch");
  if (attestation.migration_version !== context.migration) throw new Error("migration mismatch");
  if (attestation.release_profile !== context.profile) throw new Error("release profile mismatch");
  if (attestation.issuer_role !== expectedRole) throw new Error(`role mismatch: expected ${expectedRole}`);
  const trusted = trustEntry(policy, attestation, expectedRole, context.now);
  const valid = verifySignature(
    null,
    signedPayload(attestation),
    trusted.public_key_pem,
    Buffer.from(attestation.signature, "base64")
  );
  if (!valid) throw new Error("invalid Ed25519 signature");
  if (!isRuntime) {
    const evidencePath = safeEvidencePath(context.evidenceRoot, attestation.evidence_reference);
    if (!existsSync(evidencePath)) throw new Error("referenced evidence does not exist");
    if (hashFile(evidencePath) !== attestation.evidence_sha256) {
      throw new Error("evidence SHA-256 mismatch");
    }
  }
}

function componentDigests(evidence) {
  return {
    server: evidence.server_image_digest,
    admin: evidence.admin_image_digest,
    console: evidence.console_image_digest
  };
}

function verifyTestEvidence(evidence, validate, context, formalRelease) {
  if (!validate(evidence)) throw new Error(`test evidence schema: ${JSON.stringify(validate.errors)}`);
  for (const [field, expected] of [
    ["repository", context.repository], ["commit_sha", context.commit], ["tree_sha", context.tree],
    ["version", context.version], ["migration_version", context.migration]
  ]) {
    if (evidence[field] !== expected) throw new Error(`${field} mismatch`);
  }
  if (evidence.status !== "PASS") throw new Error("commercial integration status is not PASS");
  if (evidence.suite_results.some((suite) => suite.status !== "PASS")) {
    throw new Error("commercial integration contains FAIL or NOT_RUN suites");
  }
  const suites = new Set(evidence.suite_results.map((suite) => suite.id));
  for (const suite of requiredSuites) {
    if (!suites.has(suite)) throw new Error(`required suite ${suite} is missing`);
  }
  if (Date.parse(evidence.started_at) > Date.parse(evidence.completed_at) ||
      Date.parse(evidence.completed_at) > context.now) {
    throw new Error("test evidence time window is invalid");
  }
  if (evidence.evidence_origin !== "GITHUB_ACTIONS_ARTIFACT") {
    throw new Error("official readiness requires a GitHub Actions Artifact; local evidence is development-only");
  }
  if (formalRelease && evidence.evidence_level !== "RELEASE_CANDIDATE") {
    throw new Error("formal release requires GitHub Actions RELEASE_CANDIDATE evidence");
  }
  if (!formalRelease && !["ENGINEERING_CI", "RELEASE_CANDIDATE"].includes(evidence.evidence_level)) {
    throw new Error("Engineering Preview requires protected ENGINEERING_CI evidence");
  }
  {
    if (evidence.workflow_run_id !== context.workflowRunId ||
        evidence.workflow_run_attempt !== context.workflowRunAttempt) {
      throw new Error("workflow run identity does not belong to the current repository run");
    }
    if (evidence.branch_or_tag !== context.branchOrTag) throw new Error("branch or tag mismatch");
  }
  const digests = componentDigests(evidence);
  if (evidence.gateway_tested_server_digest !== digests.server) {
    throw new Error("server digest tested by Gateway does not match candidate");
  }
  for (const component of ["server", "admin", "console"]) {
    const digest = digests[component];
    if (evidence.build_digests[component] !== digest) {
      throw new Error(`${component} build digest mismatch (possible rebuild)`);
    }
    if (evidence.candidate_tag_digests[component] !== digest) {
      throw new Error(`${component} candidate tag points to another digest`);
    }
    for (const [kind, records] of [
      ["scanner", evidence.security_scans], ["SBOM", evidence.sboms], ["provenance", evidence.provenance]
    ]) {
      if (records[component].image_digest !== digest) {
        throw new Error(`${component} ${kind} binds another digest`);
      }
      if (records[component].commit !== context.commit) {
        throw new Error(`${component} ${kind} binds another commit`);
      }
      if (records[component].status !== "PASS") {
        throw new Error(`${component} ${kind} is NOT RUN`);
      }
    }
  }
}

function verifyBundleIdentity(bundle, context) {
  for (const [field, expected] of [
    ["repository", context.repository], ["reviewed_commit", context.commit], ["reviewed_tree", context.tree],
    ["version", context.version], ["migration_version", context.migration]
  ]) {
    if (bundle[field] !== expected) throw new Error(`Attestation Bundle ${field} mismatch`);
  }
}

function verifyRuntime(attestation, context, policy, testEvidence) {
  verifySigned(attestation, context, policy, "Operations", true);
  if (!testEvidence) throw new Error("Runtime Attestation cannot be checked without exact candidate evidence");
  const digests = componentDigests(testEvidence);
  for (const component of ["server", "admin", "console"]) {
    if (attestation[`${component}_image_digest`] !== digests[component]) {
      throw new Error(`${component} Runtime Attestation digest mismatch`);
    }
  }
  if (!attestation.payment_adapter.production_ready ||
      ["sandbox", "manual_transfer"].includes(attestation.payment_adapter.type)) {
    throw new Error("production payment adapter is not ready");
  }
  if (!attestation.smtp.delivery_verified) throw new Error("production SMTP delivery verification is absent");
  if (attestation.database_query_summary.invalid_decimal_rows !== 0) {
    throw new Error("database contains invalid commercial Decimal rows");
  }
  if (attestation.commercially_approved_providers.length < 1 ||
      attestation.database_query_summary.commercially_approved_provider_count !==
        attestation.commercially_approved_providers.length) {
    throw new Error("no database-derived commercially approved Provider exists");
  }
  for (const provider of attestation.commercially_approved_providers) {
    if (Date.parse(provider.contract_valid_from) > context.now ||
        Date.parse(provider.contract_valid_to) <= context.now) {
      throw new Error("Provider contract window is not currently valid");
    }
  }
  if (context.profile === "MARKETPLACE_PRODUCTION") {
    if (!attestation.payout_adapter.production_ready || attestation.payout_adapter.type === "sandbox") {
      throw new Error("production payout adapter is not ready");
    }
    if (attestation.production_ready_suppliers.length < 1 ||
        attestation.database_query_summary.production_ready_supplier_count !==
          attestation.production_ready_suppliers.length) {
      throw new Error("no database-derived production-ready supplier exists");
    }
  }
}

const args = parseArgs(process.argv.slice(2));
for (const required of [
  "manifest", "schema", "attestation-schema", "test-schema", "trust-schema", "trust-policy",
  "profile", "repository", "commit", "tree", "version", "migration", "evidence-root", "output"
]) {
  if (!args[required]) throw new Error(`Missing --${required}`);
}

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateManifest = ajv.compile(loadJson(args.schema));
const validateBundle = ajv.compile(loadJson(args["attestation-schema"]));
const validateTest = ajv.compile(loadJson(args["test-schema"]));
const validateTrust = ajv.compile(loadJson(args["trust-schema"]));
const manifest = loadJson(args.manifest);
const policy = loadJson(args["trust-policy"]);
const results = [];
const add = (id, title, passed, detail, metadata = {}) =>
  results.push({ id, title, result: passed ? "PASS" : "BLOCKED", detail, ...metadata });

const context = {
  profile: args.profile,
  repository: args.repository,
  commit: args.commit,
  tree: args.tree,
  version: args.version,
  migration: args.migration,
  evidenceRoot: args["evidence-root"],
  workflowRunId: args["workflow-run-id"] ?? "LOCAL",
  workflowRunAttempt: args["workflow-run-attempt"] ?? "LOCAL",
  branchOrTag: args["branch-or-tag"] ?? "branch/local",
  now: args.now ? Date.parse(args.now) : Date.now()
};
const formalRelease = context.profile !== "ENGINEERING_PREVIEW";

const trustValid = validateTrust(policy);
add("trust_policy_schema", "Trusted issuer policy schema", trustValid,
  trustValid ? `issuers=${policy.trusted_issuers.length}` : JSON.stringify(validateTrust.errors));
if (formalRelease) {
  const actualHash = createHash("sha256").update(readFileSync(args["trust-policy"])).digest("hex");
  add("trust_policy_anchor", "Out-of-repository trusted issuer policy anchor",
    Boolean(args["trust-policy-sha256"]) && args["trust-policy-sha256"] === actualHash,
    args["trust-policy-sha256"] ?
      `actual=${actualHash}; expected=${args["trust-policy-sha256"]}` :
      "MODELDOCK_TRUST_POLICY_SHA256 is not configured");
}

const manifestValid = validateManifest(manifest);
add("manifest_schema", "Commercial gate catalog JSON Schema", manifestValid,
  manifestValid ? `schema_version=${manifest.schema_version}; gates=${manifest.gates.length}` :
    JSON.stringify(validateManifest.errors));
let catalogValid = manifestValid;
if (catalogValid) {
  const ids = manifest.gates.map((gate) => gate.id);
  catalogValid = ids.length === new Set(ids).size && ids.length === Object.keys(expectedGates).length;
  for (const gate of manifest.gates) {
    const expected = expectedGates[gate.id];
    catalogValid &&= Boolean(expected) &&
      sameSet(gate.profiles, expected[0]) && sameSet(gate.required_roles, expected[1]);
  }
  catalogValid &&= Object.keys(expectedGates).every((id) => ids.includes(id));
}
add("mandatory_gate_catalog", "Mandatory Gate IDs, profiles, and roles", catalogValid,
  catalogValid ? "exact Gate set v2" : "deleted, renamed, duplicate, unknown, or weakened Gate");

let testEvidence;
if (args["test-evidence"]) {
  try {
    testEvidence = loadJson(args["test-evidence"]);
    verifyTestEvidence(testEvidence, validateTest, context, formalRelease);
    add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", true,
      `commit=${context.commit}; server=${testEvidence.server_image_digest}`);
  } catch (error) {
    add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", false, error.message);
  }
} else {
  add("commercial_test_evidence", "Exact-commit and same-digest commercial evidence", false, "evidence is missing");
}

let bundle = { external_attestations: [], runtime_attestations: [] };
if (args["attestation-bundle"]) {
  try {
    bundle = loadJson(args["attestation-bundle"]);
    if (!validateBundle(bundle)) throw new Error(JSON.stringify(validateBundle.errors));
    verifyBundleIdentity(bundle, context);
    add("attestation_bundle_schema", "Signed Attestation Artifact Bundle schema and identity", true,
      `external=${bundle.external_attestations.length}; runtime=${bundle.runtime_attestations.length}`);
  } catch (error) {
    bundle = { external_attestations: [], runtime_attestations: [] };
    add("attestation_bundle_schema", "Signed Attestation Artifact Bundle schema and identity", false, error.message);
  }
} else if (formalRelease) {
  add("attestation_bundle_schema", "Signed Attestation Artifact Bundle schema and identity", false,
    "GitHub Actions Attestation Bundle Artifact is missing");
}

if (formalRelease && manifestValid) {
  const runtime = bundle.runtime_attestations.find((item) => item.release_profile === context.profile);
  try {
    if (!runtime) throw new Error("signed target-environment Runtime Attestation is missing");
    verifyRuntime(runtime, context, policy, testEvidence);
    add("runtime_attestation", "Signed target-environment Runtime Attestation", true,
      `issuer=${runtime.issuer}; expires=${runtime.expires_at}`, {
        signature_status: "VALID", expires_at: runtime.expires_at, commit: runtime.commit_sha,
        digests: {
          server: runtime.server_image_digest,
          admin: runtime.admin_image_digest,
          console: runtime.console_image_digest
        }
      });
  } catch (error) {
    add("runtime_attestation", "Signed target-environment Runtime Attestation", false, error.message, {
      signature_status: runtime ? "INVALID" : "MISSING"
    });
  }

  for (const gate of manifest.gates.filter((item) => item.profiles.includes(context.profile))) {
    const roleErrors = [];
    const used = [];
    for (const role of gate.required_roles) {
      const attestation = bundle.external_attestations.find((item) =>
        item.gate_id === gate.id && item.release_profile === context.profile && item.issuer_role === role);
      try {
        if (!attestation) throw new Error(`${role} attestation is missing`);
        verifySigned(attestation, context, policy, role, false);
        used.push(attestation);
      } catch (error) {
        roleErrors.push(`${role}: ${error.message}`);
      }
    }
    const passed = roleErrors.length === 0;
    const first = used[0];
    add(gate.id, gate.title, passed,
      passed ? `roles=${gate.required_roles.join(",")}; issuer=${used.map((item) => item.issuer).join(",")}` :
        roleErrors.join("; "), {
        source: first?.evidence_reference ?? "none",
        signature_status: passed ? "VALID" : used.length ? "PARTIAL" : "MISSING",
        commit: first?.reviewed_commit ?? "",
        expires_at: first?.expires_at ?? ""
      });
  }
}

const output = {
  schema_version: "2.0.0",
  profile: context.profile,
  repository: context.repository,
  commit: context.commit,
  tree: context.tree,
  version: context.version,
  migration: context.migration,
  workflow_run_id: context.workflowRunId,
  workflow_run_attempt: context.workflowRunAttempt,
  branch_or_tag: context.branchOrTag,
  results
};
writeFileSync(args.output, `${JSON.stringify(output, null, 2)}\n`, "utf8");
if (results.some((result) => result.result === "BLOCKED")) process.exitCode = 1;
