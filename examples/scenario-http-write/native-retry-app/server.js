import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";

export function createNativeRetryCaller({ dependencyURL, namespace, token, qualificationMode = "bounded" }) {
  if (!/^[0-9a-f]{32}$/.test(namespace || "") || !/^[0-9a-f]{64}$/.test(token || "")) throw new Error("invalid_retry_fixture");
  if (!["bounded", "outlier", "missing-id", "missing-receipt"].includes(qualificationMode)) throw new Error("invalid_qualification_mode");
  let strictRequests = 0;
  const instanceID = randomUUID(), controllers = new Set(), work = new Set();
  let stopped = false, stopping, originals = 0, attempts = 0, admitted = 0, denied = 0, timers = 0, deadlineTimers = 0;
  let windowStart, windowID = 0, remaining = 2;
  const status = () => ({ budget_capacity: 2, budget_window_ms: 30000, overall_deadline_ms: 4500, attempt_deadline_ms: 1500, max_attempts: 2, namespace, instance_id: instanceID, stopped, originals, dependency_attempts: attempts,
    retries_admitted: admitted, retries_denied: denied, remaining, window_id: windowID, active_requests: controllers.size, retry_timers: timers, deadline_timers: deadlineTimers });
  function allow() {
    const now = performance.now();
    if (windowStart === undefined || now - windowStart >= 30000) { windowStart = now; windowID++; remaining = 2; }
    if (!remaining) { denied++; return false; }
    remaining--; admitted++; return true;
  }
  const stop = () => stopping ||= (async () => { stopped = true; for (const controller of controllers) controller.abort(); await Promise.all([...work]); })();
  async function query(response, correlationID) {
    if (stopped) return [503, { error: "fixture_stopped" }];
    originals++;
    const controller = new AbortController(); controllers.add(controller);
    const closed = () => controller.abort(); response.once("close", closed);
    const signal = controller.signal; deadlineTimers++;
    const overall = setTimeout(() => controller.abort(), 4500);
    const strict = Boolean(correlationID);
    const maximum = strict && qualificationMode === "outlier" && ++strictRequests === 1 ? 11 : strict && qualificationMode === "outlier" ? 1 : 2;
    let count = 0;
    try {
      while (!signal.aborted && count < maximum) {
        attempts++; count++;
        const attemptController = new AbortController(); deadlineTimers++;
        const deadline = setTimeout(() => attemptController.abort(), 1500);
        try {
          const result = await fetch(dependencyURL + "/query", { headers: correlationID && qualificationMode !== "missing-id" ? { "x-gregale-test-request-id": correlationID } : {}, redirect: "error", signal: AbortSignal.any([signal, attemptController.signal]) });
          const body = await result.json();
          if (result.status === 200 && body.ready === true && body.value === 42) return [200, { ready: true, value: 42, attempts: count }];
        } catch { /* Redacted receipt; retries are only for this fixed read. */ }
        finally { clearTimeout(deadline); deadlineTimers--; }
        if (count === maximum || signal.aborted || (qualificationMode === "bounded" && !allow())) break;
        timers++;
        try { await delay(100, undefined, { signal }); } finally { timers--; }
      }
    } catch { /* Cancellation joins the delay and dependency request. */ }
    finally { if (correlationID && qualificationMode !== "missing-receipt" && !response.destroyed) response.setHeader("x-gregale-test-dependency-attempts", String(count)); clearTimeout(overall); deadlineTimers--; controllers.delete(controller); response.off("close", closed); }
    return [503, { error: "dependency_unavailable", retryable: true, attempts: count }];
  }
  async function handle(request, response) {
    let result;
    if (request.method === "GET" && request.url === "/retry-budget-status") result = [200, status()];
    else if (request.url === "/retry-budget-control") {
      const value = Buffer.from(request.headers.authorization || ""), expected = Buffer.from("Bearer " + token);
      if (value.length !== expected.length || !timingSafeEqual(value, expected)) result = [401, { error: "unauthorized" }];
      else if (request.method !== "POST" || request.headers["x-test-namespace"] !== namespace) result = [400, { error: "invalid_control" }];
      else { await stop(); result = [200, status()]; }
    } else if (request.method === "GET" && request.url === "/query") result = await query(response, typeof request.headers["x-gregale-test-request-id"] === "string" && request.headers["x-gregale-test-request-id"].startsWith(namespace + ".") ? request.headers["x-gregale-test-request-id"] : undefined);
    else result = [request.url === "/" ? 200 : 404, { ready: !stopped }];
    if (!response.destroyed) { response.writeHead(result[0], { "content-type": "application/json", "cache-control": "no-store" }); response.end(JSON.stringify(result[1])); }
  }
  const server = http.createServer((request, response) => {
    // Controls must not wait on their own handler during shutdown.
    const pending = handle(request, response);
    if (request.url === "/query") { work.add(pending); pending.finally(() => work.delete(pending)).catch(() => {}); }
    pending.catch(() => { if (!response.destroyed) { response.writeHead(503); response.end(); } });
  });
  server.once("close", () => { stop().catch(() => {}); });
  return { server, stop, status };
}
