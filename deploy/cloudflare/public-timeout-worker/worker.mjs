/*
 * Gregale public-timeout edge adapter.
 *
 * Cloudflare Free may replace an origin 502/504 body with its own generic
 * error page. The gateway marks its own request-budget 504s with
 * X-Faas-Error-Code, so this Worker can safely reconstruct only that known
 * platform error and leave genuine CDN/origin failures untouched.
 */

const ERROR_CODE = "request_budget_exceeded";
const ERROR_CODE_HEADER = "X-Faas-Error-Code";
const REQUEST_ID_HEADER = "X-Faas-Request-Id";
const ORIGINAL_STATUS_HEADER = "X-Faas-Edge-Original-Status";
const ORIGIN_504_TRANSPORT_STATUS = 409;
const PROBLEM_CONTENT_TYPE = "application/problem+json";
const PROBLEM_TYPE = "https://docs.gregale.dev/errors/request-budget-exceeded";

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

// Exported separately so the contract tests can exercise the Worker with a
// stub fetch implementation. Cloudflare invokes the default export below.
export async function handleRequest(request, env, fetchImpl = globalThis.fetch) {
  const originHost = String(env?.ORIGIN_HOSTNAME || "").trim();
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
    upstream = await fetchImpl(request, {
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

  const encodedOrigin504 =
    upstream.status === ORIGIN_504_TRANSPORT_STATUS &&
    upstream.headers.get(ORIGINAL_STATUS_HEADER) === "504";
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
