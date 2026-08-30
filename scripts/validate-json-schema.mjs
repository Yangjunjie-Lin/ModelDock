import { readFileSync } from "node:fs";
import Ajv2020 from "../tools/commercial-evidence/node_modules/ajv/dist/2020.js";
import addFormats from "../tools/commercial-evidence/node_modules/ajv-formats/dist/index.js";

const [schemaPath, documentPath] = process.argv.slice(2);
if (!schemaPath || !documentPath) throw new Error("usage: node validate-json-schema.mjs SCHEMA DOCUMENT");
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validate = ajv.compile(JSON.parse(readFileSync(schemaPath, "utf8")));
const valid = validate(JSON.parse(readFileSync(documentPath, "utf8")));
if (!valid) throw new Error(JSON.stringify(validate.errors));
