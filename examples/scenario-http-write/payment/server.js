import http from "node:http";
import { openLedger } from "./ledger.js";
import { normalizeOrder } from "./contract.js";
import { createCheckpoints } from "./checkpoints.js";

export function createWriteProvider({ ledgerPath, holdFirstKey = "", holdStage = "pending", holdMS = 90000 }) {
  const ledger = openLedger(ledgerPath);
  const checkpoints = createCheckpoints({ holdStage, holdMS });
  let attempts = 0;

  async function charge(key, order) {
    attempts++;
    checkpoints.check(key, order);
    const record = ledger.get(key);
    const first = !record && !checkpoints.has(key);
    if (first && key === holdFirstKey) {
      // Before acceptance binds only in memory. Later gates bind durably before
      // waiting. A committed gate delays only acknowledgement, never the commit.
      if (holdStage !== "before-acceptance") ledger.set(key, holdStage === "committed" ? "confirmed" : "pending", order);
      if (!await checkpoints.wait(key, order)) throw Object.assign(new Error("checkpoint_expired"), { status: 503 });
    } else {
      // An expired uncommitted operation fails closed. A confirmed operation
      // remains safe to replay even if its acknowledgement gate timed out.
      if (checkpoints.isExpired(key) && record?.state !== "confirmed") throw Object.assign(new Error("checkpoint_expired"), { status: 503 });
      ledger.set(key, "confirmed", order);
      checkpoints.release(key);
    }
    return { id: key, status: "confirmed" };
  }
  function inspect(path) {
    if (path === "/") return [200, { ready: true }];
    if (path === "/stats") return [200, { charges: ledger.count("confirmed"), pending: ledger.count("pending"), held: checkpoints.size, ledger_id: ledger.id, attempts }];
    if (path.startsWith("/checkpoints/")) {
      const key = path.slice("/checkpoints/".length);
      return [200, checkpoints.describe(key, ledger.get(key))];
    }
    if (path.startsWith("/charges/")) {
      const key = path.slice("/charges/".length);
      const record = ledger.get(key);
      return [record ? 200 : 404, record ? { id: key, status: record.state, order: record.order } : { error: "not_found" }];
    }
    return [404, { error: "not_found" }];
  }
  const server = http.createServer(async (request, response) => {
    const send = (status, body) => {
      if (response.destroyed) return;
      response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(body));
    };
    const path = new URL(request.url, "http://payment").pathname;
    if (request.method === "GET") { send(...inspect(path)); return; }
    if (request.method !== "POST" || path !== "/charge") { send(404, { error: "not_found" }); return; }
    const key = request.headers["idempotency-key"];
    if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key)) { send(400, { error: "invalid_idempotency_key" }); return; }
    try {
      let bytes = 0;
      const chunks = [];
      for await (const chunk of request) {
        bytes += chunk.length;
        if (bytes > 1024) throw Object.assign(new Error("invalid_order"), { status: 400 });
        chunks.push(chunk);
      }
      let order;
      try { order = normalizeOrder(JSON.parse(Buffer.concat(chunks).toString("utf8"))); }
      catch { throw Object.assign(new Error("invalid_order"), { status: 400 }); }
      send(201, await charge(key, order));
    } catch (error) { send(error.status || 500, { error: error.status ? error.message : "storage_unavailable" }); }
  });
  server.cancelHeldWrites = () => checkpoints.cancel();
  server.once("close", server.cancelHeldWrites);
  return server;
}
