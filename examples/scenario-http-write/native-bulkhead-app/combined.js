import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import { createBulkhead } from "./permits.mjs";
import { createCombinedPolicies } from "./combined-policies.mjs";

export function createNativeCombinedCaller({ dependencyURL, namespace, token }) {
  if (!/^[0-9a-f]{32}$/.test(namespace || "") || !/^[0-9a-f]{64}$/.test(token || "")) throw new Error("invalid_combined_fixture");
  const instanceID = randomUUID(), bulkhead = createBulkhead(), policies = createCombinedPolicies(), controllers = new Set(), work = new Set();
  let requests = 0, attempts = 0, protectedAttempts = 0, health = 0, timers = 0, retryTimers = 0, cancellations = 0, stopped = false, stopping;
  const status = () => ({ namespace, instance_id: instanceID, stopped, capacity: 2, queue_capacity: 2, queue_deadline_ms: 1000,
    attempt_deadline_ms: 1500, overall_deadline_ms: 4500, failure_threshold: 4, cooldown_ms: 3000, budget_capacity: 2, budget_window_ms: 30000,
    requests, dependency_attempts: attempts, permit_protected_attempts: protectedAttempts, health_requests: health, request_cancellations: cancellations,
    active_requests: controllers.size, deadline_timers: timers, retry_timers: retryTimers, ...bulkhead.evidence(), ...policies.evidence() });
  const stop = () => stopping ||= (async () => { stopped = true; for (const controller of controllers) controller.abort(); await Promise.all([...work]); })();
  async function attempt(signal, count) {
    if (!policies.precheck(count > 0)) return { error: "circuit_open", sent: false };
    const lease = await bulkhead.acquire(signal);
    if (lease !== "granted") return { error: lease === "expired" ? "queue_timeout" : lease === "rejected" ? "bulkhead_rejected" : "request_canceled", sent: false };
    try {
      if (signal.aborted) return { error: "request_canceled", sent: false };
      const admission = policies.admit(count > 0);
      if (!admission) return { error: "circuit_open", sent: false };
      if (count && !policies.retry()) return { error: "retry_budget_exhausted", sent: false };
      attempts++; protectedAttempts++;
      const controller = new AbortController(); timers++;
      const deadline = setTimeout(() => controller.abort(), 1500);
      let succeeded = false;
      try {
        const result = await fetch(dependencyURL + "/query", { redirect: "error", signal: AbortSignal.any([signal, controller.signal]) });
        const body = await result.json(); succeeded = result.status === 200 && body.ready === true && body.value === 42;
      } catch { /* Exactly one protected dependency read; redacted errors. */ }
      finally { clearTimeout(deadline); timers--; policies.settle(admission, succeeded, signal.aborted); }
      return { error: "dependency_unavailable", sent: true, succeeded, probe: admission.probe };
    } finally { bulkhead.release(); }
  }
  async function query(response) {
    if (stopped) return [503, { error: "fixture_stopped" }];
    requests++;
    const controller = new AbortController(); controllers.add(controller); timers++;
    const closed = () => controller.abort(); response.once("close", closed);
    const overall = setTimeout(() => controller.abort(), 4500);
    let count = 0, error = "request_canceled";
    try {
      while (!controller.signal.aborted && count < 2) {
        const result = await attempt(controller.signal, count); if (result.sent) count++; error = result.error;
        if (result.succeeded) return [200, { ready: true, value: 42, attempts: count }];
        if (!result.sent || result.probe || count === 2 || controller.signal.aborted) break;
        retryTimers++;
        try { await delay(100, undefined, { signal: controller.signal }); } finally { retryTimers--; }
      }
    } catch { error = "request_canceled"; }
    finally {
      if (controller.signal.aborted) cancellations++;
      clearTimeout(overall); timers--; controllers.delete(controller); response.off("close", closed);
    }
    return [503, { error, retryable: true, attempts: count }];
  }
  async function handle(request, response) {
    let result;
    if (request.method === "GET" && request.url === "/combined-status") result = [200, status()];
    else if (request.method === "GET" && request.url === "/health") { health++; result = [200, { ready: !stopped, active_dependencies: bulkhead.evidence().active_dependencies }]; }
    else if (request.url === "/combined-control") {
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
