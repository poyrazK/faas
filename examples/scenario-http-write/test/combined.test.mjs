import assert from "node:assert/strict";
import http from "node:http";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { createNativeCombinedCaller } from "../native-bulkhead-app/combined.js";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
async function waitFor(predicate) {
  const end = Date.now() + 800;
  while (!predicate() && Date.now() < end) await new Promise(resolve => setTimeout(resolve, 1));
  assert.ok(predicate());
}
const fail = response => { response.writeHead(503, { "content-type": "application/json" }); response.end('{}'); };

test("queued combined request rechecks an opened circuit and releases its newly granted permit", { timeout: 5000 }, async t => {
  const calls = [];
  const dependency = http.createServer((_request, response) => { calls.push(response); if (calls.length <= 2) fail(response); });
  const dependencyURL = await listen(dependency);
  const fixture = createNativeCombinedCaller({ dependencyURL, namespace: randomUUID().replaceAll("-", ""), token: "d".repeat(64) });
  const appURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); await close(dependency); });
  const originals = [fetch(appURL + "/query"), fetch(appURL + "/query")];
  await waitFor(() => calls.length === 4 && fixture.status().active_dependencies === 2);
  fail(calls[2]);
  await waitFor(() => fixture.status().consecutive_failures === 3 && fixture.status().active_dependencies === 1);
  const remaining = fetch(appURL + "/query");
  await waitFor(() => calls.length === 5 && fixture.status().active_dependencies === 2);
  const queued = fetch(appURL + "/query");
  await waitFor(() => fixture.status().queued_requests === 1);
  fail(calls[3]);
  const rejected = await queued; assert.equal(rejected.status, 503);
  assert.deepEqual(await rejected.json(), { error: "circuit_open", retryable: true, attempts: 0 });
  assert.equal(calls.length, 5); assert.equal(fixture.status().blocked_after_acquire, 1);
  assert.equal(fixture.status().state, "open"); assert.equal(fixture.status().retries_admitted, 2);
  for (const response of await Promise.all(originals)) { assert.equal(response.status, 503); assert.equal((await response.json()).attempts, 2); }
  await fixture.stop(); await remaining; await fixture.stop();
  assert.equal(fixture.status().dependency_attempts, 5); assert.equal(fixture.status().permit_protected_attempts, 5);
  assert.equal(fixture.status().permits_granted, fixture.status().permits_released);
  for (const field of ["active_requests", "active_dependencies", "active_probes", "queued_requests", "queue_timers", "retry_timers", "deadline_timers"]) assert.equal(fixture.status()[field], 0);
});
