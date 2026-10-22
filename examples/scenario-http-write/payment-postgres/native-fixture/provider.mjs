import http from "node:http";
import tls from "node:tls";
import { randomUUID, timingSafeEqual, createHash } from "node:crypto";
import pg from "../client.js";
import { poolConfig, namespaceValue } from "../database.js";
import { createReadPair } from "./read-pair.mjs";
import { createCommitProxy } from "./pg-commit-proxy.mjs";

export async function createNativeAuthProvider({ databaseURL, namespace, cert, key, controlToken, disposable, fixtureMode = "authentication",
  upstreamCA = tls.rootCertificates, upstreamServerName = new URL(databaseURL).hostname }) {
  namespaceValue(namespace);
  if (!["authentication", "bandwidth", "read-cut", "ssl-request"].includes(fixtureMode)) throw new Error("invalid_native_fixture_mode");
  const sslRequest = fixtureMode === "ssl-request";
  const bandwidth = fixtureMode === "bandwidth", readCut = fixtureMode === "read-cut", largeRead = bandwidth || readCut;
  const pair = createReadPair();
  const route = sslRequest ? "native-ssl-request" : readCut ? "native-read-cut" : bandwidth ? "native-bandwidth" : "native-auth";
  if (disposable !== "1" || !/^[0-9a-f]{64}$/.test(controlToken || "") || !cert || !key || poolConfig(databaseURL).ssl === false) throw new Error("invalid_native_auth_configuration");
  const proxy = await createCommitProxy(databaseURL, { allowRemoteTarget: true, tls: { cert, key, ca: upstreamCA, servername: upstreamServerName } });
  const applicationName = (sslRequest ? "gregale-native-ssl-request-" : readCut ? "gregale-native-read-cut-" : bandwidth ? "gregale-native-bandwidth-" : "gregale-native-auth-") + namespace;
  const pool = new pg.Pool({ ...poolConfig(proxy.connectionString), ssl: { rejectUnauthorized: true, ca: cert }, max: readCut ? 2 : 1, ...(bandwidth ? { query_timeout: 1500 } : {}), application_name: applicationName });
  pool.on("error", () => {});
  const instanceID = randomUUID();
  let attempts = 0, active = 0, peakWaiting = 0, stopped = false, stopping;
  const status = () => ({ namespace, fixture_mode: fixtureMode, instance_id: instanceID, stopped, attempts, active_queries: active, peak_pool_waiting: peakWaiting,
    pool_total: pool.totalCount, pool_idle: pool.idleCount, pool_waiting: pool.waitingCount, ...pair.evidence(), ...proxy.evidence() });
  const stop = () => stopping ||= (async () => { stopped = true; pair.cancel(); await proxy.close(); await pool.end(); })();
  const authenticated = request => {
    const value = Buffer.from(request.headers.authorization || "");
    const expected = Buffer.from("Bearer " + controlToken);
    return value.length === expected.length && timingSafeEqual(value, expected);
  };
  async function control(request) {
    if (!authenticated(request)) return [401, { error: "unauthorized" }];
    if (request.method !== "POST") return [405, { error: "method_not_allowed" }];
    let body = "";
    for await (const chunk of request) { body += chunk; if (body.length > 256) return [400, { error: "invalid_control" }]; }
    let value;
    try { value = JSON.parse(body); } catch { return [400, { error: "invalid_control" }]; }
    if (value.namespace !== namespace || !(readCut ? ["arm", "arm-concurrent", "clear", "stop"] : bandwidth ? ["fast", "slow", "clear", "stop"] : ["arm", "clear", "stop"]).includes(value.action)) return [400, { error: "invalid_control" }];
    if (value.action === "stop") await stop();
    else if (stopped) return [409, { error: "fixture_stopped" }];
    else if (value.action === "clear") { if (readCut) { pair.cancel(); proxy.clearReadCut(); } else if (bandwidth) proxy.clearBandwidth("response"); else proxy.clearConnectionStall(); }
    else {
      if (active || pool.waitingCount || pool.totalCount) return [409, { error: "fixture_busy" }];
      if (readCut) { proxy.armReadCut({ afterBytes: 2048 }); if (value.action === "arm-concurrent") pair.arm(); }
      else if (bandwidth) proxy.limitBandwidth({ direction: "response", bytesPerSecond: value.action === "fast" ? 262144 : 16384 });
      else proxy.stallConnections({ phase: sslRequest ? "ssl_request" : "authentication", durationMS: 5000 });
    }
    return [200, status()];
  }
  async function query() {
    if (stopped) return [503, { error: "fixture_stopped" }];
    attempts++; active++;
    let client;
    try {
      const acquisition = pool.connect();
      peakWaiting = Math.max(peakWaiting, pool.waitingCount);
      client = await acquisition;
      // Faults can emit a client error as well as rejecting the active query.
      // This acquisition is always destroyed below, so it is never reused.
      client.on("error", () => {});
      if (readCut) await pair.arrive();
      const result = await client.query(largeRead ? "SELECT repeat('x',65536) AS payload" : "SELECT 42 AS answer");
      if (!largeRead) return [200, { ready: true, value: result.rows[0].answer }];
      const payload = result.rows[0].payload;
      if (payload.length !== 65536) throw new Error("incomplete_read");
      return [200, { ready: true, bytes: Buffer.byteLength(payload), sha256: createHash("sha256").update(payload).digest("hex") }];
    } catch { return [503, { error: "dependency_unavailable", retryable: true }]; }
    finally { client?.release(true); active--; }
  }
  const server = http.createServer(async (request, response) => {
    let result;
    try {
      if (request.url === ("/" + route + "-control")) result = await control(request);
      else if (request.method === "GET" && request.url === ("/" + route + "-status")) result = [200, status()];
      else if (request.method === "GET" && request.url === "/query") result = await query();
      else result = [request.url === "/" ? 200 : 404, { ready: !stopped }];
    } catch { result = [503, { error: "fixture_control_failed" }]; }
    if (!response.destroyed) { response.writeHead(result[0], { "content-type": "application/json", "cache-control": "no-store" }); response.end(JSON.stringify(result[1])); }
  });
  server.once("close", () => { stop().catch(() => {}); });
  return { server, stop, status };
}
