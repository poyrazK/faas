// Debugger journey entrypoint (tests/journeys/debugger_test.go).
//
// Wraps the probe app's request handler before server.js starts it:
//   - every request carrying a traceparent exports OTLP spans for it to the
//     guest trace bridge (127.0.0.1:4318): a SQL query, a Redis command and
//     an outbound HTTPS call, each with literals the debugger must scrub;
//   - GET / waits debug-delay.txt milliseconds first, so a second release
//     with a larger delay is a latency regression of the same route.
// The planted values are synthetic; the journey asserts none of them is
// ever shown by `gregale debug`.

import http from "node:http";
import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";

const here = new URL(".", import.meta.url);
const delayMs = Number(readFileSync(new URL("debug-delay.txt", here), "utf8").trim()) || 0;

const attr = (key, value) => ({
  key,
  value: typeof value === "number" ? { intValue: value } : { stringValue: String(value) },
});

function span(traceId, parentId, name, attributes, start, end) {
  return {
    traceId,
    spanId: randomBytes(8).toString("hex"),
    parentSpanId: parentId,
    name,
    kind: 3,
    startTimeUnixNano: String(start),
    endTimeUnixNano: String(end),
    attributes,
    status: { code: 1 },
  };
}

function exportSpans(traceId, parentId) {
  const start = BigInt(Date.now()) * 1000000n;
  const end = start + 2000000n;
  const spans = [
    span(traceId, parentId, "SELECT accounts", [
      attr("db.system", "postgresql"),
      attr("db.operation", "SELECT"),
      attr("db.sql.table", "accounts"),
      attr("db.statement", "SELECT * FROM accounts WHERE email = 'e2e-planted-email@example.com' AND token = $$e2e-planted-dollar$$"),
      attr("user.email", "e2e-planted-email@example.com"),
    ], start, end),
    span(traceId, parentId, "GET", [
      attr("db.system", "redis"),
      attr("db.statement", "GET session:e2e-planted-redis-key"),
    ], start, end),
    span(traceId, parentId, "GET api.example.com", [
      attr("http.request.method", "GET"),
      attr("url.full", "https://api.example.com/v1/customers/e2e-planted-path?token=e2e-planted-query"),
      attr("server.address", "api.example.com"),
      attr("http.response.status_code", 200),
      attr("http.request.header.authorization", "Bearer e2e-planted-header"),
    ], start, end),
  ];
  const body = JSON.stringify({
    resourceSpans: [{
      resource: { attributes: [attr("service.name", "e2e-debugger-probe"), attr("e2e.resource", "e2e-planted-resource")] },
      scopeSpans: [{ scope: { name: "e2e" }, spans }],
    }],
  });
  const req = http.request({
    host: "127.0.0.1",
    port: 4318,
    path: "/v1/traces",
    method: "POST",
    headers: { "content-type": "application/json", "content-length": Buffer.byteLength(body) },
  }, (res) => {
    res.resume();
    if (res.statusCode !== 200) console.log(`otlp export answered ${res.statusCode}`);
  });
  req.on("error", (err) => console.log(`otlp export failed: ${err.message}`));
  req.end(body);
}

const createServer = http.createServer;
http.createServer = (...args) => {
  const handler = args.findIndex((a) => typeof a === "function");
  if (handler >= 0) {
    const inner = args[handler];
    args[handler] = (req, res) => {
      const tp = /^00-([0-9a-f]{32})-([0-9a-f]{16})-/.exec(req.headers.traceparent || "");
      if (tp) exportSpans(tp[1], tp[2]);
      const path = new URL(req.url, "http://probe.invalid").pathname;
      if (path === "/") console.log("debugger probe request for e2e-planted-log@example.com");
      if (path === "/" && delayMs > 0) {
        setTimeout(() => inner(req, res), delayMs);
        return;
      }
      return inner(req, res);
    };
  }
  return createServer.apply(http, args);
};

await import("./server.js");
