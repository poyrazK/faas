import http from "node:http";
import { performance } from "node:perf_hooks";
import { setTimeout as delay } from "node:timers/promises";

export async function fetchWithRetry(url, { signal, maxAttempts = 3 } = {}) {
  if (!Number.isInteger(maxAttempts) || maxAttempts < 1 || maxAttempts > 3) throw new Error("invalid attempt limit");
  const proof = { attempts: 0, statuses: [], retry_after_seconds: [], waits_ms: [], body_errors: [] };
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    proof.attempts++;
    const response = await fetch(url, { signal, redirect: "error" });
    proof.statuses.push(response.status);
    if (response.status === 200) {
      try {
        const body = await response.json();
        if (!body || body.available !== true) {
          proof.body_errors.push("invalid_schema");
        } else {
          return { status: 200, body: { ...proof, dependency_available: true } };
        }
      } catch (error) {
        if (signal?.aborted) throw error;
        proof.body_errors.push(error instanceof SyntaxError ? "malformed_json" : "body_read_failed");
      }
      // This example retries read-only GET requests. Writes require their own
      // idempotency contract before retrying an unknown response outcome.
      if (attempt + 1 < maxAttempts) {
        const started = performance.now();
        await delay(100, undefined, { signal });
        proof.waits_ms.push(performance.now() - started);
      }
      continue;
    }
    await response.body?.cancel();
    if (response.status !== 429 && response.status !== 503) break;
    const header = response.headers.get("retry-after");
    const seconds = header && /^[0-9]{1,2}$/.test(header) ? Number(header) : NaN;
    // Fail closed for unsupported/missing headers rather than retrying in a loop.
    if (!Number.isInteger(seconds) || seconds < 1 || seconds > 30) {
      return { status: 503, body: { ...proof, error: "invalid_retry_after" } };
    }
    proof.retry_after_seconds.push(seconds);
    if (attempt + 1 === maxAttempts) break;
    const start = performance.now();
    const until = start + seconds * 1000;
    // Timers can wake fractionally early. Recheck monotonic time before retrying.
    while (performance.now() < until) await delay(Math.ceil(until - performance.now()), undefined, { signal });
    proof.waits_ms.push(performance.now() - start);
  }
  return { status: 503, body: { ...proof, error: proof.body_errors.length ? "invalid_dependency_response" : "dependency_unavailable" } };
}

export function createRetryServer({ dependencyURL, deadlineMS = 5000 }) {
  return http.createServer(async (request, response) => {
    if (request.url === "/") { response.end("ready"); return; }
    if (request.url !== "/fetch") { response.writeHead(404); response.end(); return; }
    const controller = new AbortController();
    response.once("close", () => controller.abort());
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(deadlineMS)]);
    let result;
    try { result = await fetchWithRetry(dependencyURL, { signal }); }
    catch { result = { status: 503, body: { error: "dependency_deadline" } }; }
    if (response.destroyed) return;
    response.writeHead(result.status, { "content-type": "application/json", "cache-control": "no-store" });
    response.end(JSON.stringify(result.body));
  });
}
