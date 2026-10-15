import test, { before } from "node:test";
import assert from "node:assert/strict";
import { createHash, webcrypto } from "node:crypto";
import { serve } from "./worker.mjs";

globalThis.crypto ||= webcrypto;
const now = Date.now();
const site = "11111111-1111-4111-8111-111111111111";
const first = "22222222-2222-4222-8222-222222222222";
const second = "33333333-3333-4333-8333-333333333333";
const hostname = `${site}.hosting.example`;
const origin = "a".repeat(64);
let pair, jwk, validToken;
before(async () => {
  pair = await crypto.subtle.generateKey({ name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" }, true, ["sign", "verify"]);
  jwk = { ...await crypto.subtle.exportKey("jwk", pair.publicKey), kid: "test-key", alg: "RS256" };
  validToken = await token();
});

async function token(overrides = {}) {
  const header = Buffer.from(JSON.stringify({ alg: "RS256", kid: jwk.kid })).toString("base64url");
  const payload = Buffer.from(JSON.stringify({ iss: "https://internal.cloudflareaccess.com", aud: ["hosting"], email: "operator@example.com", exp: now / 1000 + 300, ...overrides })).toString("base64url");
  const signature = await crypto.subtle.sign("RSASSA-PKCS1-v1_5", pair.privateKey, new TextEncoder().encode(`${header}.${payload}`));
  return `${header}.${payload}.${Buffer.from(signature).toString("base64url")}`;
}

function fixture() {
  let clock = now;
  const objects = new Map();
  const cached = new Map();
  const pending = [];
  const calls = { storage: 0, cache: 0, routes: 0 };
  const route = { version: 1, enabled: true, hostname, site_id: site, origin_id: origin, active_release_id: first, ready_releases: [first], revision: 1, expires_at: now / 1000 + 120 };
  const env = {
    FAAS_FRONTEND_HOSTING_ENABLED: "true", ACCESS_TEAM_DOMAIN: "internal.cloudflareaccess.com",
    ACCESS_AUD: "hosting", INTERNAL_EMAILS: "operator@example.com", ORIGIN_ID: origin,
    ROUTES: { async get() { calls.routes++; return route; } },
    ARTIFACTS: { async get(key) {
      calls.storage++;
      if (!objects.has(key)) return null;
      const body = objects.get(key);
      return { body, size: Buffer.byteLength(body) };
    } },
  };
  const deps = {
    now: () => clock,
    fetch: async url => { assert.equal(url, "https://internal.cloudflareaccess.com/cdn-cgi/access/certs"); return Response.json({ keys: [jwk] }); },
    cache: {
      async match(key) { calls.cache++; return cached.get(key.url)?.clone(); },
      async put(key, response) { cached.set(key.url, new Response(await response.arrayBuffer(), { headers: response.headers })); },
    },
  };
  function release(id, files, spa = false) {
    const manifest = { spa_fallback: spa, files: [] };
    for (const [path, body] of Object.entries(files)) {
      manifest.files.push({ path, size: Buffer.byteLength(body), sha256: createHash("sha256").update(body).digest("hex"), content_type: path.endsWith(".html") ? "text/html" : "text/javascript" });
      objects.set(`hosting/published/${site}/${id}/files/${path}`, body);
    }
    objects.set(`hosting/published/${site}/${id}/manifest.json`, JSON.stringify(manifest));
  }
  async function request(path = "/", options = {}) {
    const headers = new Headers(options.headers);
    if (!headers.has("Cf-Access-Jwt-Assertion")) headers.set("Cf-Access-Jwt-Assertion", validToken);
    const response = await serve(new Request(`https://${hostname}${path}`, { ...options, headers }), env, { waitUntil(p) { pending.push(p); } }, deps);
    await Promise.all(pending.splice(0));
    return response;
  }
  function durable(overrides = {}) {
    env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED = "true";
    const head = { ...route, version: 2, expires_at: 0, generation: 1, deleted: false, ...overrides };
    objects.set(`hosting/routes/${hostname}.json`, JSON.stringify(head));
    return head;
  }
  return { env, route, calls, release, request, cached, objects, durable, advance(ms) { clock += ms; } };
}

test("durable heads are default-off and do not extend legacy leases", async () => {
  const f = fixture();
  f.release(first, { "index.html": "durable HTML" });
  f.durable();
  delete f.env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED;
  f.route.expires_at = now / 1000 - 1;
  assert.equal((await f.request()).status, 503);
  f.env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED = "true";
  assert.equal(await (await f.request()).text(), "durable HTML");
  assert.equal(f.calls.routes, 1);
});

test("warm and cold durable requests survive twenty minutes without API renewal", async () => {
  const f = fixture();
  f.release(first, { "index.html": "durable HTML", "cold.js": "cold asset" });
  f.durable();
  const jwt = await token({ exp: now / 1000 + 3600 });
  const headers = { "Cf-Access-Jwt-Assertion": jwt };
  await f.request("/", { headers });
  f.advance(20 * 60 * 1000);
  const warm = await f.request("/", { headers });
  assert.equal(await warm.text(), "durable HTML");
  assert.equal(warm.headers.get("X-Gregale-Cache"), "HIT");
  f.cached.clear();
  const cold = await f.request("/cold.js", { headers });
  assert.equal(await cold.text(), "cold asset");
  assert.equal(cold.headers.get("X-Gregale-Cache"), "MISS");
  assert.equal(f.calls.routes, 0);
  f.env.FAAS_FRONTEND_HOSTING_ENABLED = "false";
  assert.equal((await f.request("/", { headers })).status, 404);
});

test("durable suspension and deletion override a warm cache and stale enabled KV", async () => {
  const f = fixture();
  f.release(first, { "index.html": "HTML" });
  f.durable();
  await f.request();
  await f.request();
  f.durable({ generation: 2, enabled: false });
  f.advance(60_001);
  assert.equal((await f.request()).status, 503);
  f.durable({ generation: 3, enabled: true });
  f.advance(60_001);
  assert.equal(await (await f.request()).text(), "HTML");
  f.durable({ generation: 4, enabled: false, deleted: true, active_release_id: "", ready_releases: [] });
  f.advance(60_001);
  assert.equal((await f.request()).status, 503);
  assert.equal(f.calls.routes, 0);
});

test("invalid durable heads never fall back to enabled KV or cached files", async () => {
  for (const override of [null, { origin_id: "b".repeat(64) }, { generation: 0 }, { deleted: true }, { hostname: "another.example" }, { expires_at: now / 1000 + 120 }]) {
    const f = fixture();
    f.release(first, { "index.html": "HTML" });
    f.durable();
    await f.request();
    if (override === null) f.objects.set(`hosting/routes/${hostname}.json`, "null");
    else f.durable(override);
    f.advance(60_001);
    assert.equal((await f.request()).status, 503);
    assert.equal(f.calls.routes, 0);
  }
});

test("default-off and missing Access identity never consult routing or caches", async () => {
  const f = fixture();
  delete f.env.FAAS_FRONTEND_HOSTING_ENABLED;
  assert.equal((await f.request()).status, 404);
  f.env.FAAS_FRONTEND_HOSTING_ENABLED = "true";
  assert.equal((await f.request("/", { headers: { "Cf-Access-Jwt-Assertion": "" } })).status, 403);
  assert.deepEqual(f.calls, { storage: 0, cache: 0, routes: 0 });
});

test("JWT signature, audience, expiry, issuer, and internal email are enforced", async () => {
  const f = fixture();
  for (const claims of [{ aud: ["another-app"] }, { exp: now / 1000 - 1 }, { email: "customer@example.com" }, { iss: "https://attacker.example" }, { nbf: now / 1000 + 600 }]) {
    const jwt = await token(claims);
    assert.equal((await f.request("/", { headers: { "Cf-Access-Jwt-Assertion": jwt } })).status, 403);
  }
  const forged = validToken.slice(0, -8) + "AAAAAAAA";
  assert.equal((await f.request("/", { headers: { "Cf-Access-Jwt-Assertion": forged } })).status, 403);
  assert.equal(f.calls.storage, 0);
});

test("cached files remain behind identity, flag, origin, and lease checks", async () => {
  const f = fixture();
  f.release(first, { "index.html": "first release" });
  const cold = await f.request();
  assert.equal(cold.headers.get("X-Gregale-Cache"), "MISS");
  assert.equal(await cold.text(), "first release");
  const reads = f.calls.storage;
  const warm = await f.request();
  assert.equal(warm.headers.get("X-Gregale-Cache"), "HIT");
  assert.equal(await warm.text(), "first release");
  assert.equal(f.calls.storage, reads);
  const denied = await f.request("/", { headers: { "Cf-Access-Jwt-Assertion": "forged" } });
  assert.equal(denied.status, 403);
  assert.equal(denied.headers.get("X-Gregale-Cache"), null);
  f.env.FAAS_FRONTEND_HOSTING_ENABLED = "false";
  assert.equal((await f.request()).status, 404);
  f.env.FAAS_FRONTEND_HOSTING_ENABLED = "true";
  f.route.expires_at = now / 1000 - 1;
  assert.equal((await f.request()).status, 503);
  f.route.expires_at = now / 1000 + 120;
  f.env.ORIGIN_ID = "b".repeat(64);
  assert.equal((await f.request()).status, 503);
  assert.equal(f.calls.storage, reads);
});

test("promotion and rollback partition cached HTML and retain old assets", async () => {
  const f = fixture();
  f.release(first, { "index.html": "old HTML", "assets/old-hash.js": "old JS" });
  f.release(second, { "index.html": "new HTML", "assets/new-hash.js": "new JS" });
  assert.equal(await (await f.request()).text(), "old HTML");
  f.route.active_release_id = second;
  f.route.ready_releases = [second, first];
  f.route.revision++;
  assert.equal(await (await f.request()).text(), "new HTML");
  assert.equal(await (await f.request("/assets/old-hash.js")).text(), "old JS");
  assert.equal(await (await f.request(`/_gregale/releases/${first}/index.html`)).text(), "old HTML");
  f.route.active_release_id = first;
  f.route.revision++;
  assert.equal(await (await f.request()).text(), "old HTML");
  f.route.ready_releases = [first];
  assert.equal((await f.request(`/_gregale/releases/${second}/index.html`)).status, 404);
});

test("SPA navigation, missing assets, HEAD, and conditional requests", async () => {
  const f = fixture();
  f.release(first, { "index.html": "HTML", "app.js": "script" }, true);
  const response = await f.request();
  const etag = response.headers.get("ETag");
  assert.equal(response.headers.get("Cache-Control"), "private, max-age=0, must-revalidate");
  assert.equal((await f.request("/", { headers: { "If-None-Match": etag } })).status, 304);
  assert.equal(await (await f.request("/", { method: "HEAD" })).text(), "");
  assert.equal(await (await f.request("/dashboard", { headers: { Accept: "text/html" } })).text(), "HTML");
  assert.equal((await f.request("/missing.js", { headers: { Accept: "text/html" } })).status, 404);
  assert.equal((await f.request("/dashboard", { headers: { Accept: "application/json" } })).status, 404);
  assert.equal((await f.request("/_gregale/releases/../../anything")).status >= 400, true);
});
