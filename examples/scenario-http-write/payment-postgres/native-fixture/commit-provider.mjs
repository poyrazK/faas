import tls from "node:tls";
import { timingSafeEqual } from "node:crypto";
import { namespaceValue, poolConfig } from "../database.js";
import { openPostgresLedger } from "../ledger.js";
import { createPostgresProvider } from "../server.js";
import { createCommitProxy } from "./pg-commit-proxy.mjs";

export async function createNativeCommitProvider({ databaseURL, namespace, cert, key, controlToken, disposable, faultMode = "drop",
  upstreamCA = tls.rootCertificates, upstreamServerName = new URL(databaseURL).hostname }) {
  namespaceValue(namespace);
  if (!["drop", "request-deadline", "response-deadline"].includes(faultMode)) throw new Error("invalid_native_commit_fault");
  if (disposable !== "1" || !/^[0-9a-f]{64}$/.test(controlToken || "") || !cert || !key || poolConfig(databaseURL).ssl === false) throw new Error("invalid_native_commit_configuration");
  const proxy = await createCommitProxy(databaseURL, { allowRemoteTarget: true, tls: { cert, key, ca: upstreamCA, servername: upstreamServerName } });
  let ledger;
  try { ledger = await openPostgresLedger({ connectionString: proxy.connectionString, namespace, tlsCA: cert }); }
  catch (error) { await proxy.close(); throw error; }
  const server = createPostgresProvider({ ledger, holdFirstKey: "" });
  const handler = server.listeners("request")[0]; server.removeAllListeners("request");
  let stopped = false, stopping;
  const status = () => ({ namespace, fault_mode: faultMode, ledger_id: ledger.id, stopped, ...server.writeStatus(), ...ledger.poolStatus(), ...proxy.evidence() });
  const stop = () => stopping ||= (async () => { stopped = true; server.cancelHeldWrites(); await proxy.close(); await ledger.close(); })();
  async function control(request) {
    const value = Buffer.from(request.headers.authorization || ""), expected = Buffer.from("Bearer " + controlToken);
    if (value.length !== expected.length || !timingSafeEqual(value, expected)) return [401, { error: "unauthorized" }];
    if (request.method !== "POST") return [405, { error: "method_not_allowed" }];
    let body = "";
    for await (const chunk of request) { body += chunk; if (body.length > 256) return [400, { error: "invalid_control" }]; }
    let command;
    try { command = JSON.parse(body); } catch { return [400, { error: "invalid_control" }]; }
    if (command.namespace !== namespace || !["arm", "stop"].includes(command.action)) return [400, { error: "invalid_control" }];
    if (command.action === "stop") await stop();
    else if (stopped || status().active_writes || status().queued_writes || status().armed) return [409, { error: "fixture_busy" }];
    else proxy.arm(faultMode === "drop" ? undefined : { mode: "stall", direction: faultMode === "request-deadline" ? "request" : "response", durationMS: 5000 });
    return [200, status()];
  }
  server.on("request", async (request, response) => {
    if (request.url !== "/native-commit-control" && request.url !== "/native-commit-status" && !stopped) { handler(request, response); return; }
    let result;
    try {
      result = request.url === "/native-commit-control" ? await control(request) :
        request.method === "GET" && request.url === "/native-commit-status" ? [200, status()] : [503, { error: "fixture_stopped" }];
    } catch { result = [503, { error: "fixture_control_failed" }]; }
    if (!response.destroyed) { response.writeHead(result[0], { "content-type": "application/json", "cache-control": "no-store" }); response.end(JSON.stringify(result[1])); }
  });
  server.once("close", () => { stop().catch(() => {}); });
  return { server, stop, status };
}
