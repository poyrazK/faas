import http from "node:http";
import { setTimeout as delay } from "node:timers/promises";
import { normalizeOrder } from "./contract.js";

export async function readOrder(request) {
  let bytes = 0;
  const chunks = [];
  for await (const chunk of request) {
    bytes += chunk.length;
    if (bytes > 1024) throw Object.assign(new Error("invalid_order"), { status: 400 });
    chunks.push(chunk);
  }
  try { return normalizeOrder(JSON.parse(Buffer.concat(chunks).toString("utf8"))); }
  catch { throw Object.assign(new Error("invalid_order"), { status: 400 }); }
}
export async function charge(dependencyURL, key, order, signal) {
  const proof = { attempts: 0, statuses: [], body_errors: [] };
  for (let attempt = 0; attempt < 3; attempt++) {
    proof.attempts++;
    const response = await fetch(`${dependencyURL}/charge`, {
      method: "POST", headers: { "idempotency-key": key, "content-type": "application/json" },
      body: JSON.stringify(order), signal, redirect: "error",
    });
    proof.statuses.push(response.status);
    if (response.status === 409) {
      await response.body?.cancel();
      return { status: 409, body: { ...proof, error: "idempotency_conflict" } };
    }
    if (response.status === 200 || response.status === 201) {
      try {
        const result = await response.json();
        if (result.id === key && result.status === "confirmed") {
          return { status: 200, body: { ...proof, id: key, status: "confirmed" } };
        }
        proof.body_errors.push("invalid_acknowledgement");
      } catch (error) {
        if (signal.aborted) throw error;
        proof.body_errors.push(error instanceof SyntaxError ? "invalid_acknowledgement" : "body_read_failed");
      }
    } else {
      await response.body?.cancel();
      break;
    }
    // The provider binds the same key to the same immutable payload before
    // committing, so response-read failures can safely retry this exact write.
    if (attempt < 2) await delay(100, undefined, { signal });
  }
  return { status: 503, body: { ...proof, error: "payment_unavailable", payment_outcome: "unknown", retryable: true } };
}
export function createWriteCaller({ dependencyURL, deadlineMS = 5000 }) {
  let active = 0;
  return http.createServer(async (request, response) => {
    const send = result => {
      if (response.destroyed) return;
      response.writeHead(result.status, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(result.body));
    };
    if (request.method === "GET" && request.url === "/in-flight") { send({ status: 200, body: { active } }); return; }
    if (request.method === "GET" && request.url === "/durability") {
      try {
        const upstream = await fetch(`${dependencyURL}/stats`, { signal: AbortSignal.timeout(2000), redirect: "error" });
        send({ status: upstream.status, body: await upstream.json() });
      } catch { send({ status: 503, body: { error: "payment_unavailable" } }); }
      return;
    }
    if (request.url === "/") { send({ status: 200, body: { ready: true } }); return; }
    if (request.url !== "/checkout" || request.method !== "POST") { send({ status: 404, body: { error: "not_found" } }); return; }
    const key = request.headers["idempotency-key"];
    if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key)) { send({ status: 400, body: { error: "invalid_idempotency_key" } }); return; }
    const controller = new AbortController();
    response.once("close", () => controller.abort());
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(deadlineMS)]);
    let counted = false;
    try {
      const order = await readOrder(request);
      active++;
      counted = true;
      send(await charge(dependencyURL, key, order, signal));
    } catch (error) {
      send({ status: error.status || 503, body: error.status ? { error: error.message } : { error: "payment_unavailable", payment_outcome: "unknown", retryable: true } });
    } finally {
      // Invalid bodies never increment active.
      if (counted) active--;
    }
  });
}
