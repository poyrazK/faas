import assert from "node:assert/strict";
import http from "node:http";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { createNativeRetryCaller } from "../native-retry-app/server.js";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }

test("canceling a shared-budget retry delay joins timers and sends no second dependency request", { timeout: 5000 }, async t => {
  let calls = 0;
  const dependency = http.createServer((_request, response) => { calls++; response.writeHead(503, { "content-type": "application/json" }); response.end('{}'); });
  const dependencyURL = await listen(dependency);
  const fixture = createNativeRetryCaller({ dependencyURL, namespace: randomUUID().replaceAll("-", ""), token: "a".repeat(64) });
  const appURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); await close(dependency); });
  const controller = new AbortController();
  const pending = fetch(appURL + "/query", { signal: controller.signal }).then(response => ({ status: response.status }), error => ({ aborted: error.name === "AbortError" }));
  const end = Date.now() + 1000;
  while (Date.now() < end && fixture.status().retry_timers !== 1) await new Promise(resolve => setTimeout(resolve, 1));
  assert.equal(fixture.status().retry_timers, 1);
  controller.abort(); assert.deepEqual(await pending, { aborted: true });
  await fixture.stop(); await fixture.stop();
  assert.equal(calls, 1); assert.equal(fixture.status().dependency_attempts, 1);
  assert.equal(fixture.status().retries_admitted, 1); assert.equal(fixture.status().remaining, 1);
  for (const field of ["active_requests", "retry_timers", "deadline_timers"]) assert.equal(fixture.status()[field], 0);
});
