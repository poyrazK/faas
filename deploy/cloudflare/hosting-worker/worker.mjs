// Internal static hosting. Every request, including a cache hit, must pass
// the deployment switch, verified Access identity, and an enabled route.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const DIGEST = /^[0-9a-f]{64}$/;
const MAX_FILES = 1000; // api.HostingFilesMax
const MAX_MANIFEST_BYTES = 512 * 1024;
const MAX_ROUTE_BYTES = 8 * 1024; // api.HostingDurableRouteMaxBytes
const ROUTE_CACHE_TTL_MS = 60_000; // api.HostingRouteCacheTTL
const MAX_DOMAIN_HOSTNAME_BYTES = 64; // api.HostingDomainHostnameMaxBytes
// Release configuration limits mirror pkg/api/limits.go.
const MAX_CONFIG_BYTES = 32 * 1024; // HostingConfigMaxBytes
const MAX_CONFIG_REDIRECTS = 32; // HostingConfigRedirectsMax
const MAX_CONFIG_HEADER_RULES = 16; // HostingConfigHeaderRulesMax
const MAX_CONFIG_HEADERS_PER_RULE = 8; // HostingConfigHeadersPerRuleMax
const MAX_CONFIG_HEADER_VALUE_BYTES = 2048; // HostingConfigHeaderValueMaxBytes
let accessKeys;

function problem(status, code) {
  return Response.json({ status, code }, {
    status, headers: { "Cache-Control": "no-store" },
  });
}

function decodeBase64URL(value) {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error("invalid encoding");
  return Uint8Array.from(atob(value.replace(/-/g, "+").replace(/_/g, "/")), c => c.charCodeAt(0));
}

