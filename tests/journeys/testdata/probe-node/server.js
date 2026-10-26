// Probe app for the customer journey suite (tests/journeys).
//
// Zero dependencies so the build is fast and cannot fail on a registry.
// Every route is deterministic so a journey can assert on it:
//   GET  /            version baked into version.txt (proves which revision serves)
//   GET  /healthz     readiness
//   GET  /env?key=K   presence and SHA-256 of E2E_* variables, never the value
//   GET  /log?marker= writes one runtime log line carrying the marker
//   POST /cron        counts cron deliveries and logs them
//   GET  /cron-hits   reads the counter
//   GET  /sse         three server-sent events, then closes

import http from "node:http";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

const version = readFileSync(new URL("./version.txt", import.meta.url), "utf8").trim();
const started = Date.now();
let cronHits = 0;

function json(res, status, body) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify(body));
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://probe.invalid");
  switch (url.pathname) {
    case "/":
      return json(res, 200, { app: "gregale-e2e-probe", version, uptime_ms: Date.now() - started });
    case "/healthz":
      return json(res, 200, { ok: true });
    case "/env": {
      const key = url.searchParams.get("key") || "";
      if (!/^E2E_[A-Z0-9_]{1,40}$/.test(key)) {
        return json(res, 400, { error: "key must match E2E_[A-Z0-9_]+" });
      }
      const value = process.env[key];
      if (value === undefined) {
        return json(res, 200, { key, present: false });
      }
      const sha256 = createHash("sha256").update(value).digest("hex");
      return json(res, 200, { key, present: true, sha256 });
    }
    case "/log": {
      const marker = url.searchParams.get("marker") || "";
      if (!/^[a-z0-9-]{1,64}$/.test(marker)) {
        return json(res, 400, { error: "marker must match [a-z0-9-]{1,64}" });
      }
      console.log(`e2e-probe-marker ${marker}`);
      return json(res, 200, { logged: marker });
    }
    case "/cron":
      cronHits += 1;
      console.log(`e2e-probe-cron method=${req.method} hits=${cronHits}`);
      return json(res, 200, { hits: cronHits });
    case "/cron-hits":
      return json(res, 200, { hits: cronHits });
    case "/sse": {
      res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-store" });
      let sent = 0;
      const timer = setInterval(() => {
        sent += 1;
        res.write(`id: ${sent}\ndata: tick ${sent}\n\n`);
        if (sent === 3) {
          clearInterval(timer);
          res.end();
        }
      }, 200);
      req.on("close", () => clearInterval(timer));
      return;
    }
    default:
      return json(res, 404, { error: "not found" });
  }
});

server.listen(Number(process.env.PORT) || 8080, () => {
  console.log(`gregale-e2e-probe ${version} listening`);
});
