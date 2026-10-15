// Internal static hosting. Every request, including a cache hit, must pass
// the deployment switch, verified Access identity, and an enabled route.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const DIGEST = /^[0-9a-f]{64}$/;
const MAX_FILES = 1000; // api.HostingFilesMax
const MAX_MANIFEST_BYTES = 512 * 1024;
const MAX_ROUTE_BYTES = 8 * 1024; // api.HostingDurableRouteMaxBytes
const ROUTE_CACHE_TTL_MS = 60_000; // api.HostingRouteCacheTTL
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
    !path.startsWith("_gregale/") && !path.split("/").some(s => !s || s === "." || s === "..");
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
  return { files, spa: manifest.spa_fallback === true };
}

async function resolveFile(request, url, env, ctx, deps, route) {
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
  if (!path || path.endsWith("/")) path += "index.html";
  if (!validPath(path)) return null;
  const manifest = await manifestFor(url, env, ctx, deps, route.site_id, release);
  let file = manifest.files.get(path) || manifest.files.get(`${path}/index.html`);
  // Retain fingerprinted assets referenced by older HTML documents. HTML
  // itself only falls back within its selected release.
  if (!file && !pinned && !path.endsWith(".html")) {
    for (const old of route.ready_releases) {
      if (old === release) continue;
      const candidate = (await manifestFor(url, env, ctx, deps, route.site_id, old)).files.get(path);
      if (candidate && !candidate.content_type.toLowerCase().startsWith("text/html")) return { file: candidate, release: old, pinned: false };
    }
  }
  if (!file && manifest.spa && request.headers.get("Accept")?.includes("text/html") && !path.split("/").at(-1).includes(".")) {
    file = manifest.files.get("index.html");
  }
  return file ? { file, release, pinned } : null;
}

async function serveFile(request, url, env, ctx, deps, route, selected) {
  const { file, release, pinned } = selected;
  const etag = `"sha256:${file.sha256}"`;
  const headers = new Headers({
    "Content-Type": file.content_type, "Content-Length": String(file.size), ETag: etag,
    "Cache-Control": "private, max-age=0, must-revalidate", "X-Content-Type-Options": "nosniff",
    "X-Gregale-Release": release, "Referrer-Policy": "strict-origin-when-cross-origin",
  });
  // The browser revalidates internal content; the shared edge cache remains
  // usable only behind fresh identity and routing checks on every request.
  if (request.headers.get("If-None-Match") === etag) {
    headers.delete("Content-Length");
    return new Response(null, { status: 304, headers });
  }
  if (request.method === "HEAD") return new Response(null, { headers });
  const key = new Request(`https://${url.hostname}/_gregale/cache/files/${route.site_id}/${release}/${file.sha256}/${file.path}`);
  let response = await deps.cache?.match(key);
  headers.set("X-Gregale-Cache", response ? "HIT" : "MISS");
  if (!response) {
    const object = await env.ARTIFACTS.get(`hosting/published/${route.site_id}/${release}/files/${file.path}`);
    if (!object || object.size !== file.size) return problem(503, "hosting_artifact_unavailable");
    response = new Response(object.body, { headers: { "Cache-Control": "public, max-age=31536000, immutable" } });
    if (deps.cache) ctx.waitUntil(deps.cache.put(key, response.clone()));
  }
  if (pinned) headers.set("Content-Location", `/_gregale/releases/${release}/${file.path}`);
  return new Response(response.body, { headers });
}

export async function serve(request, env, ctx, deps = { fetch, now: Date.now, cache: globalThis.caches?.default }) {
  if (env.FAAS_FRONTEND_HOSTING_ENABLED !== "true") return problem(404, "frontend_hosting_disabled");
  if (!env.ROUTES || !env.ARTIFACTS) return problem(503, "hosting_unconfigured");
  if (!(await verifyAccess(request, env, deps))) return problem(403, "internal_access_required");
  if (!new Set(["GET", "HEAD"]).has(request.method)) return problem(405, "method_not_allowed");
  try {
    const url = new URL(request.url);
    let route;
    if (env.FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED === "true") {
      route = await durableRouteFor(url, env, ctx, deps);
      // An existing disabled/deleted/invalid head never falls back to a stale KV lease.
      if (route !== undefined && !validDurableRoute(route, url.hostname, env.ORIGIN_ID)) return problem(503, "hosting_route_unavailable");
    }
    if (route === undefined) {
      route = await env.ROUTES.get(`site:${url.hostname}`, { type: "json", cacheTtl: 60 });
      if (!validRoute(route, url.hostname, deps.now(), env.ORIGIN_ID)) return problem(503, "hosting_route_unavailable");
    }
    const selected = await resolveFile(request, url, env, ctx, deps, route);
    if (!selected) return problem(404, "hosting_file_not_found");
    return await serveFile(request, url, env, ctx, deps, route, selected);
  } catch {
    return problem(503, "hosting_unavailable");
  }
}

export default { fetch: serve };