async function verifyAccess(request, env, deps) {
  const team = env.ACCESS_TEAM_DOMAIN;
  if (!/^[a-z0-9-]+\.cloudflareaccess\.com$/.test(team || "") || !env.ACCESS_AUD) return false;
  const allowed = new Set((env.INTERNAL_EMAILS || "").split(",").map(s => s.trim().toLowerCase()).filter(Boolean));
  if (!allowed.size) return false;
  const token = request.headers.get("Cf-Access-Jwt-Assertion") || "";
  if (token.length > 16384) return false;
  const parts = token.split(".");
  if (parts.length !== 3) return false;
  try {
    const header = JSON.parse(new TextDecoder().decode(decodeBase64URL(parts[0])));
    const claims = JSON.parse(new TextDecoder().decode(decodeBase64URL(parts[1])));
    const now = deps.now() / 1000;
    const audiences = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
    if (header.alg !== "RS256" || typeof header.kid !== "string" || header.kid.length > 128 ||
        claims.iss !== `https://${team}` || !audiences.includes(env.ACCESS_AUD) ||
        !Number.isFinite(claims.exp) || claims.exp <= now ||
        (claims.nbf !== undefined && (!Number.isFinite(claims.nbf) || claims.nbf > now)) ||
        (claims.iat !== undefined && (!Number.isFinite(claims.iat) || claims.iat > now + 30)) ||
        typeof claims.email !== "string" || !allowed.has(claims.email.toLowerCase())) return false;
    const keys = await getAccessKeys(team, deps);
    const jwk = keys.find(k => k.kid === header.kid && k.kty === "RSA" && (!k.alg || k.alg === "RS256"));
    if (!jwk) return false;
    const key = await crypto.subtle.importKey("jwk", jwk, { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" }, false, ["verify"]);
    return await crypto.subtle.verify("RSASSA-PKCS1-v1_5", key, decodeBase64URL(parts[2]), new TextEncoder().encode(`${parts[0]}.${parts[1]}`));
  } catch { return false; }
}

async function getAccessKeys(team, deps) {
  if (accessKeys?.team === team && accessKeys.expires > deps.now()) return accessKeys.keys;
  const response = await deps.fetch(`https://${team}/cdn-cgi/access/certs`, { redirect: "error", signal: AbortSignal.timeout(5000) });
  if (!response.ok) throw new Error("Access keys unavailable");
  const text = await response.text();
  if (text.length > 65536) throw new Error("invalid key set");
  const data = JSON.parse(text);
  if (!Array.isArray(data.keys) || data.keys.length > 16) throw new Error("invalid key set");
  accessKeys = { team, expires: deps.now() + 60000, keys: data.keys };
  return data.keys;
}

function validPath(path) {
  return path.length > 0 && path.length <= 512 && /^[A-Za-z0-9._~/-]+$/.test(path) &&
    path !== "gregale-hosting.json" && !path.startsWith("_gregale/") && !path.split("/").some(s => !s || s === "." || s === "..");
}

function validRoute(route, hostname, now, originID) {
  return route?.version === 1 && route.enabled === true && route.hostname === hostname &&
    UUID.test(route.site_id) && UUID.test(route.active_release_id) && DIGEST.test(originID || "") && route.origin_id === originID &&
    Number.isSafeInteger(route.revision) && route.revision > 0 &&
    Number.isFinite(route.expires_at) && route.expires_at > now / 1000 &&
    route.expires_at <= now / 1000 + 150 && // finite API lease, with limited clock skew
    Array.isArray(route.ready_releases) && route.ready_releases.length <= 10 &&
    route.ready_releases.every(id => UUID.test(id)) && route.ready_releases.includes(route.active_release_id);
}

function validDurableRoute(route, hostname, originID) {
  return route?.version === 2 && route.enabled === true && route.deleted === false &&
    route.expires_at === 0 && Number.isSafeInteger(route.generation) && route.generation > 0 &&
    route.hostname === hostname && hostname.startsWith(`${route.site_id}.`) &&
    UUID.test(route.site_id) && UUID.test(route.active_release_id) &&
    DIGEST.test(originID || "") && route.origin_id === originID &&
    Number.isSafeInteger(route.revision) && route.revision > 0 &&
    Array.isArray(route.ready_releases) && route.ready_releases.length <= 10 &&
    new Set(route.ready_releases).size === route.ready_releases.length &&
    route.ready_releases.every(id => UUID.test(id)) && route.ready_releases.includes(route.active_release_id);
}

async function durableRouteFor(url, env, ctx, deps) {
  if (!UUID.test(url.hostname.split(".")[0]) || !DIGEST.test(env.ORIGIN_ID || "")) return undefined;
  const key = new Request(`https://${url.hostname}/_gregale/cache/routes/v2/${env.ORIGIN_ID}`);
  let response = await deps.cache?.match(key);
  const until = Number(response?.headers.get("X-Gregale-Route-Until"));
  if (!(until > deps.now() && until <= deps.now() + ROUTE_CACHE_TTL_MS)) response = null;
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/routes/${url.hostname}.json`);
    if (object && object.size > MAX_ROUTE_BYTES) throw new Error("invalid route head");
    const body = object ? await new Response(object.body).text() : null;
    if (body && body.length > MAX_ROUTE_BYTES) throw new Error("invalid route head");
    response = new Response(body, { status: body === null ? 204 : 200, headers: {
      "Cache-Control": "public, max-age=60", "X-Gregale-Route-Until": String(deps.now() + ROUTE_CACHE_TTL_MS),
    } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  return response.status === 204 ? undefined : JSON.parse(await response.text());
}

function canonicalHostname(hostname, domain) {
  return /^[a-z0-9.-]+$/.test(domain || "") && UUID.test(hostname.split(".")[0]) &&
    hostname === `${hostname.split(".")[0]}.${domain}`;
}

function previewHostname(hostname, domain) {
  const label = hostname.split('.')[0];
  return label.startsWith('p-') && UUID.test(label.slice(2)) && hostname === `${label}.${domain}`;
}

async function previewRouteFor(url, env, ctx, deps) {
  const key = new Request(`https://${url.hostname}/_gregale/cache/previews/v1/${env.ORIGIN_ID}`);
  let response = await deps.cache?.match(key);
  const until = Number(response?.headers.get('X-Gregale-Route-Until'));
  if (!(until > deps.now() && until <= deps.now() + ROUTE_CACHE_TTL_MS)) response = null;
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/previews/${url.hostname}.json`);
    if (!object || object.size > MAX_ROUTE_BYTES) throw new Error('preview unavailable');
    const body = await new Response(object.body).text();
    if (body.length > MAX_ROUTE_BYTES) throw new Error('invalid preview head');
    response = new Response(body, { headers: { 'Cache-Control': 'public, max-age=60', 'X-Gregale-Route-Until': String(deps.now() + ROUTE_CACHE_TTL_MS) } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  const p = JSON.parse(await response.text());
  // Recheck expiry and revocation on every request, including warm HEAD/304.
  if (p.version !== 1 || !UUID.test(p.id) || p.hostname !== url.hostname ||
      p.hostname !== `p-${p.id}.${env.HOSTING_DOMAIN}` || !UUID.test(p.site_id) || !UUID.test(p.release_id) ||
      p.canonical_hostname !== `${p.site_id}.${env.HOSTING_DOMAIN}` || !DIGEST.test(env.ORIGIN_ID || '') || p.origin_id !== env.ORIGIN_ID ||
      p.generation !== 1 || p.revoked !== false || !Number.isSafeInteger(p.expires_at) || p.expires_at * 1000 <= deps.now()) throw new Error('preview unavailable');
  const head = await durableRouteFor(new URL(`https://${p.canonical_hostname}/`), env, ctx, deps);
  if (head?.preview_allowed !== true || head.site_id !== p.site_id) throw new Error('preview site unavailable');
  const route = { ...head, enabled: true, active_release_id: p.release_id, ready_releases: [p.release_id] };
  if (!validDurableRoute(route, p.canonical_hostname, env.ORIGIN_ID)) throw new Error('preview site unavailable');
  return route;
}

async function domainBindingFor(url, env, ctx, deps) {
  if (!/^[a-z0-9.-]+$/.test(url.hostname) || url.hostname.length > MAX_DOMAIN_HOSTNAME_BYTES) throw new Error("invalid hostname");
  const key = new Request(`https://${url.hostname}/_gregale/cache/domains/v1/${env.ORIGIN_ID}`);
  let response = await deps.cache?.match(key);
  const until = Number(response?.headers.get("X-Gregale-Route-Until"));
  if (!(until > deps.now() && until <= deps.now() + ROUTE_CACHE_TTL_MS)) response = null;
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/domains/${url.hostname}.json`);
    if (!object || object.size > MAX_ROUTE_BYTES) throw new Error("domain unavailable");
    const body = await new Response(object.body).text();
    if (body.length > MAX_ROUTE_BYTES) throw new Error("invalid domain head");
    response = new Response(body, { headers: { "Cache-Control": "public, max-age=60", "X-Gregale-Route-Until": String(deps.now() + ROUTE_CACHE_TTL_MS) } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  const binding = JSON.parse(await response.text());
  if (binding.version !== 1 || !UUID.test(binding.id) || !UUID.test(binding.site_id) ||
      binding.hostname !== url.hostname || binding.canonical_hostname !== `${binding.site_id}.${env.HOSTING_DOMAIN}` ||
      !DIGEST.test(binding.origin_id || "") || binding.origin_id !== env.ORIGIN_ID || !DIGEST.test(binding.access_aud || "") ||
      !Number.isSafeInteger(binding.generation) || binding.generation < 1 || binding.enabled !== true || binding.deleted !== false) throw new Error("domain unavailable");
  return binding;
}

async function manifestFor(requestURL, env, ctx, deps, site, release) {
  const key = new Request(`https://${requestURL.hostname}/_gregale/cache/manifests/${site}/${release}`);
  let response = await deps.cache?.match(key);
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/published/${site}/${release}/manifest.json`);
    if (!object || object.size > MAX_MANIFEST_BYTES) throw new Error("manifest unavailable");
    response = new Response(object.body, { headers: { "Cache-Control": "public, max-age=31536000, immutable" } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  const text = await response.text();
  if (text.length > MAX_MANIFEST_BYTES) throw new Error("invalid manifest");
  const manifest = JSON.parse(text);
  if (!Array.isArray(manifest.files) || !manifest.files.length || manifest.files.length > MAX_FILES) throw new Error("invalid manifest");
  const files = new Map();
  for (const file of manifest.files) {
    if (!validPath(file.path) || files.has(file.path) || !DIGEST.test(file.sha256) ||
        !Number.isSafeInteger(file.size) || file.size < 0 || file.size > 32 * 1024 * 1024 ||
        typeof file.content_type !== "string" || file.content_type.length > 128 || /[\r\n]/.test(file.content_type)) throw new Error("invalid file");
    files.set(file.path, file);
  }
  if (!files.has("index.html")) throw new Error("invalid manifest");
  if (manifest.config !== undefined) {
    validateReleaseConfig(manifest.config, files);
    if (env.FAAS_FRONTEND_HOSTING_CONFIG_ENABLED !== "true") throw new Error("release configuration disabled");
  }
  return { files, spa: manifest.spa_fallback === true, config: manifest.config };
}

// Mirrors pkg/hosting/release_config.go; bounded release metadata, no regex rules.
function configPath(p) {
  if (p === "/") return true;
  return typeof p === "string" && p.startsWith("/") && p.length <= 512 &&
    p.slice(1).replace(/\/$/, "") !== "_gregale" && validPath(p.slice(1).replace(/\/$/, ""));
}
const CONFIG_HEADERS = new Set(["content-security-policy", "content-security-policy-report-only", "permissions-policy", "referrer-policy", "x-frame-options", "cross-origin-opener-policy", "cross-origin-embedder-policy", "cross-origin-resource-policy"]);
export function validateReleaseConfig(config, files) {
  if (!config || typeof config !== "object" || Array.isArray(config) || config.version !== 1 ||
      Object.keys(config).some(k => !["version", "redirects", "headers", "not_found"].includes(k)) ||
      new TextEncoder().encode(JSON.stringify(config)).length > MAX_CONFIG_BYTES) throw new Error("invalid release configuration");
  const redirects = config.redirects ?? [];
  const headers = config.headers ?? [];
  if (!Array.isArray(redirects) || redirects.length > MAX_CONFIG_REDIRECTS || !Array.isArray(headers) || headers.length > MAX_CONFIG_HEADER_RULES) throw new Error("invalid rules");
  const graph = new Map();
  for (const r of redirects) {
    if (!r || Object.keys(r).some(k => !["from", "to", "status"].includes(k)) || !configPath(r.from) || !configPath(r.to) || ![301,302,307,308].includes(r.status) || graph.has(r.from)) throw new Error("invalid redirect");
    graph.set(r.from, r.to);
  }
  for (const source of graph.keys()) {
    const seen = new Set();
    for (let p = source; graph.has(p); p = graph.get(p)) {
      if (seen.has(p)) throw new Error("redirect cycle"); seen.add(p);
    }
  }
  const seen = new Set();
  for (const rule of headers) {
    if (!rule || Object.keys(rule).some(k => !["path", "values"].includes(k)) || typeof rule.path !== "string" ||
        !configPath(rule.path.replace(/\*$/, "")) || (rule.path.includes("*") && !rule.path.endsWith("/*")) || seen.has(rule.path) ||
        !rule.values || typeof rule.values !== "object" || Array.isArray(rule.values) || Object.keys(rule.values).length < 1 || Object.keys(rule.values).length > MAX_CONFIG_HEADERS_PER_RULE) throw new Error("invalid header rule");
    seen.add(rule.path);
    for (const [name,value] of Object.entries(rule.values)) {
      if (!CONFIG_HEADERS.has(name) || typeof value !== "string" || value.length < 1 || value.length > MAX_CONFIG_HEADER_VALUE_BYTES || value.trim() !== value || /[^\x20-\x7e]/.test(value)) throw new Error("invalid response header");
    }
  }
  if (config.not_found !== undefined && config.not_found !== "") {
    if (typeof config.not_found !== "string" || !validPath(config.not_found)) throw new Error("invalid 404 path");
    const file = files?.get(config.not_found);
    if (files && (!file || file.content_type.split(";")[0].toLowerCase() !== "text/html")) throw new Error("missing 404 HTML");
  }
  return config;
}

function configuredHeaders(config, path, headers) {
  for (const rule of config?.headers || []) {
    if (rule.path === path || (rule.path.endsWith("/*") && path.startsWith(rule.path.slice(0,-1)))) {
      for (const [name,value] of Object.entries(rule.values)) headers.set(name,value);
    }
  }
}

async function resolveFile(request, url, env, ctx, deps, route) {
  const manifestURL = new URL(url);
  manifestURL.hostname = route.hostname;
  let path = decodeURIComponent(url.pathname).replace(/^\//, "");
  let release = route.active_release_id;
  let pinned = false;
  if (path.startsWith("_gregale/releases/")) {
    const parts = path.split("/");
    release = parts[2];
    path = parts.slice(3).join("/");
    pinned = true;
    if (!UUID.test(release) || !route.ready_releases.includes(release)) return null;
  }
  const requestPath = `/${path}`;
  if (!path || path.endsWith("/")) path += "index.html";
  if (!validPath(path)) return null;
  const manifest = await manifestFor(manifestURL, env, ctx, deps, route.site_id, release);
  const redirect = !pinned && manifest.config?.redirects?.find(r => r.from === requestPath);
  if (redirect) return { redirect, release, config: manifest.config, requestPath };
  let file = manifest.files.get(path) || manifest.files.get(`${path}/index.html`);
  // Retain fingerprinted assets referenced by older HTML documents. HTML
  // itself only falls back within its selected release.
  if (!file && !pinned && !path.endsWith(".html")) {
    for (const old of route.ready_releases) {
      if (old === release) continue;
      const oldManifest = await manifestFor(manifestURL, env, ctx, deps, route.site_id, old);
      const candidate = oldManifest.files.get(path);
      if (candidate && !candidate.content_type.toLowerCase().startsWith("text/html")) return { file: candidate, release: old, pinned: false, config: oldManifest.config, requestPath };
    }
  }
  if (!file && manifest.spa && request.headers.get("Accept")?.includes("text/html") && !path.split("/").at(-1).includes(".")) {
    file = manifest.files.get("index.html");
  }
  if (!file && manifest.config?.not_found && request.headers.get("Accept")?.includes("text/html")) {
    return { file: manifest.files.get(manifest.config.not_found), release, pinned, config: manifest.config, requestPath, status: 404 };
  }
  return file ? { file, release, pinned, config: manifest.config, requestPath } : null;
}

async function serveFile(request, url, env, ctx, deps, route, selected, trace) {
  const { file, release, pinned, config, requestPath, status = 200, redirect } = selected;
  if (redirect) {
    const headers = new Headers({ Location: redirect.to, "Cache-Control": "private, no-store", "X-Gregale-Release": release, "X-Content-Type-Options": "nosniff" });
    configuredHeaders(config, requestPath, headers);
    if (route.version === 2) headers.set("X-Gregale-Route-Generation", String(route.generation));
    if (route.domain_generation) headers.set("X-Gregale-Domain-Generation", String(route.domain_generation));
    return new Response(null, { status: redirect.status, headers });
  }
  if (status === 404) trace.code = "hosting_file_not_found";
  const etag = `"sha256:${file.sha256}"`;
  const headers = new Headers({
    "Content-Type": file.content_type, "Content-Length": String(file.size), ETag: etag,
    "Cache-Control": "private, max-age=0, must-revalidate", "X-Content-Type-Options": "nosniff",
    "X-Gregale-Release": release, "Referrer-Policy": "strict-origin-when-cross-origin",
  });
  configuredHeaders(config, requestPath, headers);
  if (route.version === 2) headers.set("X-Gregale-Route-Generation", String(route.generation));
  if (route.domain_generation) headers.set("X-Gregale-Domain-Generation", String(route.domain_generation));
  // The browser revalidates internal content; the shared edge cache remains
  // usable only behind fresh identity and routing checks on every request.
  if (status === 200 && request.headers.get("If-None-Match") === etag) {
    headers.delete("Content-Length");
    return new Response(null, { status: 304, headers });
  }
  if (request.method === "HEAD") return new Response(null, { status, headers });
  const key = new Request(`https://${route.hostname}/_gregale/cache/files/${route.site_id}/${release}/${file.sha256}/${file.path}`);
  let response = await deps.cache?.match(key);
  headers.set("X-Gregale-Cache", response ? "HIT" : "MISS");
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/published/${route.site_id}/${release}/files/${file.path}`);
    if (!object || object.size !== file.size) { trace.code = "hosting_artifact_unavailable"; return problem(503, "hosting_artifact_unavailable"); }
    response = new Response(object.body, { headers: { "Cache-Control": "public, max-age=31536000, immutable" } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  if (pinned) headers.set("Content-Location", `/_gregale/releases/${release}/${file.path}`);
  return new Response(response.body, { status, headers });
}

async function dispatch(request, env, ctx, deps, trace) {
  const fail = (status, code) => { trace.code = code; return problem(status, code); };
  if (env.FAAS_FRONTEND_HOSTING_ENABLED !== "true") return fail(404, "not_found");
  if (!env.ROUTES || !env.ARTIFACTS) return fail(503, "hosting_unconfigured");
  try {
    const url = new URL(request.url);
    const isPreview = previewHostname(url.hostname, env.HOSTING_DOMAIN);
    if (env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED === 'true' && !env.HOSTING_DOMAIN) return fail(503, 'hosting_unconfigured');
    if (isPreview) {
      if (env.FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED !== 'true') return fail(404, 'frontend_hosting_previews_disabled');
      if (env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED !== 'true') return fail(503, 'hosting_route_unavailable');
      if (!(await verifyAccess(request, env, deps))) return fail(403, 'internal_access_required');
      if (!new Set(['GET', 'HEAD']).has(request.method)) return fail(405, 'method_not_allowed');
      const route = await previewRouteFor(url, env, ctx, deps);
      const selected = await resolveFile(request, url, env, ctx, deps, route);
      if (!selected) return fail(404, 'hosting_file_not_found');
      // Preview traffic is intentionally excluded from production traffic metrics.
      return await serveFile(request, url, env, ctx, deps, route, selected, trace);
    }
    let binding;
    let routingURL = url;
    if (env.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED === "true" && !env.HOSTING_DOMAIN) return fail(503, "hosting_unconfigured");
    if (env.HOSTING_DOMAIN && !canonicalHostname(url.hostname, env.HOSTING_DOMAIN)) {
      if (env.FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED !== "true") return fail(404, "frontend_hosting_domains_disabled");
      if (env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED !== "true") return fail(503, "hosting_route_unavailable");
      const assertion = request.headers.get("Cf-Access-Jwt-Assertion") || "";
      if (assertion.length > 16384 || assertion.split(".").length !== 3) return fail(403, "internal_access_required");
      binding = await domainBindingFor(url, env, ctx, deps);
      routingURL = new URL(`https://${binding.canonical_hostname}/`);
    }
    if (!(await verifyAccess(request, binding ? { ...env, ACCESS_AUD: binding.access_aud } : env, deps))) return fail(403, "internal_access_required");
    // Attribute only authenticated requests to a canonical site or a verified alias.
    trace.site = binding?.site_id || (canonicalHostname(url.hostname, env.HOSTING_DOMAIN) ? url.hostname.split('.')[0] : '');
    if (!new Set(["GET", "HEAD"]).has(request.method)) return fail(405, "method_not_allowed");
    let route;
    if (env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED === "true") {
      route = await durableRouteFor(routingURL, env, ctx, deps);
      // An existing disabled/deleted/invalid head never falls back to a stale KV lease.
      if (route !== undefined && !validDurableRoute(route, routingURL.hostname, env.ORIGIN_ID)) return fail(503, "hosting_route_unavailable");
    }
    if (binding && (route === undefined || route.site_id !== binding.site_id)) return fail(503, "hosting_route_unavailable");
    if (binding) route.domain_generation = binding.generation;
    if (route === undefined) {
      route = await env.ROUTES.get(`site:${url.hostname}`, { type: "json", cacheTtl: 60 });
      if (!validRoute(route, url.hostname, deps.now(), env.ORIGIN_ID)) return fail(503, "hosting_route_unavailable");
    }
    const selected = await resolveFile(request, url, env, ctx, deps, route);
    if (!selected) return fail(404, "hosting_file_not_found");
    return await serveFile(request, url, env, ctx, deps, route, selected, trace);
  } catch {
    return fail(503, "hosting_unavailable");
  }
}

