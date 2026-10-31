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
//   POST /hook        alert webhook receiver: 200 only when the delivery's
//                     X-Faas-Alert-Signature verifies against E2E_HOOK_SECRET
//                     (412 without the secret, 401 on a bad signature)

import http from "node:http";
import net from "node:net";
import { createHash, createHmac, randomBytes, timingSafeEqual } from "node:crypto";
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

// 169.254.169.254:80 is guest-init's own metadata proxy inside every Gregale
// guest (guest/init/metadata_port.go), so a TCP connect there always succeeds
// and proves nothing. A cloud metadata service is told apart by its API: the
// guest proxy answers 404 to these paths, while GCP (with Metadata-Flavor)
// and AWS (401 when IMDSv2 is enforced) answer otherwise. Only directory
// listings are requested, never credentials.
const metadataPaths = [
  ["/computeMetadata/v1/", { "Metadata-Flavor": "Google" }],
  ["/latest/meta-data/", {}],
];

function probeMetadata() {
  const started = Date.now();
  const ask = ([path, headers]) =>
    new Promise((resolve) => {
      const req = http.get({ host: "169.254.169.254", port: 80, path, headers, timeout: 3000 }, (res) => {
        res.resume();
        resolve({ path, status: res.statusCode });
      });
      req.once("timeout", () => req.destroy(new Error("timeout")));
      req.once("error", (err) => resolve({ path, error: err.message === "timeout" ? "timeout" : err.code || String(err) }));
    });
  return Promise.all(metadataPaths.map(ask)).then((answers) => {
    const cloud = answers.find((a) => a.status !== undefined && a.status !== 404);
    return {
      connected: Boolean(cloud),
      error: cloud ? null : answers.map((a) => `${a.path} ${a.status ?? a.error}`).join("; "),
      ms: Date.now() - started,
    };
  });
}

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

// verifyAlert checks an alert delivery's signature as pkg/webhookout makes
// it: HMAC-SHA256 over "<timestamp>.<delivery id>.<body>", sent as
// "sha256=<hex>".
function verifyAlert(req, body) {
  const { value: secret } = secretValue("E2E_HOOK_SECRET");
  if (!secret) {
    return 412;
  }
  const ts = req.headers["x-faas-alert-timestamp"] || "";
  const id = req.headers["x-faas-alert-id"] || "";
  const got = Buffer.from(String(req.headers["x-faas-alert-signature"] || ""));
  const want = Buffer.from("sha256=" + createHmac("sha256", secret).update(`${ts}.${id}.`).update(body).digest("hex"));
  return got.length === want.length && timingSafeEqual(got, want) ? 200 : 401;
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
    case "/cacheable":
      // The one response a kind=cache edge rule may store: json() sends
      // no-store, which the edge cache never stores.
      res.writeHead(200, { "content-type": "application/json", "cache-control": "public, max-age=60" });
      return res.end(JSON.stringify({ cacheable: true, version }));
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
    case "/slow": {
      // Holds the response so the gateway reports a slow request and the
      // platform captures the instance automatically (ADR-1155).
      const ms = Math.min(Math.max(Number(url.searchParams.get("ms")) || 0, 0), 20000);
      setTimeout(() => json(res, 200, { slept_ms: ms }), ms);
      return;
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
    case "/hook": {
      const chunks = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        const status = verifyAlert(req, Buffer.concat(chunks));
        console.log(`e2e-probe-hook status=${status} id=${req.headers["x-faas-alert-id"] || ""}`);
        json(res, status, { verified: status === 200 });
      });
      return;
    }
    case "/egress": {
      const names = Object.keys(egressTargets);
      Promise.all(names.map((n) => (n === "metadata" ? probeMetadata() : tryConnect(...egressTargets[n])))).then((results) => {
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
    case "/.well-known/jwks.json": {
      // The realtime journey signs its clients' tokens and publishes the
      // public key here, so this app is the endpoint's OIDC issuer.
      const jwks = process.env.E2E_REALTIME_JWKS_B64;
      if (!jwks) {
        return json(res, 404, { error: "no realtime JWKS configured" });
      }
      res.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      return res.end(Buffer.from(jwks, "base64"));
    }
    case "/realtime/connect":
    case "/realtime/message":
    case "/realtime/disconnect":
    case "/realtime/authorize-channel":
      return realtimeCallback(req, res, url.pathname);
    default:
      return json(res, 404, { error: "not found" });
  }
});

// realtimeCallback answers a managed realtime endpoint's callbacks once
// they carry the endpoint's callback token. It grants every channel except
// those named denied-*, so the realtime journey can see a refusal.
function realtimeCallback(req, res, path) {
  const token = process.env.E2E_REALTIME_CALLBACK_TOKEN;
  if (req.method !== "POST" || !token || req.headers.authorization !== `Bearer ${token}`) {
    req.resume();
    return json(res, 401, { error: "bad realtime callback token" });
  }
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    let event = {};
    try {
      event = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    } catch {
      return json(res, 400, { error: "callback body is not JSON" });
    }
    const channel = String(event.channel || "");
    console.log(`e2e-probe-realtime path=${path} permission=${event.permission || ""} channel=${channel}`);
    if (path === "/realtime/authorize-channel" && channel.startsWith("denied-")) {
      return json(res, 403, { granted: false });
    }
    res.writeHead(204);
    res.end();
  });
}

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
