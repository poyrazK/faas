#!/usr/bin/env node
// ADR-172: accepted uploads are not necessarily public npm versions.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { spawnSync } from "node:child_process";
import { isDeepStrictEqual, parseArgs } from "node:util";

const REGISTRY = "https://registry.npmjs.org/";
const NAMES = ["@gregale/cli-darwin-amd64", "@gregale/cli-darwin-arm64",
  "@gregale/cli-linux-amd64", "@gregale/cli-linux-arm64", "gregale"];
class Pending extends Error {}
const isHolding = data => data?.versions?.["0.0.0-stage"] &&
  Object.keys(data.versions).every(version => version === "0.0.0-stage");

export function loadPackages(directory) {
  const packages = NAMES.map(name => {
    const dir = resolve(directory, name);
    return { dir, manifest: JSON.parse(readFileSync(resolve(dir, "package.json"))) };
  });
  const version = packages[4].manifest.version;
  if (!/^\d+\.\d+\.\d+(?:-[\w.-]+)?$/.test(version)) throw new Error("Invalid npm release version");
  packages.forEach(({ manifest }, i) => {
    if (manifest.name !== NAMES[i] || manifest.version !== version) {
      throw new Error(`Expected ${NAMES[i]}@${version}`);
    }
    if (i < 4) {
      const [, os, arch] = manifest.name.split("-");
      if (!isDeepStrictEqual(manifest.os, [os]) ||
          !isDeepStrictEqual(manifest.cpu, [arch === "amd64" ? "x64" : arch])) {
        throw new Error(`Invalid platform mapping for ${manifest.name}`);
      }
    }
  });
  if (!isDeepStrictEqual(packages[4].manifest.optionalDependencies,
      Object.fromEntries(NAMES.slice(0, 4).map(name => [name, version])))) {
    throw new Error("Launcher must pin all four platform packages to the release version");
  }
  return packages;
}

