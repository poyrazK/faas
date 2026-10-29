// Probe app for the customer journey suite (tests/journeys).
//
// Zero dependencies so the build is fast and cannot fail on a registry.
// Every route is deterministic so a journey can assert on it:
//   GET  /            version baked into version.txt (proves which revision serves)
//   GET  /healthz     readiness; 503 when version.txt starts with "broken",
//                     so a journey can deploy a release that never gets ready
//   GET  /env?key=K   presence and SHA-256 of E2E_* variables, never the value
//   GET  /log?marker= writes one runtime log line carrying the marker
//   POST /cron        counts cron deliveries and logs them
//   GET  /cron-hits   reads the counter
//   GET  /sse         three server-sent events, then closes
//   GET  /egress      tries a fixed set of outbound connections (never a
//                     caller-chosen target) and reports which connected
//   GET  /entropy     16 fresh random bytes and the guest clock, to prove
//                     restored instances reseed their RNG and step the clock
//   GET  /ready       readiness for a gregale.yaml health override
//   GET  /secret?key= presence and SHA-256 of an E2E_* secret from the live
//                     reload snapshot (SIGHUP), else the environment
//   GET  /ws          WebSocket: sends "hello <version>", then closes

import http from "node:http";
import net from "node:net";
import { createHash, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";

const version = readFileSync(new URL("./version.txt", import.meta.url), "utf8").trim();
const started = Date.now();
let cronHits = 0;

// In-process secret reload (docs/secrets.md): on SIGHUP reread the atomic
// snapshot, adopt its secrets map and acknowledge its revision.
let liveSecrets = null;
let liveRevision = "";
process.on("SIGHUP", () => {
  try {
    const snapshot = JSON.parse(readFileSync(process.env.FAAS_SECRETS_SNAPSHOT_FILE, "utf8"));
    liveSecrets = snapshot.secrets || {};
    liveRevision = snapshot.revision || "";
    console.log(`e2e-probe-reload revision=${liveRevision}`);
    const endpoint = process.env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT;
    if (endpoint && liveRevision) {
      fetch(endpoint, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          revision: liveRevision,
          status: "applied",
          generation: process.env.FAAS_SECRETS_RELOAD_GENERATION || "",
        }),
      }).then(
        (r) => console.log(`e2e-probe-reload ack status=${r.status}`),
        (err) => console.log(`e2e-probe-reload ack error=${err}`),
      );
    }
  } catch (err) {
    console.log(`e2e-probe-reload failed: ${err}`);
  }
});

function secretValue(key) {
  if (liveSecrets !== null) {
    return { value: liveSecrets[key], source: "reload" };
  }
  return { value: process.env[key], source: "env" };
}

// Outbound targets for /egress. Tenant egress must refuse cloud metadata,
// private ranges, the host side of the guest's link and mail submission
// ports (CLAUDE.md security rules); public HTTPS is the control that proves
// the guest has a network at all.
const egressTargets = {
  public_https: ["1.1.1.1", 443],
  public_https_2: ["8.8.8.8", 443],
  public_https_dns: ["api.github.com", 443],
  metadata: ["169.254.169.254", 80],
  host_link_ssh: ["10.0.0.1", 22],
  private_10: ["10.10.0.2", 22],
  private_192: ["192.168.0.1", 80],
  smtp_25: ["gmail-smtp-in.l.google.com", 25],
  smtp_465: ["smtp.gmail.com", 465],
  smtp_587: ["smtp.gmail.com", 587],
};

function tryConnect(host, port) {
  return new Promise((resolve) => {
    const started = Date.now();
    const socket = net.connect({ host, port, timeout: 3000 });
    const done = (connected, error) => {
      socket.destroy();
      resolve({ connected, error, ms: Date.now() - started });
    };
    socket.once("connect", () => done(true, null));
    socket.once("timeout", () => done(false, "timeout"));
    socket.once("error", (err) => done(false, err.code || String(err)));
  });
}

function json(res, status, body) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify(body));
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://probe.invalid");
  switch (url.pathname) {
    case "/":
      return json(res, 200, { app: "gregale-e2e-probe", version, uptime_ms: Date.now() - started });
    case "/ready":
      return json(res, 200, { ready: true, port: Number(process.env.PORT) || 8080 });
    case "/secret": {
      const key = url.searchParams.get("key") || "";
      if (!/^E2E_[A-Z0-9_]{1,40}$/.test(key)) {
        return json(res, 400, { error: "key must match E2E_[A-Z0-9_]+" });
      }
      const { value, source } = secretValue(key);
      if (value === undefined) {
        return json(res, 200, { key, present: false, source, revision: liveRevision, pid: process.pid });
      }
      const sha256 = createHash("sha256").update(String(value)).digest("hex");
      return json(res, 200, { key, present: true, sha256, source, revision: liveRevision, pid: process.pid });
    }
    case "/healthz":
      if (version.startsWith("broken")) {
        return json(res, 503, { ok: false, version });
      }
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
    case "/egress": {
      const names = Object.keys(egressTargets);
      Promise.all(names.map((n) => tryConnect(...egressTargets[n]))).then((results) => {
        const out = {};
        names.forEach((n, i) => {
          out[n] = { target: egressTargets[n].join(":"), ...results[i] };
        });
        json(res, 200, out);
      });
      return;
    }
    case "/entropy":
      return json(res, 200, { random: randomBytes(16).toString("hex"), now_ms: Date.now(), pid: process.pid });
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

// Minimal WebSocket endpoint (RFC 6455): handshake, one text frame, close.
server.on("upgrade", (req, socket) => {
  const url = new URL(req.url, "http://probe.invalid");
  const key = req.headers["sec-websocket-key"];
  if (url.pathname !== "/ws" || !key) {
    socket.end("HTTP/1.1 400 Bad Request\r\n\r\n");
    return;
  }
  const accept = createHash("sha1").update(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
  socket.write(
    "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
  );
  const payload = Buffer.from(`hello ${version}`);
  socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]));
  socket.end(Buffer.from([0x88, 0x00]));
});

server.listen(Number(process.env.PORT) || 8080, () => {
  console.log(`gregale-e2e-probe ${version} listening`);
});
