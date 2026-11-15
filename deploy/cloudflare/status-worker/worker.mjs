// status.gregale.dev — off-platform status page and outside probe
// (ADR-20261115090000).
//
// The page itself is static assets, served by Cloudflare without running
// this script (asset requests are free and do not count toward the Workers
// daily request limit). Status data is read straight from the R2 bucket's
// custom domain, also without this script. The only code that runs is the
// one-minute cron below, so an outage traffic spike cannot exhaust the free
// plan's 100k requests/day and take the status page down with the platform.

export const PROBE_KEY = "v1/probe.json";
export const PROBE_CACHE_CONTROL = "public, max-age=15";
export const DEFAULT_TIMEOUT_MS = 8000;
const MAX_TARGETS = 10;

export const DEFAULT_TARGETS = [
  { id: "api", name: "API", url: "https://api.gregale.dev/readyz", components: ["api_console"] },
];

export function parseTargets(raw) {
  if (raw == null || String(raw).trim() === "") return DEFAULT_TARGETS;
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed) || parsed.length === 0 || parsed.length > MAX_TARGETS) {
    throw new Error(`PROBE_TARGETS must be a JSON array of 1-${MAX_TARGETS} targets`);
  }
  return parsed.map((target) => {
    const url = new URL(target.url);
    if (url.protocol !== "https:" || typeof target.id !== "string" || !/^[a-z0-9_-]{1,32}$/.test(target.id)) {
      throw new Error("each probe target needs an id ([a-z0-9_-]) and an https url");
    }
    return {
      id: target.id,
      name: String(target.name || target.id),
      url: url.toString(),
      components: Array.isArray(target.components) ? target.components.map(String) : [],
      expect: target.expect === "any_below_500" ? "any_below_500" : "2xx",
    };
  });
}

export async function probeTarget(target, fetchImpl, clock = Date.now, timeoutMs = DEFAULT_TIMEOUT_MS) {
  const started = clock();
  try {
    const response = await fetchImpl(target.url, {
      method: "GET",
      redirect: "manual",
      headers: { "user-agent": "gregale-status-probe/1", "cache-control": "no-cache" },
      cf: { cacheTtl: 0 },
      signal: AbortSignal.timeout(timeoutMs),
    });
    const ok = target.expect === "any_below_500" ? response.status < 500 : response.status >= 200 && response.status < 300;
    try {
      await response.body?.cancel();
    } catch {
      // The body is not needed; failing to discard it is harmless.
    }
    return { ok, status: response.status, latency_ms: clock() - started, error: ok ? null : `HTTP ${response.status}` };
  } catch (error) {
    const timedOut = error?.name === "TimeoutError" || error?.name === "AbortError";
    return { ok: false, status: 0, latency_ms: clock() - started, error: timedOut ? "timeout" : "unreachable" };
  }
}

export async function readPrevious(bucket) {
  try {
    const object = await bucket.get(PROBE_KEY);
    if (!object) return null;
    const doc = await object.json();
    return doc?.schema === 1 && Array.isArray(doc.checks) ? doc : null;
  } catch {
    return null;
  }
}

export async function runProbe(env, fetchImpl = fetch, clock = Date.now) {
  const targets = parseTargets(env.PROBE_TARGETS);
  const previous = await readPrevious(env.STATUS_BUCKET);
  const before = new Map((previous?.checks || []).map((check) => [check.id, check]));
  const results = await Promise.all(targets.map((target) => probeTarget(target, fetchImpl, clock)));
  const checkedAt = new Date(clock()).toISOString();
  const checks = targets.map((target, i) => {
    const result = results[i];
    const last = before.get(target.id);
    const consecutive = result.ok ? 0 : (last && !last.ok ? Number(last.consecutive_failures) || 0 : 0) + 1;
    return {
      id: target.id,
      name: target.name,
      components: target.components,
      ok: result.ok,
      status: result.status,
      latency_ms: result.latency_ms,
      error: result.error,
      consecutive_failures: consecutive,
      failing_since: result.ok ? null : last && !last.ok && last.failing_since ? last.failing_since : checkedAt,
    };
  });
  const doc = { schema: 1, checked_at: checkedAt, checks };
  await env.STATUS_BUCKET.put(PROBE_KEY, JSON.stringify(doc), {
    httpMetadata: { contentType: "application/json", cacheControl: PROBE_CACHE_CONTROL },
  });
  return doc;
}

export default {
  // Only reached for paths with no static asset. Kept trivial on purpose.
  async fetch() {
    return new Response("Not found", { status: 404, headers: { "cache-control": "public, max-age=300" } });
  },
  async scheduled(_event, env, ctx) {
    ctx.waitUntil(runProbe(env));
  },
};
