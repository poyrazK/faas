import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

export async function verifyRetry(baseURL, phase) {
  const response = await fetch(`${baseURL}/fetch`, { signal: AbortSignal.timeout(6000) });
  const result = await response.json();
  if (phase === "outage") {
    assert.equal(response.status, 503);
    assert.equal(result.error, "dependency_unavailable");
    assert.equal(result.attempts, 3);
    assert.deepEqual(result.statuses, [429, 429, 429]);
    assert.deepEqual(result.retry_after_seconds, [1, 1, 1]);
    assert.equal(result.waits_ms.length, 2);
    for (const wait of result.waits_ms) assert.ok(wait >= 1000, `retry started too early: ${wait}ms`);
  } else if (phase === "malformed" || phase === "truncated") {
    assert.equal(response.status, 503);
    assert.equal(result.error, "invalid_dependency_response");
    assert.equal(result.attempts, 3);
    assert.deepEqual(result.statuses, [200, 200, 200]);
    assert.deepEqual(result.body_errors, Array(3).fill(phase === "malformed" ? "malformed_json" : "body_read_failed"));
    assert.deepEqual(result.retry_after_seconds, []);
    assert.equal(result.waits_ms.length, 2);
  } else if (phase === "baseline" || phase === "recovery") {
    assert.equal(response.status, 200);
    assert.equal(result.attempts, 1);
    assert.deepEqual(result.statuses, [200]);
    assert.equal(result.dependency_available, true);
    assert.deepEqual(result.waits_ms, []);
  } else throw new Error("unknown retry acceptance phase");
  console.log(JSON.stringify({ phase, ...result }));
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyRetry(process.env.GREGALE_TEST_URL, process.argv[2]);
}
