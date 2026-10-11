import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";

const stage = process.argv[2];
assert.ok(["baseline", "slow-dependency", "recovery"].includes(stage));
if (stage !== "baseline") await delay(1200); // allow the pooled gateway policy refresh to settle

const response = await fetch(`${process.env.GREGALE_TEST_URL}/pool/probe`, {
  signal: AbortSignal.timeout(10000),
});
assert.equal(response.status, 200);
const result = await response.json();
console.log(`${stage} observation:`, result);
assert.equal(result.outcome, "ok", result.error);
assert.equal(result.bytes, 16);
assert.equal(result.connection_generation, 1, "the application replaced its dependency connection");

if (stage === "baseline") {
  assert.ok(result.elapsed_ms < 180, `baseline was unexpectedly slow: ${result.elapsed_ms} ms`);
} else if (stage === "slow-dependency") {
  assert.ok(result.elapsed_ms >= 200, `latency fault was not observed: ${result.elapsed_ms} ms`);
} else {
  assert.ok(result.elapsed_ms < 180, `dependency did not recover after clearing the fault: ${result.elapsed_ms} ms`);
}
