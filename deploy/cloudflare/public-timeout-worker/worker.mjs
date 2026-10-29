/*
 * Gregale public edge adapter.
 *
 * Cloudflare Free may replace an origin 502/504 body with its own generic
 * error page. The gateway marks its own request-budget 504s with
 * X-Faas-Error-Code, so this Worker can safely reconstruct only that known
 * platform error and leave genuine CDN/origin failures untouched. The same
 * route applies the frontend document security policy at the CDN boundary.
 */

import appHosts from "./app-hosts.json" with { type: "json" };

const ERROR_CODE = "request_budget_exceeded";
const ERROR_CODE_HEADER = "X-Faas-Error-Code";
const REQUEST_ID_HEADER = "X-Faas-Request-Id";
const ORIGINAL_STATUS_HEADER = "X-Faas-Edge-Original-Status";
const ORIGIN_504_TRANSPORT_STATUS = 409;
// Statuses this Worker can restore from the 409 transport. The gateway
// encodes 504 for every Worker subrequest and 502 only when the Worker
// advertises it here, so a Worker that predates 502 support never receives a
// transported 502 it would hand to customers as a raw 409.
const RESTORABLE_HEADER = "X-Faas-Edge-Restorable";
const RESTORABLE_STATUSES = "502,504";
const PROBLEM_CONTENT_TYPE = "application/problem+json";
const PROBLEM_TYPE = "https://gregale.dev/docs/errors/request-budget-exceeded";
const DEFAULT_FRONTEND_HOSTNAME = "gregale.dev";
const DEFAULT_APPS_DOMAIN = "gregale.dev";
const FRONTEND_CSP = [
  "default-src 'self'",
  "base-uri 'self'",
  "frame-ancestors 'none'",
  "object-src 'none'",
  "script-src 'self' 'unsafe-inline'",
  "script-src-attr 'none'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  "connect-src 'self' https://api.gregale.dev wss://api.gregale.dev",
  "form-action 'self'",
  "manifest-src 'self'",
  "worker-src 'self' blob:",
  "upgrade-insecure-requests",
].join("; ");

const HOP_BY_HOP_HEADERS = [
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "content-length",
  "content-encoding",
];

function removeHopByHopHeaders(headers) {
  for (const name of HOP_BY_HOP_HEADERS) {
    headers.delete(name);
  }
  return headers;
}

function requestID(request, headers) {
  return (
    headers.get(REQUEST_ID_HEADER) ||
    request.headers.get(REQUEST_ID_HEADER) ||
    crypto.randomUUID()
  );
}

function fallbackProblem(id) {
  return JSON.stringify({
    type: PROBLEM_TYPE,
    title: "Request budget exceeded",
    status: 504,
    code: ERROR_CODE,
    detail:
      "the request exceeded its wall-clock budget while capacity was becoming ready",
    request_id: id,
  });
}

function isStructuredBudgetBody(body, contentType) {
  if (!contentType.toLowerCase().includes(PROBLEM_CONTENT_TYPE)) {
    return false;
  }
  try {
    return JSON.parse(body)?.code === ERROR_CODE;
  } catch (_) {
    return false;
  }
}

function withFrontendSecurityHeaders(response) {
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.toLowerCase().includes("text/html")) {
    return response;
  }
  const headers = new Headers(response.headers);
  headers.set("Content-Security-Policy", FRONTEND_CSP);
  headers.set("X-Content-Type-Options", "nosniff");
  headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()");
  headers.set("X-Frame-Options", "DENY");
  headers.delete("Access-Control-Allow-Origin");
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

function hostList(value) {
  return new Set(
    String(value || "")
      .split(",")
      .map((host) => host.trim().toLowerCase())
      .filter(Boolean),
  );
}

const APP_SLUG = new RegExp(appHosts.slug_pattern);
const NOT_APP_LABELS = new Set([...appHosts.reserved_slugs, ...appHosts.platform_labels]);

// isAppHost reports whether host is <app slug>.<apps domain>: one label that
// is a valid, unreserved app name (pkg/api ValidAppSlug and IsReservedAppSlug,
// mirrored in app-hosts.json). Platform hosts, Gregale-owned names, deeper
// names and anything outside the apps domain are not apps.
function isAppHost(host, appsDomain) {
  const suffix = `.${appsDomain}`;
  if (!appsDomain || !host.endsWith(suffix)) {
    return false;
  }
  const label = host.slice(0, -suffix.length);
  return (
    APP_SLUG.test(label) &&
    !appHosts.invalid_prefixes.some((prefix) => label.startsWith(prefix)) &&
    !NOT_APP_LABELS.has(label)
  );
}

