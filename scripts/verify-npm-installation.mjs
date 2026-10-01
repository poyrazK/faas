#!/usr/bin/env node
// ADR-172: install without credentials or lifecycle scripts; compare the binary
// to the same checksum-verified archive used to build the platform package.
import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { parseArgs } from "node:util";
import { loadPackages } from "./publish-npm-packages.mjs";

const { values } = parseArgs({ options: {
  "packages-dir": { type: "string" }, prefix: { type: "string" },
} });
if (!values["packages-dir"] || !values.prefix) throw new Error("--packages-dir and --prefix are required");
const packages = loadPackages(values["packages-dir"]);
const version = packages[4].manifest.version;
const prefix = resolve(values.prefix);
mkdirSync(prefix, { recursive: true });
const userConfig = resolve(prefix, "user.npmrc");
const globalConfig = resolve(prefix, "global.npmrc");
writeFileSync(userConfig, "");
writeFileSync(globalConfig, "");
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
  !key.toUpperCase().startsWith("NPM_") && key !== "NODE_AUTH_TOKEN"));
function run(command, args) {
  const result = spawnSync(command, args, { env, encoding: "utf8", timeout: 180_000 });
  if (result.error || result.status !== 0) throw new Error(`${command} failed: ${result.error?.message ?? result.stderr}`);
  return result.stdout;
}
console.log(run("npm", ["install", "--global", "--prefix", prefix, "--cache", resolve(prefix, "cache"),
  "--userconfig", userConfig, "--globalconfig", globalConfig, "--ignore-scripts", "--include=optional",
  "--registry=https://registry.npmjs.org/", `gregale@${version}`]).trim());
const cli = resolve(prefix, "bin/gregale");
const output = run(cli, ["version"]);
if (!output.includes(`v${version}`)) throw new Error(`Installed CLI reports the wrong version: ${output}`);
run(cli, ["--help"]);
const arch = process.arch === "x64" ? "amd64" : process.arch;
const name = `@gregale/cli-${process.platform}-${arch}`;
const staged = packages.find(pkg => pkg.manifest.name === name);
if (!staged) throw new Error(`Unsupported smoke-test platform: ${name}`);
const require = createRequire(resolve(prefix, "lib/node_modules/gregale/package.json"));
const installed = require.resolve(`${name}/bin/gregale`);
const sha256 = path => createHash("sha256").update(readFileSync(path)).digest("hex");
if (sha256(installed) !== sha256(resolve(staged.dir, "bin/gregale"))) {
  throw new Error("Installed npm binary differs from the verified release archive");
}
console.log(output.trim());
console.log("Anonymous npm install, version, help and release binary SHA256: PASS");
