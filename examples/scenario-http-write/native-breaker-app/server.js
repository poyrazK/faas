import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";

export function createNativeBreakerCaller({ dependencyURL, namespace, token }) {
  if (!/^[0-9a-f]{32}$/.test(namespace || "") || !/^[0-9a-f]{64}$/.test(token || "")) throw new Error("invalid_breaker_fixture");
  const instanceID = randomUUID(), controllers = new Set(), work = new Set();
  let state = "closed", generation = 0, openedAt = 0, failures = 0, requests = 0, attempts = 0, blocked = 0;
  let opens = 0, halves = 0, closes = 0, probes = 0, activeProbes = 0, peakProbes = 0, probeFailures = 0, probeCancellations = 0, probeSuccesses = 0;
  let timers = 0, stopped = false, stopping;
  const status = () => ({ namespace, instance_id: instanceID, stopped, state, generation, failure_threshold: 2, cooldown_ms: 3000,
    deadline_ms: 1500, cooldown_remaining_ms: state === "open" ? Math.max(0, 3000 - (performance.now() - openedAt)) : 0,
    consecutive_failures: failures, requests, dependency_attempts: attempts, blocked, open_transitions: opens, half_open_transitions: halves,
    closed_transitions: closes, probes_started: probes, active_probes: activeProbes, peak_probes: peakProbes,
    probe_failures: probeFailures, probe_cancellations: probeCancellations, probe_successes: probeSuccesses, active_requests: controllers.size, deadline_timers: timers });
  const open = () => { state = "open"; generation++; openedAt = performance.now(); opens++; failures = 2; };
  function admit() {
    if (state === "open" && performance.now() - openedAt >= 3000) { state = "half-open"; generation++; halves++; }
    if (state === "open" || state === "half-open" && activeProbes) { blocked++; return null; }
    const probe = state === "half-open";
    if (probe) { probes++; activeProbes++; peakProbes = Math.max(peakProbes, activeProbes); }
    return { generation, probe };
  }
  function settle(admission, succeeded, canceled) {
    if (admission.probe) {
      activeProbes--;
      if (canceled) probeCancellations++; else if (succeeded) probeSuccesses++; else probeFailures++;
    }
    if (admission.generation !== generation) return; // Late closed-state results cannot reset a newer open circuit.
    if (canceled) { if (admission.probe) open(); return; }
    if (succeeded) { failures = 0; if (admission.probe) { state = "closed"; generation++; closes++; } }
    else if (admission.probe || ++failures >= 2) open();
  }
  const stop = () => stopping ||= (async () => { stopped = true; for (const controller of controllers) controller.abort(); await Promise.all([...work]); })();
  async function query(response) {
    if (stopped) return [503, { error: "fixture_stopped" }];
    requests++;
    const admission = admit();
    if (!admission) return [503, { error: "circuit_open", retryable: true, attempts: 0 }];
    attempts++;
    const controller = new AbortController(), deadlineController = new AbortController(); controllers.add(controller); timers++;
    const close = () => controller.abort(); response.once("close", close);
    const deadline = setTimeout(() => deadlineController.abort(), 1500);
    let succeeded = false;
    try {
      const result = await fetch(dependencyURL + "/query", { redirect: "error", signal: AbortSignal.any([controller.signal, deadlineController.signal]) });
      const body = await result.json(); succeeded = result.status === 200 && body.ready === true && body.value === 42;
    } catch { /* Redacted failure receipt; exactly one dependency attempt. */ }
    finally {
      clearTimeout(deadline); timers--; controllers.delete(controller); response.off("close", close);
      settle(admission, succeeded, controller.signal.aborted);
    }
    return succeeded ? [200, { ready: true, value: 42, attempts: 1 }] : [503, { error: "dependency_unavailable", retryable: true, attempts: 1 }];
  }
  async function handle(request, response) {
    let result;
    if (request.method === "GET" && request.url === "/breaker-status") result = [200, status()];
    else if (request.url === "/breaker-control") {
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
