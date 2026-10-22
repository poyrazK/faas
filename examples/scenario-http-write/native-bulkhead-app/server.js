import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";
import { createBulkhead } from "./permits.mjs";

export function createNativeBulkheadCaller({ dependencyURL, namespace, token }) {
  if (!/^[0-9a-f]{32}$/.test(namespace || "") || !/^[0-9a-f]{64}$/.test(token || "")) throw new Error("invalid_bulkhead_fixture");
  const instanceID = randomUUID(), bulkhead = createBulkhead(), controllers = new Set(), work = new Set();
  let requests = 0, attempts = 0, health = 0, timers = 0, stopped = false, stopping;
  const status = () => ({ namespace, instance_id: instanceID, stopped, capacity: 2, queue_capacity: 2, queue_deadline_ms: 1000,
    attempt_deadline_ms: 2500, overall_deadline_ms: 3500, requests, dependency_attempts: attempts, health_requests: health,
    active_requests: controllers.size, deadline_timers: timers, ...bulkhead.evidence() });
  const stop = () => stopping ||= (async () => { stopped = true; for (const controller of controllers) controller.abort(); await Promise.all([...work]); })();
  async function query(response) {
    if (stopped) return [503, { error: "fixture_stopped" }];
    requests++;
    const controller = new AbortController(); controllers.add(controller); timers++;
    const closed = () => controller.abort(); response.once("close", closed);
    const overall = setTimeout(() => controller.abort(), 3500);
    let permit = false;
    try {
      const admission = await bulkhead.acquire(controller.signal); permit = admission === "granted";
      if (!permit) return [503, { error: admission === "expired" ? "queue_timeout" : admission === "rejected" ? "bulkhead_rejected" : "request_canceled", retryable: true, attempts: 0 }];
      if (controller.signal.aborted) return [503, { error: "request_canceled", retryable: true, attempts: 0 }];
      attempts++;
      const deadlineController = new AbortController(); timers++;
      const deadline = setTimeout(() => deadlineController.abort(), 2500);
      try {
        const result = await fetch(dependencyURL + "/query", { redirect: "error", signal: AbortSignal.any([controller.signal, deadlineController.signal]) });
        const body = await result.json();
        if (result.status === 200 && body.ready === true && body.value === 42) return [200, { ready: true, value: 42, attempts: 1 }];
      } catch { /* Fixed receipt; no retry or dependency error disclosure. */ }
      finally { clearTimeout(deadline); timers--; }
      return [503, { error: "dependency_unavailable", retryable: true, attempts: 1 }];
    } finally {
      if (permit) bulkhead.release();
      clearTimeout(overall); timers--; controllers.delete(controller); response.off("close", closed);
    }
  }
  async function handle(request, response) {
    let result;
    if (request.method === "GET" && request.url === "/bulkhead-status") result = [200, status()];
    else if (request.method === "GET" && request.url === "/health") { health++; result = [200, { ready: !stopped, active_dependencies: bulkhead.evidence().active_dependencies }]; }
    else if (request.url === "/bulkhead-control") {
      const value = Buffer.from(request.headers.authorization || ""), expected = Buffer.from("Bearer " + token);
      if (value.length !== expected.length || !timingSafeEqual(value, expected)) result = [401, { error: "unauthorized" }];
      else if (request.method !== "POST" || request.headers["x-test-namespace"] !== namespace) result = [400, { error: "invalid_control" }];
      else { await stop(); result = [200, status()]; }
    } else if (request.method === "GET" && request.url === "/query") result = await query(response);
    else result = [request.url === "/" ? 200 : 404, { ready: !stopped }];
    if (!response.destroyed) { response.writeHead(result[0], { "content-type": "application/json", "cache-control": "no-store" }); response.end(JSON.stringify(result[1])); }
  }
  const server = http.createServer((request, response) => {
    const pending = handle(request, response);
    if (request.url === "/query") { work.add(pending); pending.finally(() => work.delete(pending)).catch(() => {}); }
    pending.catch(() => { if (!response.destroyed) { response.writeHead(503); response.end(); } });
  });
  server.once("close", () => { stop().catch(() => {}); });
  return { server, stop, status };
}
