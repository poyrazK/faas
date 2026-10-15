import assert from "node:assert/strict";
import http from "node:http";
import { performance } from "node:perf_hooks";
import test from "node:test";
import { createRetryServer, fetchWithRetry } from "../app/server.js";
import { verifyRetry } from "./acceptance.mjs";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function dependency(t, handler) {
  const calls = [];
  const server = http.createServer((request, response) => { calls.push(performance.now()); handler(response, calls.length); });
  const url = await listen(server);
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  return { url, calls };
}
const limited = response => { response.writeHead(429, { "retry-after": "1" }); response.end(); };
const available = response => { response.writeHead(200, { "content-type": "application/json" }); response.end('{"available":true}'); };

test("429 waits a full Retry-After interval and stops after three attempts", async t => {
  const provider = await dependency(t, limited);
  const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(5000) });
  assert.equal(result.status, 503);
  assert.equal(result.body.attempts, 3);
  assert.deepEqual(result.body.statuses, [429, 429, 429]);
  assert.equal(provider.calls.length, 3);
  for (const wait of result.body.waits_ms) assert.ok(wait >= 1000);
  for (let i = 1; i < provider.calls.length; i++) assert.ok(provider.calls[i] - provider.calls[i - 1] >= 1000);
});

test("dependency can recover while the caller waits to retry", async t => {
  const provider = await dependency(t, (response, count) => count === 1 ? limited(response) : available(response));
  const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(5000) });
  assert.equal(result.status, 200);
  assert.equal(result.body.attempts, 2);
  assert.equal(result.body.dependency_available, true);
  assert.deepEqual(result.body.statuses, [429, 200]);
  assert.equal(provider.calls.length, 2);
});

test("503 Retry-After is respected too", async t => {
  const provider = await dependency(t, (response, count) => {
    if (count === 1) { response.writeHead(503, { "retry-after": "1" }); response.end(); }
    else available(response);
  });
  const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(5000) });
  assert.equal(result.status, 200);
  assert.deepEqual(result.body.statuses, [503, 200]);
  assert.ok(result.body.waits_ms[0] >= 1000);
});

test("acceptance commands prove active fault, bounded attempts and recovery", async t => {
  let fault = false;
  const provider = await dependency(t, response => fault ? limited(response) : available(response));
  const app = createRetryServer({ dependencyURL: provider.url });
  const url = await listen(app);
  t.after(async () => { app.closeAllConnections(); await new Promise(resolve => app.close(resolve)); });
  await verifyRetry(url, "baseline");
  await assert.rejects(verifyRetry(url, "outage"), "unapplied fault must fail acceptance");
  fault = true;
  await verifyRetry(url, "outage");
  fault = false;
  await verifyRetry(url, "recovery");
});

test("missing or invalid retry headers cannot produce a retry loop", async t => {
  for (const header of [null, "0", "-1", "1.5", "31", "tomorrow"]) {
    const provider = await dependency(t, response => {
      response.writeHead(429, header === null ? {} : { "retry-after": header }); response.end();
    });
    const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(2000) });
    assert.equal(result.body.error, "invalid_retry_after");
    assert.equal(provider.calls.length, 1);
  }
});

test("total deadline cancels a long Retry-After wait before another attempt", async t => {
  const provider = await dependency(t, response => { response.writeHead(429, { "retry-after": "30" }); response.end(); });
  const app = createRetryServer({ dependencyURL: provider.url, deadlineMS: 100 });
  const url = await listen(app);
  t.after(async () => { app.closeAllConnections(); await new Promise(resolve => app.close(resolve)); });
  const response = await fetch(`${url}/fetch`, { signal: AbortSignal.timeout(2000) });
  assert.equal(response.status, 503);
  assert.deepEqual(await response.json(), { error: "dependency_deadline" });
  assert.equal(provider.calls.length, 1);
});

function brokenBody(response, mode) {
  const prefix = '{"available":';
  const complete = '{"available":true}';
  response.writeHead(200, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(mode === "malformed" ? prefix : complete),
    connection: "close",
  });
  response.end(prefix);
}

test("malformed JSON and truncated bodies exhaust only three read attempts", async t => {
  for (const mode of ["malformed", "truncated"]) {
    const provider = await dependency(t, response => brokenBody(response, mode));
    const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(3000) });
    assert.equal(result.status, 503);
    assert.equal(result.body.error, "invalid_dependency_response");
    assert.equal(result.body.attempts, 3);
    assert.equal(provider.calls.length, 3);
    assert.deepEqual(result.body.statuses, [200, 200, 200]);
    assert.deepEqual(result.body.body_errors, Array(3).fill(mode === "malformed" ? "malformed_json" : "body_read_failed"));
  }
});

test("valid response during body-fault retries confirms success once", async t => {
  let successful = 0;
  const provider = await dependency(t, (response, count) => {
    if (count <= 2) brokenBody(response, "malformed");
    else { successful++; available(response); }
  });
  const result = await fetchWithRetry(provider.url, { signal: AbortSignal.timeout(3000) });
  assert.equal(result.status, 200);
  assert.equal(result.body.dependency_available, true);
  assert.equal(result.body.attempts, 3);
  assert.equal(successful, 1);
  assert.deepEqual(result.body.body_errors, ["malformed_json", "malformed_json"]);
});

test("body-fault acceptance proves controlled failures and recovery after clear", async t => {
  let mode;
  const provider = await dependency(t, response => mode ? brokenBody(response, mode) : available(response));
  const app = createRetryServer({ dependencyURL: provider.url });
  const url = await listen(app);
  t.after(async () => { app.closeAllConnections(); await new Promise(resolve => app.close(resolve)); });
  for (const fault of ["malformed", "truncated"]) {
    mode = fault;
    const before = provider.calls.length;
    await verifyRetry(url, mode);
    assert.equal(provider.calls.length - before, 3);
    mode = undefined;
    await verifyRetry(url, "recovery");
  }
});

test("total deadline aborts a body that never finishes without retrying", async t => {
  const provider = await dependency(t, response => {
    response.writeHead(200, { "content-type": "application/json", "content-length": "18" });
    response.write('{"available":');
  });
  const app = createRetryServer({ dependencyURL: provider.url, deadlineMS: 100 });
  const url = await listen(app);
  t.after(async () => { app.closeAllConnections(); await new Promise(resolve => app.close(resolve)); });
  const response = await fetch(`${url}/fetch`, { signal: AbortSignal.timeout(2000) });
  assert.equal(response.status, 503);
  assert.deepEqual(await response.json(), { error: "dependency_deadline" });
  assert.equal(provider.calls.length, 1);
});
