import assert from "node:assert/strict";
import http from "node:http";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { createNativeBulkheadCaller } from "../native-bulkhead-app/server.js";

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

test("bulkhead hands one released permit to its queue and shutdown joins all remaining requests", { timeout: 5000 }, async t => {
  const responses = [];
  const dependency = http.createServer((_request, response) => responses.push(response));
  const dependencyURL = await listen(dependency);
  const fixture = createNativeBulkheadCaller({ dependencyURL, namespace: randomUUID().replaceAll("-", ""), token: "c".repeat(64) });
  const appURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); await close(dependency); });
  const admitted = [fetch(appURL + "/query"), fetch(appURL + "/query")];
  await waitFor(() => responses.length === 2);
  const queued = [fetch(appURL + "/query"), fetch(appURL + "/query")];
  await waitFor(() => fixture.status().queued_requests === 2);
  const excess = await fetch(appURL + "/query"); assert.equal(excess.status, 503);
  assert.deepEqual(await excess.json(), { error: "bulkhead_rejected", retryable: true, attempts: 0 });
  responses[0].writeHead(200, { "content-type": "application/json" }); responses[0].end(JSON.stringify({ ready: true, value: 42 }));
  await waitFor(() => responses.length === 3);
  assert.equal(fixture.status().active_dependencies, 2); assert.equal(fixture.status().queued_requests, 1);
  assert.equal(fixture.status().permits_granted, 3); assert.equal(fixture.status().permits_released, 1);
  await fixture.stop(); await Promise.all([...admitted, ...queued]); await fixture.stop();
  assert.equal(responses.length, 3); assert.equal(fixture.status().permits_granted, fixture.status().permits_released);
  assert.equal(fixture.status().queue_cancellations, 1);
  for (const field of ["active_requests", "active_dependencies", "queued_requests", "queue_timers", "deadline_timers"]) assert.equal(fixture.status()[field], 0);
});
