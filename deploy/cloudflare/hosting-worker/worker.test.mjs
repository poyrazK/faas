import test, { before } from "node:test";
import assert from "node:assert/strict";
import { createHash, webcrypto } from "node:crypto";
import { readFileSync } from "node:fs";
import { serve, validateReleaseConfig } from "./worker.mjs";

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
  function release(id, files, spa = false, config) {
    const manifest = { spa_fallback: spa, files: [], ...(config ? { config } : {}) };
    for (const [path, body] of Object.entries(files)) {
      manifest.files.push({ path, size: Buffer.byteLength(body), sha256: createHash("sha256").update(body).digest("hex"), content_type: path.endsWith(".html") ? "text/html" : "text/javascript" });
      objects.set(`hosting/published/${site}/${id}/files/${path}`, body);
    }
    objects.set(`hosting/published/${site}/${id}/manifest.json`, JSON.stringify(manifest));
  }
  async function request(path = "/", options = {}) {
    const headers = new Headers(options.headers);
    if (!headers.has("Cf-Access-Jwt-Assertion")) headers.set("Cf-Access-Jwt-Assertion", validToken);
    const { hostname: requestHost = hostname, ...init } = options;
    const response = await serve(new Request(`https://${requestHost}${path}`, { ...init, headers }), env, { waitUntil(p) { pending.push(p); } }, deps);
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

function customFixture() {
  const f = fixture();
  f.durable();
  f.env.HOSTING_DOMAIN = 'hosting.example';
  f.env.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED = 'true';
  const custom = 'portal.customer.example';
  const binding = { version: 1, id: '44444444-4444-4444-8444-444444444444', hostname: custom, site_id: site, canonical_hostname: hostname, origin_id: origin, access_aud: 'b'.repeat(64), generation: 1, enabled: true, deleted: false };
  f.objects.set(`hosting/domains/${custom}.json`, JSON.stringify(binding));
  return { ...f, binding, custom, customRequest(path = '/', options = {}) { return f.request(path, { ...options, hostname: custom }); } };
}

test('custom domains require their own flag and Access audience on warm hits, HEAD, and 304', async () => {
  const f = customFixture(); f.release(first, { 'index.html': 'private custom site' });
  const headers = { 'Cf-Access-Jwt-Assertion': await token({ aud: [f.binding.access_aud] }) };
  const initial = await f.customRequest('/', { headers }); assert.equal(initial.status, 200);
  assert.equal(initial.headers.get('X-Gregale-Domain-Generation'), '1');
  assert.equal((await f.customRequest()).status, 403);
  assert.equal((await f.customRequest('/', { method: 'HEAD' })).status, 403);
  assert.equal((await f.customRequest('/', { headers: { 'If-None-Match': initial.headers.get('ETag') } })).status, 403);
  const warm = await f.customRequest('/', { headers }); assert.equal(warm.headers.get('X-Gregale-Cache'), 'HIT');
  assert.equal((await f.customRequest('/', { headers: { ...headers, 'If-None-Match': initial.headers.get('ETag') } })).status, 304);
  f.env.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED = 'false';
  assert.equal((await f.customRequest('/', { headers })).status, 404);
  assert.equal((await f.request()).status, 200);
});

test('custom domains always follow canonical suspension, rollback, and retirement state', async () => {
  const f = customFixture(); f.release(first, { 'index.html': 'first' }); f.release(second, { 'index.html': 'second' });
  const headers = { 'Cf-Access-Jwt-Assertion': await token({ aud: [f.binding.access_aud] }) };
  assert.equal(await (await f.customRequest('/', { headers })).text(), 'first');
  f.objects.set(`hosting/routes/${hostname}.json`, JSON.stringify({ ...f.durable(), generation: 2, active_release_id: second, ready_releases: [second] }));
  f.advance(61000);
  assert.equal(await (await f.customRequest('/', { headers })).text(), 'second');
  assert.equal((await f.customRequest(`/_gregale/releases/${first}/index.html`, { headers })).status, 404);
  f.objects.set(`hosting/routes/${hostname}.json`, JSON.stringify({ ...f.durable(), generation: 3, enabled: false }));
  f.advance(61000);
  assert.equal((await f.customRequest('/', { headers })).status, 503);
  assert.equal(f.calls.routes, 0);
});

test('custom-domain tombstones deny cached files and never fall back to KV', async () => {
  const f = customFixture(); f.release(first, { 'index.html': 'private site' });
  const headers = { 'Cf-Access-Jwt-Assertion': await token({ aud: [f.binding.access_aud] }) };
  await f.customRequest('/', { headers });
  f.objects.set(`hosting/domains/${f.custom}.json`, JSON.stringify({ ...f.binding, generation: 2, enabled: false, deleted: true }));
  f.advance(61000);
  assert.equal((await f.customRequest('/', { headers })).status, 503);
  assert.equal((await f.request()).status, 200);
  assert.equal(f.calls.routes, 0);
});

test('custom domains reuse immutable canonical bytes after their own authentication and survive API outages', async () => {
  const f = customFixture(); f.release(first, { 'index.html': 'shared private bytes', 'cold.js': 'cold asset' });
  await f.request();
  const headers = { 'Cf-Access-Jwt-Assertion': await token({ aud: [f.binding.access_aud], exp: now / 1000 + 3600 }) };
  const cached = await f.customRequest('/', { headers });
  assert.equal(cached.headers.get('X-Gregale-Cache'), 'HIT');
  f.advance(20 * 60 * 1000);
  assert.equal(await (await f.customRequest('/', { headers })).text(), 'shared private bytes');
  const cold = await f.customRequest('/cold.js', { headers });
  assert.equal(cold.headers.get('X-Gregale-Cache'), 'MISS');
  assert.equal(await cold.text(), 'cold asset');
  assert.equal(f.calls.routes, 0);
});

test('foreign and malformed domain bindings cannot reuse canonical authentication or content caches', async () => {
  for (const patch of [{ origin_id: 'c'.repeat(64) }, { canonical_hostname: `other.${hostname}` }, { site_id: first }, { enabled: false }, { access_aud: 'hosting' }, { generation: 1.5 }]) {
    const f = customFixture(); f.release(first, { 'index.html': 'secret' });
    f.objects.set(`hosting/domains/${f.custom}.json`, JSON.stringify({ ...f.binding, ...patch }));
    const headers = { 'Cf-Access-Jwt-Assertion': await token({ aud: [f.binding.access_aud] }) };
    assert.equal((await f.customRequest('/', { headers })).status, 503);
    assert.equal(f.calls.routes, 0);
  }
});

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

test("durable generation is reported after routing on GET, HEAD, and revalidation", async () => {
  const f = fixture();
  f.release(first, { "index.html": "HTML" });
  f.durable({ generation: 7 });
  const response = await f.request();
  assert.equal(response.headers.get("X-Gregale-Route-Generation"), "7");
  assert.equal((await f.request("/", { method: "HEAD" })).headers.get("X-Gregale-Route-Generation"), "7");
  const conditional = await f.request("/", { headers: { "If-None-Match": response.headers.get("ETag") } });
  assert.equal(conditional.status, 304);
  assert.equal(conditional.headers.get("X-Gregale-Route-Generation"), "7");
  f.durable({ generation: 8 });
  f.advance(60_001);
  assert.equal((await f.request()).headers.get("X-Gregale-Route-Generation"), "8");
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
  const hidden = await f.request();
  assert.equal(hidden.status, 404);
  assert.deepEqual(await hidden.json(), { status: 404, code: 'not_found' });
  assert.equal(hidden.headers.get('Cache-Control'), 'no-store');
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

test('analytics is separately gated, private, and does not buffer or alter serving', async () => {
  const f = fixture(); f.env.HOSTING_DOMAIN = 'hosting.example';
  const points = []; f.env.ANALYTICS = { writeDataPoint(point) { points.push(point); } };
  f.release(first, { 'index.html': 'private bytes' });
  await f.request('/?secret=query'); assert.equal(points.length, 0);
  f.env.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED = 'true';
  const cold = await f.request('/'); assert.equal(await cold.text(), 'private bytes');
  const warm = await f.request('/'); assert.equal(warm.headers.get('X-Gregale-Cache'), 'HIT');
  await f.request('/', { method: 'HEAD' });
  await f.request('/', { headers: { 'If-None-Match': warm.headers.get('ETag') } });
  assert.equal(points.length, 4);
  assert.deepEqual(points[0], { indexes: [site], blobs: [origin, first, 'ok', 'hit', 'v1'], doubles: [1, 13, 200] });
  assert.deepEqual(points.slice(2).map(p => p.doubles), [[1,0,200],[1,0,304]]);
  const serialized = JSON.stringify(points);
  for (const secret of ['operator@example.com', 'private bytes', 'query', validToken, 'index.html']) assert.ok(!serialized.includes(secret));
  f.env.ANALYTICS.writeDataPoint = () => { throw new Error('provider outage'); };
  assert.equal((await f.request('/')).status, 200);
  delete f.env.ANALYTICS;
  assert.equal((await f.request('/')).status, 200);
});

test('analytics counts authorized serving failures but excludes access denials and disabled serving', async () => {
  const f = fixture(); f.env.HOSTING_DOMAIN = 'hosting.example'; f.env.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED = 'true';
  const points = []; f.env.ANALYTICS = { writeDataPoint(p) { points.push(p); } };
  await f.request('/', { headers: { 'Cf-Access-Jwt-Assertion': 'forged' } }); assert.equal(points.length, 0);
  f.route.enabled = false;
  assert.equal((await f.request('/')).status, 503);
  assert.deepEqual(points[0], { indexes:[site], blobs:[origin,'','hosting_route_unavailable','none','v1'], doubles:[1,0,503] });
  f.env.FAAS_FRONTEND_HOSTING_ENABLED = 'false'; await f.request('/'); assert.equal(points.length, 1);
});

test('custom-domain analytics attributes to its bound canonical site', async () => {
  const f = customFixture(); f.env.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED = 'true';
  const points=[]; f.env.ANALYTICS={writeDataPoint(p){points.push(p);}};
  f.release(first, {'index.html':'alias'});
  const headers={'Cf-Access-Jwt-Assertion':await token({aud:[f.binding.access_aud]})};
  await f.customRequest('/',{headers});
  assert.equal(points.length,1); assert.deepEqual(points[0].indexes,[site]);
  assert.ok(!JSON.stringify(points).includes(f.custom));
});

test('release configuration agrees with the Go shared contract', () => {
  const cases = JSON.parse(readFileSync(new URL('./release-config-cases.json', import.meta.url)));
  const files = new Map([['index.html',{content_type:'text/html'}],['404.html',{content_type:'text/html; charset=utf-8'}],['app.js',{content_type:'text/javascript'}]]);
  for (const c of cases) {
    if (c.valid) assert.doesNotThrow(() => validateReleaseConfig(c.config,files),c.name);
    else assert.throws(() => validateReleaseConfig(c.config,files),undefined,c.name);
  }
});

const releaseConfig = (to='/new') => ({version:1, redirects:[{from:'/old',to,status:308}], headers:[{path:'/*',values:{'content-security-policy':"default-src 'self'",'permissions-policy':'camera=()'}},{path:'/private/*',values:{'referrer-policy':'no-referrer'}},{path:'/private/page',values:{'x-frame-options':'DENY'}}], not_found:'404.html'});
const configFiles = {'index.html':'home','404.html':'missing page','private/page/index.html':'page','app.js':'script'};

test('redirects precede files, preserve chosen status, discard queries, and cannot redirect pinned release URLs', async () => {
  const f=fixture();f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';
  f.release(first,{...configFiles,old:'old file'},false,releaseConfig());
  for (const method of ['GET','HEAD']) {
    const r=await f.request('/old?secret=discard',{method});
    assert.equal(r.status,308);assert.equal(r.headers.get('Location'),'/new');assert.equal(await r.text(),'');
    assert.equal(r.headers.get('Cache-Control'),'private, no-store');
    assert.equal(r.headers.get('Content-Security-Policy'),"default-src 'self'");
  }
  const pinned=await f.request(`/_gregale/releases/${first}/old`);
  assert.equal(pinned.status,200);assert.equal(await pinned.text(),'old file');
});

test('headers match request paths on GET, HEAD, 304, and custom 404 while platform headers remain enforced', async () => {
  const f=fixture();f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';f.release(first,configFiles,false,releaseConfig());
  const r=await f.request('/private/page');
  for (const response of [r,await f.request('/private/page',{method:'HEAD'}),await f.request('/private/page',{headers:{'If-None-Match':r.headers.get('ETag')}})]) {
    assert.equal(response.headers.get('X-Frame-Options'),'DENY');assert.equal(response.headers.get('Referrer-Policy'),'no-referrer');
    assert.equal(response.headers.get('X-Content-Type-Options'),'nosniff');assert.equal(response.headers.get('Cache-Control'),'private, max-age=0, must-revalidate');
  }
  assert.equal((await f.request('/privateer/page',{headers:{Accept:'text/html'}})).headers.get('Referrer-Policy'),'strict-origin-when-cross-origin');
  const notFound=await f.request('/missing',{headers:{Accept:'text/html'}});
  assert.equal(notFound.status,404);assert.equal(await notFound.text(),'missing page');
  assert.equal(notFound.headers.get('Content-Security-Policy'),"default-src 'self'");
  const head=await f.request('/missing',{method:'HEAD',headers:{Accept:'text/html','If-None-Match':notFound.headers.get('ETag')}});
  assert.equal(head.status,404);assert.equal(await head.text(),'');assert.equal(head.headers.get('Content-Length'),'12');
  assert.equal((await f.request('/missing',{headers:{Accept:'text/html','If-None-Match':notFound.headers.get('ETag')}})).status,404);
  assert.equal((await f.request('/missing',{headers:{Accept:'application/json'}})).headers.get('Content-Type'),'application/json');
});

test('configuration flags, Access, and suspension gate redirects and warm cached content', async () => {
  const f=fixture();f.durable();f.release(first,configFiles,false,releaseConfig());
  assert.equal((await f.request()).status,503);
  f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';await f.request();
  assert.equal((await f.request()).headers.get('X-Gregale-Cache'),'HIT');
  for (const path of ['/old','/','/missing']) assert.equal((await f.request(path,{headers:{'Cf-Access-Jwt-Assertion':'forged',Accept:'text/html'}})).status,403);
  f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='false';
  for (const path of ['/old','/','/missing']) assert.equal((await f.request(path,{headers:{Accept:'text/html'}})).status,503);
  f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';f.durable({enabled:false,generation:2});f.advance(61000);
  assert.equal((await f.request('/old')).status,503);
});

test('promotion and rollback restore configuration, and retained assets keep their own release headers', async () => {
  const f=fixture();f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';
  f.release(first,{...configFiles,'old.js':'old script'},false,releaseConfig('/first'));
  const changed=releaseConfig('/second');changed.headers[0].values['permissions-policy']='camera=(self)';
  f.release(second,configFiles,false,changed);
  assert.equal((await f.request('/old')).headers.get('Location'),'/first');const initial=await f.request();
  f.route.active_release_id=second;f.route.ready_releases=[second,first];f.route.revision++;
  assert.equal((await f.request('/old')).headers.get('Location'),'/second');
  assert.equal((await f.request()).headers.get('Permissions-Policy'),'camera=(self)');
  const revalidated=await f.request('/',{headers:{'If-None-Match':initial.headers.get('ETag')}});
  assert.equal(revalidated.status,304);assert.equal(revalidated.headers.get('Permissions-Policy'),'camera=(self)');
  assert.equal((await f.request('/old.js')).headers.get('Permissions-Policy'),'camera=()');
  f.route.active_release_id=first;f.route.revision++;
  assert.equal((await f.request('/old')).headers.get('Location'),'/first');
  assert.equal((await f.request()).headers.get('Permissions-Policy'),'camera=()');
  assert.equal((await f.request('/gregale-hosting.json')).status,404);
});

test('SPA fallback precedes the custom 404 and custom domains use the same protected configuration', async () => {
  const f=customFixture();f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';f.release(first,configFiles,true,releaseConfig());
  const headers={'Cf-Access-Jwt-Assertion':await token({aud:[f.binding.access_aud]}),Accept:'text/html'};
  assert.equal(await (await f.customRequest('/dashboard',{headers})).text(),'home');
  const missing=await f.customRequest('/missing.js',{headers});assert.equal(missing.status,404);assert.equal(await missing.text(),'missing page');
  const redirect=await f.customRequest('/old',{headers});assert.equal(redirect.status,308);assert.equal(redirect.headers.get('Location'),'/new');
  assert.equal((await f.customRequest('/old')).status,403);
});

test('verified but never-promoted artifacts remain unreachable outside routing inventory', async () => {
  const f=fixture();
  f.release(first,{'index.html':'live','old.js':'retained'});
  f.release(second,{'index.html':'staged','new.js':'not live'});
  assert.equal(await (await f.request()).text(),'live');
  assert.equal((await f.request('/new.js')).status,404);
  assert.equal((await f.request(`/_gregale/releases/${second}/index.html`)).status,404);
  f.route.active_release_id=second;f.route.ready_releases=[first,second];f.route.revision++;
  assert.equal(await (await f.request()).text(),'staged');
  assert.equal(await (await f.request('/new.js')).text(),'not live');
  assert.equal(await (await f.request('/old.js')).text(),'retained');
});

function previewFixture(overrides={}) {
 const f=fixture(); const head=f.durable({preview_allowed:true, enabled:false, active_release_id:'',ready_releases:[]});
 f.env.HOSTING_DOMAIN='hosting.example';f.env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED='true';
 const id='55555555-5555-4555-8555-555555555555';const host=`p-${id}.hosting.example`;
 const p={version:1,id,hostname:host,site_id:site,canonical_hostname:hostname,origin_id:origin,release_id:second,expires_at:Math.floor(now/1000)+300,generation:1,revoked:false,...overrides};
 const key=`hosting/previews/${host}.json`;f.objects.set(key,JSON.stringify(p));
 return {...f,p,head,key,host,previewRequest(path='/',options={}){return f.request(path,{...options,hostname:host});}};
}

test('private previews serve a staged first release without production or retained-release exposure',async()=>{
 const f=previewFixture();f.release(first,{'index.html':'live','old.js':'old'});f.release(second,{'index.html':'preview','new.js':'new'});
 assert.equal(await(await f.previewRequest()).text(),'preview');assert.equal((await f.request()).status,503);
 assert.equal((await f.previewRequest('/old.js')).status,404);
 assert.equal((await f.previewRequest(`/_gregale/releases/${first}/index.html`)).status,404);
 assert.equal(await(await f.previewRequest(`/_gregale/releases/${second}/index.html`)).text(),'preview');
 // A promotion/rollback changes canonical routing, never the preview binding.
 const live={...f.head,enabled:true,active_release_id:first,ready_releases:[first],generation:2};f.objects.set(`hosting/routes/${hostname}.json`,JSON.stringify(live));f.advance(61000);
 assert.equal(await(await f.request()).text(),'live');assert.equal(await(await f.previewRequest()).text(),'preview');
 assert.equal((await f.request(`/_gregale/releases/${second}/index.html`)).status,404);assert.equal(f.calls.routes,0);
});

test('preview flags and Access are enforced before cold or warm preview lookups',async()=>{
 const f=previewFixture();f.release(second,{'index.html':'preview'});
 f.env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED='false';assert.equal((await f.previewRequest()).status,404);assert.equal(f.calls.storage,0);assert.equal(f.calls.cache,0);
 f.env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED='true';assert.equal((await f.previewRequest('/',{headers:{'Cf-Access-Jwt-Assertion':''}})).status,403);assert.equal(f.calls.storage,0);
 const warm=await f.previewRequest();assert.equal(warm.status,200);
 for(const method of ['GET','HEAD'])assert.equal((await f.previewRequest('/',{method,headers:{'Cf-Access-Jwt-Assertion':await token({email:'outsider@example.com'}),'If-None-Match':warm.headers.get('ETag')}})).status,403);
 f.env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED='false';assert.equal((await f.previewRequest('/',{method:'HEAD'})).status,404);
 f.env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED='true';f.env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED='false';assert.equal((await f.previewRequest()).status,503);assert.equal(f.calls.routes,0);
});

test('warm preview expiry denies GET HEAD and 304 without waiting for route cache eviction',async()=>{
 const f=previewFixture({expires_at:Math.floor(now/1000)+10});f.release(second,{'index.html':'preview'});
 const warm=await f.previewRequest();assert.equal(warm.status,200);f.advance(11000);
 for(const method of ['GET','HEAD'])assert.equal((await f.previewRequest('/',{method,headers:{'If-None-Match':warm.headers.get('ETag')}})).status,503);
 assert.equal(f.calls.routes,0);
});

test('preview tombstones and canonical suspension/deletion stop warm content after bounded head refresh',async()=>{
 for(const change of ['revoke','suspend','delete','missing-permission']){
  const f=previewFixture();f.release(second,{'index.html':'preview'});const warm=await f.previewRequest();assert.equal(warm.status,200);
  if(change==='revoke')f.objects.set(f.key,JSON.stringify({...f.p,revoked:true,generation:2}));
  else f.objects.set(`hosting/routes/${hostname}.json`,JSON.stringify({...f.head,preview_allowed:change==='delete',deleted:change==='delete',generation:2}));
  f.advance(61000);
  for(const method of ['GET','HEAD'])assert.equal((await f.previewRequest('/',{method,headers:{'If-None-Match':warm.headers.get('ETag')}})).status,503,change);
  assert.equal(f.calls.routes,0);
 }
});

test('invalid preview identities fail closed and preview traffic is excluded from production analytics',async()=>{
 for(const overrides of [{site_id:first},{origin_id:'b'.repeat(64)},{canonical_hostname:`${first}.hosting.example`},{id:first},{release_id:'invalid'},{expires_at:Infinity},{generation:2},{revoked:true}]){
  const f=previewFixture(overrides);f.release(second,{'index.html':'preview'});assert.equal((await f.previewRequest()).status,503);assert.equal(f.calls.routes,0);
 }
 const f=previewFixture();f.release(second,{'index.html':'preview'});const points=[];f.env.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED='true';f.env.ANALYTICS={writeDataPoint:p=>points.push(p)};
 assert.equal((await f.previewRequest()).status,200);assert.equal(points.length,0);
});

test('preview configuration uses its own release with SPA, redirects, headers, and config serving gate',async()=>{
 const f=previewFixture();f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='true';
 f.release(second,{'index.html':'preview','404.html':'preview missing'},true,{version:1,redirects:[{from:'/old',to:'/new',status:308}],headers:[{path:'/*',values:{'x-frame-options':'DENY'}}],not_found:'404.html'});
 assert.equal((await f.previewRequest('/old')).headers.get('Location'),'/new');
 const home=await f.previewRequest('/dashboard',{headers:{Accept:'text/html'}});assert.equal(await home.text(),'preview');assert.equal(home.headers.get('X-Frame-Options'),'DENY');
 assert.equal(await(await f.previewRequest('/missing.js',{headers:{Accept:'text/html'}})).text(),'preview missing');
 f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED='false';assert.equal((await f.previewRequest()).status,503);
});

// Shared with pkg/hosting/localpreview. This exercises the production Worker,
// including Access verification; local preview does not emulate Access or CDN.
const localPreviewCases = JSON.parse(readFileSync(new URL('./local-preview-cases.json', import.meta.url), 'utf8'));
for (const scenario of localPreviewCases.scenarios) {
  test(`local preview contract: ${scenario.name}`, async () => {
    const f = fixture();
    f.env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED = 'true';
    const files = localPreviewCases.files.map(file => {
      f.objects.set(`hosting/published/${site}/${first}/files/${file.path}`, file.body);
      return {path: file.path, size: Buffer.byteLength(file.body), sha256: createHash('sha256').update(file.body).digest('hex'), content_type: file.content_type};
    });
    f.objects.set(`hosting/published/${site}/${first}/manifest.json`, JSON.stringify({files, spa_fallback: !!scenario.spa, ...(scenario.config ? {config: scenario.config} : {})}));
    for (const item of scenario.requests) {
      const headers = {...item.headers};
      const tagFile = item.if_none_match_file || item.weak_if_none_match_file;
      if (tagFile) headers['If-None-Match'] = (item.weak_if_none_match_file ? 'W/' : '') + `"sha256:${files.find(file => file.path === tagFile).sha256}"`;
      const response = await f.request(item.path, {method: item.method || 'GET', headers});
      assert.equal(response.status, item.status, item.name);
      for (const [key, value] of Object.entries(item.response_headers || {})) assert.equal(response.headers.get(key), value, `${item.name}: ${key}`);
      for (const key of item.absent_headers || []) assert.equal(response.headers.get(key), null, `${item.name}: absent ${key}`);
      // HTTP transports suppress HEAD bodies, including platform errors.
      const body = item.method === 'HEAD' ? '' : await response.text();
      if (item.body !== undefined) assert.equal(body, item.body, item.name);
      if (item.code) assert.equal(JSON.parse(body).code, item.code, item.name);
    }
  });
}