// originFor picks the origin a request resolves to. App hosts selected by
// APP_EDGE_HOSTS go to the compute-node app edge (ADR-1343): the Cloudflare
// Tunnel behind APP_EDGE_ORIGIN_HOSTNAME, an in-zone CNAME to
// <tunnel-id>.cfargotunnel.com. APP_EDGE_HOSTS lists exact hostnames (the
// pilot), or "*" for every app host. APP_EDGE_EXCLUDE_HOSTS keeps exact
// hostnames on the control plane either way. Everything else, and every host
// while APP_EDGE_ORIGIN_HOSTNAME or APP_EDGE_HOSTS is unset, keeps the
// control-plane origin.
function originFor(requestHost, env) {
  const controlPlane = String(env?.ORIGIN_HOSTNAME || "").trim();
  const appEdgeOrigin = String(env?.APP_EDGE_ORIGIN_HOSTNAME || "").trim();
  if (!appEdgeOrigin || hostList(env?.APP_EDGE_EXCLUDE_HOSTS).has(requestHost)) {
    return controlPlane;
  }
  const selected = hostList(env?.APP_EDGE_HOSTS);
  if (selected.has(requestHost)) {
    return appEdgeOrigin;
  }
  const appsDomain = String(env?.APPS_DOMAIN || DEFAULT_APPS_DOMAIN).trim().toLowerCase();
  if (selected.has("*") && isAppHost(requestHost, appsDomain)) {
    return appEdgeOrigin;
  }
  return controlPlane;
}

// Exported separately so the contract tests can exercise the Worker with a
// stub fetch implementation. Cloudflare invokes the default export below.
export async function handleRequest(request, env, fetchImpl = globalThis.fetch) {
  const requestHost = new URL(request.url).hostname.toLowerCase();
  const frontendHost = String(env?.FRONTEND_HOSTNAME || DEFAULT_FRONTEND_HOSTNAME)
    .trim()
    .toLowerCase();
  if (requestHost === frontendHost) {
    try {
      return withFrontendSecurityHeaders(await fetchImpl(request));
    } catch (_) {
      return new Response("frontend origin unavailable", {
        status: 502,
        headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
      });
    }
  }

  const originHost = originFor(requestHost, env);
  if (!originHost) {
    return new Response("public-timeout-worker: ORIGIN_HOSTNAME is not configured", {
      status: 500,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });
  }

  let upstream;
  try {
    // resolveOverride changes only DNS resolution; the original URL/Host is
    // preserved so gatewayd-internal still routes by the customer hostname.
    const headers = new Headers(request.headers);
    headers.set(RESTORABLE_HEADER, RESTORABLE_STATUSES);
    upstream = await fetchImpl(new Request(request, { headers }), {
      cf: { resolveOverride: originHost },
    });
  } catch (_) {
    // Do not claim a platform budget error when the Worker itself cannot
    // reach the origin. This is a genuine edge failure and remains a 502.
    return new Response("origin unavailable", {
      status: 502,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });
  }

  const transportedStatus =
    upstream.status === ORIGIN_504_TRANSPORT_STATUS
      ? upstream.headers.get(ORIGINAL_STATUS_HEADER)
      : null;
  if (transportedStatus === "502") {
    // A platform "App connection failed" problem or an app's own 502.
    // Recreating the response keeps Cloudflare from substituting its generic
    // 502 page, so API clients keep the problem body and request id.
    const headers = removeHopByHopHeaders(new Headers(upstream.headers));
    headers.delete(ORIGINAL_STATUS_HEADER);
    return new Response(await upstream.text(), {
      status: 502,
      statusText: "Bad Gateway",
      headers,
    });
  }
  const encodedOrigin504 = transportedStatus === "504";
  if (upstream.status !== 504 && !encodedOrigin504) {
    return upstream;
  }

  const headers = removeHopByHopHeaders(new Headers(upstream.headers));
  headers.delete(ORIGINAL_STATUS_HEADER);
  const contentType = headers.get("content-type") || "";
  const marked = headers.get(ERROR_CODE_HEADER) === ERROR_CODE;
  let body;
  if (!marked) {
    // Some TLS/proxy frontends drop X-Faas-Error-Code while preserving the
    // origin's canonical problem envelope. Require the exact platform error
    // code; generic/unmarked failures retain their original body and status
    // so CDN errors are never reclassified.
    body = await upstream.text();
    if (!isStructuredBudgetBody(body, contentType)) {
      // Returning the fetch Response object lets Cloudflare apply its generic
      // 504 page after the Worker. Recreate the same response so an app's own
      // status, headers, and body survive the edge unchanged.
      return new Response(body, {
        status: 504,
        statusText: "Gateway Timeout",
        headers,
      });
    }
  }
  const id = requestID(request, headers);
  headers.set(ERROR_CODE_HEADER, ERROR_CODE);
  headers.set(REQUEST_ID_HEADER, id);
  headers.set("cache-control", "no-store");
  headers.set("content-type", PROBLEM_CONTENT_TYPE);

  // The origin normally already supplied the RFC 7807 body. If an upstream
  // proxy replaced it, reconstruct a bounded, stable envelope from the
  // marker header instead of forwarding a generic CDN page.
  body ||= await upstream.text();
  const responseBody = isStructuredBudgetBody(body, contentType)
    ? body
    : fallbackProblem(id);

  return new Response(responseBody, { status: 504, headers });
}

export default {
  fetch(request, env) {
    return handleRequest(request, env);
  },
};
