import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { loadPackages, publishPackages } from "./publish-npm-packages.mjs";

const names = ["@gregale/cli-darwin-amd64", "@gregale/cli-darwin-arm64",
  "@gregale/cli-linux-amd64", "@gregale/cli-linux-arm64", "gregale"];
function fixture(t, version = "1.2.3-rc.4") {
  const dir = mkdtempSync(resolve(tmpdir(), "gregale-npm-publish-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const published = new Map(), accepted = new Set(), calls = [], tags = new Map();
  const manifests = names.map((name, i) => {
    const [, os, arch] = name.split("-");
    return { name, version, os: i < 4 ? [os] : ["darwin", "linux"],
      cpu: i < 4 ? [arch === "amd64" ? "x64" : arch] : ["x64", "arm64"],
      ...(i === 4 ? { optionalDependencies: Object.fromEntries(names.slice(0, 4).map(n => [n, version])) } : {}) };
  });
  for (const manifest of manifests) {
    const pkgDir = resolve(dir, manifest.name);
    mkdirSync(pkgDir, { recursive: true });
    writeFileSync(resolve(pkgDir, "package.json"), JSON.stringify(manifest));
  }
  const state = { published, accepted, calls, tags, manifests, ticks: 0, pending: new Set(),
    missingTarball: new Set(), onPoll() {}, onPublish() {} };
  const makePublic = name => {
    const manifest = structuredClone(manifests[names.indexOf(name)]);
    manifest.dist = { integrity: "sha512-test", tarball: `https://registry.npmjs.org/tarball/${encodeURIComponent(name)}`,
      attestations: { url: "https://registry.npmjs.org/attestation", provenance: { predicateType: "https://slsa.dev/provenance/v1" } } };
    published.set(name, manifest);
    tags.set(name, { ...tags.get(name), [version.includes("-") ? "rc" : "latest"]: version,
      // Reproduce npm's initial-package latest behavior, even with --tag rc.
      ...(!tags.get(name)?.latest ? { latest: version } : {}) });
  };
  const options = {
    log() {}, timeoutMs: 30, pollMs: 10, now: () => state.ticks,
    sleep: async ms => { state.ticks += ms; state.onPoll(); },
    fetchPublic: async (url, options) => {
      assert.equal(options.headers.authorization, undefined, "registry checks must be anonymous");
      if (options.method === "HEAD") {
        const name = decodeURIComponent(new URL(url).pathname.split("/").pop());
        return { ok: !state.missingTarball.has(name), status: state.missingTarball.has(name) ? 404 : 200 };
      }
      const name = decodeURIComponent(new URL(url).pathname.slice(1));
      const manifest = published.get(name);
      const holding = accepted.has(name) && !manifest;
      return { ok: true, status: 200, json: async () => ({
        "dist-tags": structuredClone(tags.get(name) ?? {}),
        versions: manifest ? { [version]: structuredClone(manifest) } :
          holding ? { "0.0.0-stage": { version: "0.0.0-stage" } } : {},
      }) };
    },
    runNpm: async args => {
      calls.push(args);
      if (args[0] === "publish") {
        const name = JSON.parse(readFileSync(resolve(args[1], "package.json"))).name;
        state.onPublish(name);
        if (name === "gregale") {
          assert.equal(published.size, 4, "launcher exposed before all four binaries are public");
          assert.equal(state.missingTarball.size, 0, "launcher exposed before binary tarballs are downloadable");
        }
        accepted.add(name);
        if (!state.pending.has(name)) makePublic(name);
        else tags.set(name, { latest: "0.0.0-stage" });
      } else if (args[1] === "rm") {
        delete tags.get(args[2])[args[3]];
      } else {
        const spec = args[2], at = spec.lastIndexOf("@"), name = spec.slice(0, at);
        tags.set(name, { ...tags.get(name), [args[3]]: spec.slice(at + 1) });
      }
    },
  };
  return { dir, packages: loadPackages(dir), options, state, makePublic };
}

test("delayed platform metadata gates the root and initial latest is removed", async t => {
  const f = fixture(t);
  f.state.pending.add(names[1]);
  f.state.onPoll = () => { if (f.state.ticks >= 20) f.makePublic(names[1]); };
  await publishPackages(f.packages, f.options);
  assert.equal(f.state.ticks, 20);
  assert.deepEqual(f.state.calls.filter(c => c[0] === "publish").map(c => c[1]), f.packages.map(p => p.dir));
  for (const name of names) assert.deepEqual(f.state.tags.get(name), { rc: "1.2.3-rc.4" });
});

test("a non-runner platform held forever fails without publishing the root", async t => {
  const f = fixture(t);
  f.state.pending.add(names[1]);
  await assert.rejects(publishPackages(f.packages, f.options), /incomplete.*do not blindly republish/);
  assert.equal(f.state.accepted.has("gregale"), false);
  for (const name of names.slice(0, 4)) assert.equal(f.state.tags.get(name)?.latest, undefined);
});

test("a visible manifest with an unavailable tarball still gates the launcher", async t => {
  const f = fixture(t);
  f.state.missingTarball.add(names[3]);
  f.state.onPoll = () => f.state.missingTarball.clear();
  await publishPackages(f.packages, f.options);
  assert.equal(f.state.ticks, 10);
});

test("rerun rechecks every platform even when the root already exists", async t => {
  const f = fixture(t);
  names.forEach(f.makePublic);
  f.state.published.delete(names[1]);
  f.state.accepted.add(names[1]);
  await assert.rejects(publishPackages(f.packages, f.options), /holding placeholder/);
  assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 0);
  assert.equal(f.state.tags.get("gregale").rc, undefined, "incomplete release must not remain exposed as rc");
});

test("repair-only restores a completed release without any uploads", async t => {
  const f = fixture(t);
  names.forEach(f.makePublic);
  await publishPackages(f.packages, { ...f.options, repairOnly: true });
  assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 0);
  for (const name of names) assert.deepEqual(f.state.tags.get(name), { rc: "1.2.3-rc.4" });
});