export async function publishPackages(packages, {
  repairOnly = false, registry = REGISTRY, fetchPublic = fetch,
  runNpm = args => {
    const result = spawnSync("npm", [...args, `--registry=${registry}`], {
      stdio: ["ignore", "inherit", "inherit"], timeout: 180_000,
    });
    if (result.error || result.status !== 0) throw new Error(`npm ${args[0]} failed: ${result.error?.message ?? result.status}`);
  },
  timeoutMs = 600_000, pollMs = 10_000, now = Date.now,
  sleep = ms => new Promise(done => setTimeout(done, ms)), log = console.log,
} = {}) {
  const version = packages[4].manifest.version;
  const prerelease = version.includes("-");
  const tag = prerelease ? "rc" : "latest";
  const stableTags = new Map();
  // These requests deliberately never use NODE_AUTH_TOKEN or npm's config.
  async function request(url, method = "GET") {
    try {
      return await fetchPublic(url, { method, headers: { "cache-control": "no-cache" },
        signal: AbortSignal.timeout(10_000) });
    } catch (error) { throw new Pending(`public registry request failed: ${error.message}`); }
  }
  async function metadata(name) {
    const response = await request(new URL(encodeURIComponent(name), registry));
    if (response.status === 404) return null;
    if (!response.ok) throw new Pending(`${name}: public registry HTTP ${response.status}`);
    return response.json();
  }
  async function ready(pkg) {
    const name = pkg.manifest.name;
    const data = await metadata(name);
    const published = data?.versions?.[version];
    if (!published) throw new Pending(`${name}@${version} is not public${isHolding(data) ? " (holding placeholder)" : ""}`);
    for (const key of ["name", "version", "os", "cpu", "optionalDependencies"]) {
      if (!isDeepStrictEqual(published[key], pkg.manifest[key])) {
        throw new Error(`${name}@${version}: public ${key} differs from staged manifest`);
      }
    }
    const dist = published.dist;
    if (!dist?.integrity || !dist?.attestations?.url ||
        dist.attestations.provenance?.predicateType !== "https://slsa.dev/provenance/v1") {
      throw new Error(`${name}@${version}: missing integrity or provenance`);
    }
    if (!dist.tarball || new URL(dist.tarball).origin !== new URL(registry).origin) {
      throw new Error(`${name}@${version}: unexpected tarball registry`);
    }
    const tarball = await request(dist.tarball, "HEAD");
    if (!tarball.ok) throw new Pending(`${name}@${version}: tarball HTTP ${tarball.status}`);
    return data;
  }
  async function waitFor(group) {
    const deadline = now() + timeoutMs;
    while (true) {
      const pending = [];
      await Promise.all(group.map(async pkg => {
        try { await ready(pkg); }
        catch (error) { if (!(error instanceof Pending)) throw error; pending.push(error.message); }
      }));
      if (!pending.length) return;
      log(`Waiting for public npm packages: ${pending.join("; ")}`);
      if (now() >= deadline) {
        throw new Error(`npm publication remains incomplete after ${timeoutMs / 1000}s. ` +
          "Check npm's package/owner dashboard for processing or staging; do not blindly republish accepted versions. " + pending.join("; "));
      }
      await sleep(Math.min(pollMs, deadline - now()));
    }
  }
  async function cleanLatest() {
    if (!prerelease) return;
    for (const { manifest: { name } } of packages) {
      const latest = (await metadata(name))?.["dist-tags"]?.latest;
      if (latest && !latest.includes("-")) stableTags.set(name, latest);
      if (latest?.includes("-")) {
        const stable = stableTags.get(name);
        await runNpm(stable ? ["dist-tag", "add", `${name}@${stable}`, "latest"] :
          ["dist-tag", "rm", name, "latest"]);
        log(`${name}: ${stable ? "restored stable latest=" + stable : "removed prerelease latest=" + latest}`);
      }
    }
  }
  async function submit(pkg) {
    const name = pkg.manifest.name;
    const data = await metadata(name);
    if (data?.versions?.[version]) { log(`${name}@${version} already public; no upload`); return; }
    if (repairOnly || isHolding(data)) {
      log(`${name}@${version}: waiting for accepted upload; no repeat upload`);
      return;
    }
    await runNpm(["publish", pkg.dir, "--access", "public", "--tag", tag, "--provenance"]);
    log(`${name}@${version}: upload accepted; public availability still required`);
  }
  async function setTag(pkg) {
    const name = pkg.manifest.name;
    const current = (await metadata(name))?.["dist-tags"]?.[tag];
    if (current !== version) {
      if (repairOnly && current) throw new Error(`Refusing to replace another release's ${name} ${tag}=${current}`);
      await runNpm(["dist-tag", "add", `${name}@${version}`, tag]);
    }
  }
  await cleanLatest();
  const platforms = packages.slice(0, 4);
  const root = packages[4];
  // An earlier workflow may already have exposed a launcher without binaries.
  // Withdraw only this broken prerelease's rc tag until its dependencies exist.
  if (prerelease) {
    let incomplete = false;
    for (const pkg of platforms) {
      try { await ready(pkg); }
      catch (error) { if (!(error instanceof Pending)) throw error; incomplete = true; }
    }
    if (incomplete && (await metadata("gregale"))?.["dist-tags"]?.rc === version) {
      await runNpm(["dist-tag", "rm", "gregale", "rc"]);
      log(`gregale: withdrew incomplete rc=${version} until all platforms are public`);
    }
  }
  let failure;
  try {
    for (const pkg of platforms) await submit(pkg);
    await cleanLatest();
    await waitFor(platforms);
    for (const pkg of platforms) await setTag(pkg);
    await submit(root);
    await waitFor([root]);
    await setTag(root);
  } catch (error) { failure = error; }
  // npm can assign latest when a new package becomes visible, even with --tag rc.
  try { await cleanLatest(); }
  catch (error) { if (!failure) failure = error; else log(`Tag cleanup also failed: ${error.message}`); }
  if (failure) throw failure;
  for (const { manifest: { name } } of packages) {
    const tags = (await metadata(name))?.["dist-tags"];
    if (tags?.[tag] !== version || (prerelease && tags?.latest?.includes("-"))) {
      throw new Error(`${name}: public dist-tags do not satisfy the release policy`);
    }
  }
  log(`All five npm packages are public with integrity, provenance and ${tag}=${version}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const { values } = parseArgs({ options: {
      "packages-dir": { type: "string" }, "repair-only": { type: "boolean", default: false },
    } });
    if (!values["packages-dir"]) throw new Error("--packages-dir is required");
    await publishPackages(loadPackages(values["packages-dir"]), { repairOnly: values["repair-only"] });
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
