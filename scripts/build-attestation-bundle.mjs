import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

function args(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    if (!argv[index]?.startsWith("--") || argv[index + 1] === undefined) throw new Error("invalid argument list");
    values[argv[index].slice(2)] = argv[index + 1];
  }
  return values;
}
function json(path) { return JSON.parse(readFileSync(path, "utf8")); }
function jsonFiles(directory) {
  if (!directory) return [];
  return readdirSync(directory)
    .filter((name) => name.endsWith(".json"))
    .map((name) => resolve(directory, name))
    .filter((path) => statSync(path).isFile())
    .map(json);
}

const options = args(process.argv.slice(2));
for (const name of ["output", "commit", "tree", "version", "migration", "external-workflow-run-id", "runtime-workflow-run-id"]) {
  if (!options[name]) throw new Error(`missing --${name}`);
}
if (process.env.GITHUB_ACTIONS !== "true" ||
    process.env.GITHUB_REPOSITORY !== "Yangjunjie-Lin/ModelDock") {
  throw new Error("Attestation Bundles must be assembled by ModelDock GitHub Actions");
}
const external = jsonFiles(options["external-directory"]);
const runtime = jsonFiles(options["runtime-directory"]);
const identities = new Set();
for (const attestation of external) {
  if (attestation.workflow_run_id !== options["external-workflow-run-id"]) {
    throw new Error("external Attestation Workflow Run does not match the current-repository Artifact source");
  }
  const identity = `${attestation.gate_id}|${attestation.release_profile}|${attestation.issuer_role}`;
  if (identities.has(identity)) throw new Error(`duplicate external Attestation ${identity}`);
  identities.add(identity);
}
const profiles = new Set();
for (const attestation of runtime) {
  if (attestation.workflow_run_id !== options["runtime-workflow-run-id"]) {
    throw new Error("Runtime Attestation Workflow Run does not match the current-repository Artifact source");
  }
  if (profiles.has(attestation.release_profile)) {
    throw new Error(`duplicate Runtime Attestation for ${attestation.release_profile}`);
  }
  profiles.add(attestation.release_profile);
}
const bundle = {
  schema_version: "2.0.0",
  repository: process.env.GITHUB_REPOSITORY,
  reviewed_commit: options.commit,
  reviewed_tree: options.tree,
  version: options.version,
  migration_version: options.migration,
  evidence_origin: "GITHUB_ACTIONS_ARTIFACT",
  external_attestations: external,
  runtime_attestations: runtime
};
writeFileSync(options.output, `${JSON.stringify(bundle, null, 2)}\n`, "utf8");