test("repair-only waits for held uploads and restores the withdrawn root tag", async t => {
  const f = fixture(t);
  names.forEach(f.makePublic);
  f.state.published.delete(names[2]); f.state.accepted.add(names[2]);
  f.state.onPoll = () => f.makePublic(names[2]);
  await publishPackages(f.packages, { ...f.options, repairOnly: true });
  assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 0);
  assert.equal(f.state.tags.get("gregale").rc, "1.2.3-rc.4");
});

test("repair-only never uploads a version missing from the registry", async t => {
  const f = fixture(t);
  await assert.rejects(publishPackages(f.packages, { ...f.options, repairOnly: true }), /incomplete/);
  assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 0);
});

test("historical holding placeholders do not prevent subsequent releases", async t => {
  const f = fixture(t);
  const originalFetch = f.options.fetchPublic;
  f.options.fetchPublic = async (...args) => {
    const response = await originalFetch(...args);
    if (args[1].method === "HEAD") return response;
    const data = await response.json();
    data.versions["0.0.0-stage"] = { version: "0.0.0-stage" };
    data.versions["1.2.3-rc.3"] = { version: "1.2.3-rc.3" };
    return { ...response, json: async () => data };
  };
  await publishPackages(f.packages, f.options);
  assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 5);
});

test("prerelease publishing preserves a previous stable latest", async t => {
  const f = fixture(t);
  names.forEach(name => f.state.tags.set(name, { latest: "1.2.2" }));
  await publishPackages(f.packages, f.options);
  for (const name of names) assert.deepEqual(f.state.tags.get(name), { latest: "1.2.2", rc: "1.2.3-rc.4" });
});

test("late processing cannot replace a previously observed stable latest", async t => {
  const f = fixture(t);
  names.forEach(name => f.state.tags.set(name, { latest: "1.2.2" }));
  f.state.pending.add(names[1]);
  f.state.onPoll = () => {
    f.makePublic(names[1]);
    f.state.tags.set(names[1], { latest: "1.2.3-rc.4", rc: "1.2.3-rc.4" });
  };
  await publishPackages(f.packages, f.options);
  assert.equal(f.state.tags.get(names[1]).latest, "1.2.2");
});

test("stable publishing moves latest without touching rc", async t => {
  const f = fixture(t, "1.2.3");
  names.forEach(name => f.state.tags.set(name, { rc: "1.2.3-rc.4" }));
  await publishPackages(f.packages, f.options);
  for (const name of names) assert.deepEqual(f.state.tags.get(name), { latest: "1.2.3", rc: "1.2.3-rc.4" });
});

test("repair refuses to roll a different release's rc tag back", async t => {
  const f = fixture(t);
  names.forEach(f.makePublic);
  f.state.tags.set("gregale", { rc: "1.2.3-rc.5" });
  await assert.rejects(publishPackages(f.packages, { ...f.options, repairOnly: true }), /Refusing to replace another release/);
  assert.equal(f.state.tags.get("gregale").rc, "1.2.3-rc.5");
});

