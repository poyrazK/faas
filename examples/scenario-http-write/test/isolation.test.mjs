import assert from "node:assert/strict";
import http from "node:http";
import { randomUUID } from "node:crypto";
import test from "node:test";
import { createNativeIsolationCaller } from "../native-bulkhead-app/isolation.js";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
async function waitFor(predicate) {
  const end = Date.now() + 800;
  while (!predicate() && Date.now() < end) await new Promise(resolve => setTimeout(resolve, 2));
  assert.ok(predicate());
}

test("saturating either dependency leaves the other's permits and retry budget usable; cancellation joins both", { timeout: 5000 }, async t => {
  const namespace = randomUUID().replaceAll("-", ""), token = "d".repeat(64), servers = {}, dependencyURLs = {};
  let faulted = "a";
  for (const name of ["a", "b"]) {
    servers[name] = http.createServer((_request, response) => {
      if (name === faulted) return;
      response.writeHead(200, { "content-type": "application/json" }); response.end(JSON.stringify({ ready: true, value: 42 }));
    });
    dependencyURLs[name] = await listen(servers[name]);
  }
  const fixture = createNativeIsolationCaller({ dependencyURLs, namespace, token }), appURL = await listen(fixture.server);
  t.after(async () => { await fixture.stop(); await close(fixture.server); await Promise.all(Object.values(servers).map(close)); });
  assert.equal((await fetch(appURL + "/isolation-control", { method: "POST" })).status, 401);
  assert.equal((await fetch(appURL + "/isolation-control", { method: "POST", headers: { authorization: "Bearer " + token, "x-test-namespace": "e".repeat(32) } })).status, 400);
  for (const name of ["a", "b"]) {
    faulted = name; const healthy = name === "a" ? "b" : "a";
    const controller = new AbortController();
    const wave = Array.from({ length: 4 }, () => fetch(appURL + "/" + name + "/query", { signal: controller.signal }).catch(() => undefined));
    await waitFor(() => fixture.status().dependencies[name].active_dependencies === 2 && fixture.status().dependencies[name].queued_requests === 2);
    for (let index = 0; index < 6; index++) {
      const result = await fetch(appURL + "/" + healthy + "/query");
      assert.equal(result.status, 200); assert.deepEqual(await result.json(), { ready: true, value: 42, attempts: 1 });
    }
    const evidence = fixture.status();
    assert.equal(evidence.dependencies[name].active_dependencies, 2);
    assert.equal(evidence.dependencies[healthy].remaining, 2); assert.equal(evidence.dependencies[healthy].retries_admitted, 0);
    assert.equal(evidence.dependencies[healthy].window_id, 0); assert.equal(evidence.dependencies[healthy].state, "closed");
    const health = await fetch(appURL + "/health"); assert.equal(health.status, 200);
    assert.deepEqual(await health.json(), { ready: true, active_dependencies: { [name]: 2, [healthy]: 0 } });
    controller.abort(); await Promise.all(wave);
    await waitFor(() => fixture.status().dependencies[name].active_requests === 0);
    const retired = fixture.status().dependencies[name];
    assert.equal(retired.permits_granted, retired.permits_released);
    for (const key of ["active_dependencies", "queued_requests", "queue_timers", "retry_timers", "deadline_timers"]) assert.equal(retired[key], 0);
  }
  await fixture.stop(); await fixture.stop();
  const final = fixture.status(); assert.equal(final.stopped, true);
  for (const value of Object.values(final.dependencies)) assert.equal(value.stopped, true);
  const stopped = await fetch(appURL + "/a/query"); assert.equal(stopped.status, 503); assert.deepEqual(await stopped.json(), { error: "fixture_stopped" });
});
