import { createPrivateKey, sign } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  }
  return value;
}

const [inputPath, outputPath, privateKeyPath] = process.argv.slice(2);
if (!inputPath || !outputPath || !privateKeyPath) {
  throw new Error("usage: node sign-attestation.mjs INPUT OUTPUT ED25519_PRIVATE_KEY_PEM");
}
const attestation = JSON.parse(readFileSync(inputPath, "utf8"));
attestation.signature = "";
const payload = { ...attestation };
delete payload.signature;
const privateKey = createPrivateKey(readFileSync(privateKeyPath, "utf8"));
if (privateKey.asymmetricKeyType !== "ed25519") throw new Error("Attestation key must be Ed25519");
attestation.signature = sign(
  null,
  Buffer.from(JSON.stringify(canonical(payload)), "utf8"),
  privateKey
).toString("base64");
writeFileSync(outputPath, `${JSON.stringify(attestation, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