test("wrong platform mapping or missing provenance fails immediately", async t => {
  for (const field of ["cpu", "dist"]) {
    const f = fixture(t); names.forEach(f.makePublic);
    if (field === "cpu") f.state.published.get(names[1]).cpu = ["x64"];
    else delete f.state.published.get(names[1]).dist.attestations;
    await assert.rejects(publishPackages(f.packages, f.options), /differs from staged manifest|missing integrity or provenance/);
    assert.equal(f.state.calls.filter(c => c[0] === "publish").length, 0);
  }
});

test("publish failures are fatal and never expose the root", async t => {
  const f = fixture(t);
  f.state.onPublish = name => { if (name === names[2]) throw new Error("publish denied"); };
  await assert.rejects(publishPackages(f.packages, f.options), /publish denied/);
  assert.equal(f.state.accepted.has("gregale"), false);
});

test("invalid local dependency pins fail before registry mutations", t => {
  const f = fixture(t);
  const root = f.state.manifests[4]; root.optionalDependencies[names[1]] = "^1.2.3";
  writeFileSync(resolve(f.dir, "gregale/package.json"), JSON.stringify(root));
  assert.throws(() => loadPackages(f.dir), /pin all four platform packages/);
});

test("anonymous installation strips credentials, disables scripts and checks the binary hash", t => {
  const f = fixture(t);
  const target = `@gregale/cli-${process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  const platform = resolve(f.dir, target, "bin"); mkdirSync(platform, { recursive: true });
  writeFileSync(resolve(platform, "gregale"), "#!/bin/sh\nprintf 'gregale v1.2.3-rc.4\\n'\n", { mode: 0o755 });
  const tools = resolve(f.dir, "tools"); mkdirSync(tools);
  writeFileSync(resolve(tools, "npm"), `#!/usr/bin/env node
const fs = require('node:fs'), path = require('node:path');
if (Object.keys(process.env).some(k => k.toUpperCase().startsWith('NPM_') || k === 'NODE_AUTH_TOKEN')) process.exit(91);
const args = process.argv.slice(2), value = flag => args[args.indexOf(flag) + 1];
if (!args.includes('--ignore-scripts') || !args.includes('--include=optional')) process.exit(92);
if (fs.readFileSync(value('--userconfig'), 'utf8') || fs.readFileSync(value('--globalconfig'), 'utf8')) process.exit(93);
const prefix = value('--prefix'), pkg = path.join(prefix, 'lib/node_modules/gregale');
const binary = path.join(pkg, 'node_modules', ${JSON.stringify(target)}, 'bin/gregale');
fs.mkdirSync(path.dirname(binary), {recursive:true});
fs.writeFileSync(path.join(pkg, 'package.json'), '{}');
fs.copyFileSync(${JSON.stringify(resolve(platform, "gregale"))}, binary); fs.chmodSync(binary, 0o755);
fs.mkdirSync(path.join(prefix, 'bin'), {recursive:true}); fs.symlinkSync(binary, path.join(prefix, 'bin/gregale'));
`, { mode: 0o755 });
  const result = spawnSync(process.execPath, ["scripts/verify-npm-installation.mjs",
    "--packages-dir", f.dir, "--prefix", resolve(f.dir, "install")], { encoding: "utf8",
    env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, NODE_AUTH_TOKEN: "fixture-secret",
      NPM_CONFIG_USERCONFIG: "/fixture-auth-config", npm_config_token: "fixture-token" } });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /binary SHA256: PASS/);
  // Reject a corrupted payload before executing version or help.
  const marker = resolve(f.dir, "unverified-binary-executed");
  writeFileSync(resolve(tools, "npm"), readFileSync(resolve(tools, "npm"), "utf8") +
    `\nfs.appendFileSync(binary, ${JSON.stringify("\ntouch '" + marker + "'\n")});\n`);
  const corrupt = spawnSync(process.execPath, ["scripts/verify-npm-installation.mjs",
    "--packages-dir", f.dir, "--prefix", resolve(f.dir, "corrupt-install")], {
    encoding: "utf8", env: { ...process.env, PATH: `${tools}:${process.env.PATH}` },
  });
  assert.notEqual(corrupt.status, 0);
  assert.match(corrupt.stderr, /differs from the verified release archive/);
  assert.equal(existsSync(marker), false, "unverified binary must never execute");
});
