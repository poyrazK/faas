import http from "node:http";
import { randomUUID } from "node:crypto";
import { normalizeOrder } from "./contract.js";
import { createCheckpoints } from "./checkpoints.js";

export function createPostgresProvider({ ledger, holdFirstKey = "in-flight", holdStage = "pending", holdMS = 90000 }) {
  if (!["pending", "committed"].includes(holdStage)) throw new Error("invalid_checkpoint_configuration");
  const checkpoints = createCheckpoints({ holdStage, holdMS });
  const locks = new Map();
  const instanceID = randomUUID();
  let attempts = 0;
  let activeWrites = 0;
  async function prepare(key, order) {
    const previous = locks.get(key) || Promise.resolve();
    const work = previous.catch(() => {}).then(async () => {
      checkpoints.check(key, order);
      if (checkpoints.isExpired(key) && (await ledger.get(key))?.state !== "confirmed") throw Object.assign(new Error("checkpoint_expired"), { status: 503 });
      const state = key === holdFirstKey && holdStage === "pending" ? "pending" : "confirmed";
      const bound = await ledger.bind(key, state, order);
      if (bound.created && key === holdFirstKey) return { wait: checkpoints.wait(key, order) };
      if (bound.record.state !== "confirmed") await ledger.bind(key, "confirmed", order);
      checkpoints.release(key);
      return {};
    });
    locks.set(key, work);
    try { return await work; } finally { if (locks.get(key) === work) locks.delete(key); }
  }
  async function charge(key, order) {
    attempts++;
    activeWrites++;
    try {
      const plan = await prepare(key, order);
      if (plan.wait && !await plan.wait) throw Object.assign(new Error("checkpoint_expired"), { status: 503 });
      return { id: key, status: "confirmed" };
    } finally { activeWrites--; }
  }
  async function inspect(path) {
    if (path === "/" || path === "/stats") return [200, { ...await ledger.stats(), ready: true, held: checkpoints.size, active_writes: activeWrites, queued_writes: locks.size, attempts, instance_id: instanceID }];
    if (path.startsWith("/checkpoints/")) {
      const key = path.slice("/checkpoints/".length);
      return [200, checkpoints.describe(key, await ledger.get(key))];
    }
    if (path.startsWith("/charges/")) {
      const key = path.slice("/charges/".length);
      const record = await ledger.get(key);
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
    try {
      if (request.method === "GET") { send(...await inspect(path)); return; }
      if (request.method !== "POST" || path !== "/charge") { send(404, { error: "not_found" }); return; }
      const key = request.headers["idempotency-key"];
      if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key)) { send(400, { error: "invalid_idempotency_key" }); return; }
      const chunks = [];
      let bytes = 0;
      for await (const chunk of request) {
        bytes += chunk.length;
        if (bytes > 1024) throw Object.assign(new Error("invalid_order"), { status: 400 });
        chunks.push(chunk);
      }
      let order;
      try { order = normalizeOrder(JSON.parse(Buffer.concat(chunks).toString("utf8"))); }
      catch { throw Object.assign(new Error("invalid_order"), { status: 400 }); }
      send(201, await charge(key, order));
    } catch (error) {
      if (error.outcomeUnknown) send(503, { error: "commit_outcome_unknown", payment_outcome: "unknown", retryable: true });
      else send(error.status || 503, { error: error.status ? error.message : "storage_unavailable" });
    }
  });
  server.writeStatus = () => ({ attempts, active_writes: activeWrites, queued_writes: locks.size, instance_id: instanceID });
  server.cancelHeldWrites = () => checkpoints.cancel();
  server.once("close", server.cancelHeldWrites);
  return server;
}
