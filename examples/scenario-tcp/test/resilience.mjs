import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";

const kind = process.argv[2];
assert.ok(["latency", "bandwidth", "timeout", "reset"].includes(kind));
const probe = async bytes => {
  const response = await fetch(`${process.env.GREGALE_TEST_URL}/probe?bytes=${bytes}`, {
    signal: AbortSignal.timeout(10000),
  });
  assert.equal(response.status, 200);
  return response.json();
};
const result = await probe(kind === "bandwidth" ? 16384 : 16);
console.log("fault observation:", result);
if (kind === "timeout") {
  assert.equal(result.outcome, "timeout");
  assert.equal(result.error, "application_deadline");
} else if (kind === "reset") {
  assert.equal(result.outcome, "error");
  assert.ok(["ECONNRESET", "EPIPE"].includes(result.error), result.error);
} else {
  assert.equal(result.outcome, "ok");
  assert.ok(result.elapsed_ms >= (kind === "latency" ? 200 : 800), `fault too fast: ${result.elapsed_ms} ms`);
  assert.equal(result.bytes, kind === "bandwidth" ? 16384 : 16);
}
// The eight-second lease expires without a destructive change to the app.
// Reset recovery opens a fresh connection; the other rules also release pools.
const deadline = Date.now() + 20000;
let recovered = false;
while (Date.now() < deadline) {
  await delay(400);
  const recovery = await probe(16);
  if (recovery.outcome === "ok" && recovery.bytes === 16 && recovery.elapsed_ms < 180) {
    console.log("recovery observation:", recovery);
    recovered = true;
    break;
  }
}
assert.ok(recovered, "dependency did not recover after fault expiry");
