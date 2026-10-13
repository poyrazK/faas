import assert from "node:assert/strict";
import http from "node:http";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { createNativeBreakerCaller } from "../native-breaker-app/server.js";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }

test("late closed-state success preserves a circuit opened by newer failures", { timeout: 5000 }, async t => {
  let calls = 0, first;
  const dependency = http.createServer((_request, response) => {
    calls++;
    if (calls === 1) { first = response; return; }
    response.writeHead(503, { "content-type": "application/json" }); response.end('{}');
  });
  const dependencyURL = await listen(dependency);
  const fixture = createNativeBreakerCaller({ dependencyURL, namespace: randomUUID().replaceAll("-", ""), token: "b".repeat(64) });
  const appURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); await close(dependency); });
  const late = fetch(appURL + "/query");
  const end = Date.now() + 1000;
  while (!first && Date.now() < end) await new Promise(resolve => setTimeout(resolve, 1));
  assert.ok(first);
  for (let i = 0; i < 2; i++) assert.equal((await fetch(appURL + "/query")).status, 503);
  assert.equal(fixture.status().state, "open");
  first.writeHead(200, { "content-type": "application/json" }); first.end(JSON.stringify({ ready: true, value: 42 }));
  assert.equal((await late).status, 200);
  assert.equal(fixture.status().state, "open"); assert.equal(fixture.status().closed_transitions, 0);
  const blocked = await fetch(appURL + "/query"); assert.equal(blocked.status, 503);
  assert.deepEqual(await blocked.json(), { error: "circuit_open", retryable: true, attempts: 0 });
  assert.equal(calls, 3); await fixture.stop(); await fixture.stop();
  for (const field of ["active_requests", "active_probes", "deadline_timers"]) assert.equal(fixture.status()[field], 0);
});