// Analytics Engine enqueues these writes in the provider runtime. There is no
// telemetry HTTP fetch, body buffering, waitUntil dependency, or Gregale RPC.
export async function serve(request, env, ctx, deps = { fetch, now: Date.now, cache: globalThis.caches?.default }) {
  const trace = { site: '', code: '' };
  const response = await dispatch(request, env, ctx, deps, trace);
  if (env.FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED === 'true' && UUID.test(trace.site) && DIGEST.test(env.ORIGIN_ID || '')) {
    try {
      const release = response.headers.get('X-Gregale-Release') || '';
      const size = Number(response.headers.get('Content-Length'));
      const bytes = response.status === 200 && request.method === 'GET' && UUID.test(release) && Number.isSafeInteger(size) && size >= 0 && size <= 32 * 1024 * 1024 ? size : 0;
      const cache = response.headers.get('X-Gregale-Cache');
      env.ANALYTICS?.writeDataPoint({
        indexes: [trace.site],
        blobs: [env.ORIGIN_ID, UUID.test(release) ? release : '', trace.code || (response.status === 304 ? 'not_modified' : 'ok'), cache === 'HIT' ? 'hit' : cache === 'MISS' ? 'miss' : 'none', 'v1'],
        doubles: [1, bytes, response.status],
      });
    } catch { /* Observability must never interrupt static serving. */ }
  }
  return response;
}

export default { fetch: serve };
