import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import http from "node:http";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { createWriteCaller } from "../app/server.js";
import { createWriteProvider } from "../payment/server.js";
import { order, verifyWrite } from "./acceptance.mjs";

async function listen(server, port = 0) {
  await new Promise(resolve => server.listen(port, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) {
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
async function fixture(t, deadlineMS = 5000) {
  const directory = await mkdtemp(join(tmpdir(), "gregale-http-write-"));
  const ledgerPath = join(directory, "charges.json");
  let provider = createWriteProvider({ ledgerPath });
  let providerURL = await listen(provider);
  const state = { lost: false, neverFinish: false };
  const proxy = http.createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const upstream = await fetch(providerURL + request.url, {
      method: request.method, headers: request.headers,
      body: request.method === "GET" ? undefined : Buffer.concat(chunks),
      signal: AbortSignal.timeout(2000),
    });
    const body = Buffer.from(await upstream.arrayBuffer()); // Drain after the committed write.
    if (state.lost && upstream.status >= 200 && upstream.status < 300 && body.length > 0) {
      response.writeHead(upstream.status, { "content-length": "1", connection: "close" });
      if (state.neverFinish) response.flushHeaders();
      else response.end();
    } else {
      response.writeHead(upstream.status, { "content-type": "application/json" });
      response.end(body);
    }
  });
  const proxyURL = await listen(proxy);
  const app = createWriteCaller({ dependencyURL: proxyURL, deadlineMS });
  const appURL = await listen(app);
  t.after(async () => { await close(app); await close(proxy); await close(provider); await rm(directory, { recursive: true, force: true }); });
  const get = async path => (await fetch(providerURL + path)).json();
  const checkout = async (key, payload = order) => {
    const response = await fetch(appURL + "/checkout", {
      method: "POST", headers: { "idempotency-key": key, "content-type": "application/json" },
      body: JSON.stringify(payload), signal: AbortSignal.timeout(6000),
    });
    return { status: response.status, body: await response.json() };
  };
  return { state, appURL, get providerURL() { return providerURL; }, get, checkout, ledgerPath, async restartProvider() {
    await close(provider);
    provider = createWriteProvider({ ledgerPath });
    // A fresh endpoint avoids a cached keep-alive socket to the retired server.
    // The proxy resolves this mutable URL on each request.
    providerURL = await listen(provider);
  } };
}

test("committed charge survives three lost replies and reconciles once", async t => {
  const f = await fixture(t);
  await verifyWrite(f.appURL, f.providerURL, "baseline");
  f.state.lost = true;
  await verifyWrite(f.appURL, f.providerURL, "outage");
  const saved = JSON.parse(await readFile(f.ledgerPath, "utf8"));
  assert.equal(saved.entries.length, 2);
  await f.restartProvider();
  assert.equal((await f.get("/stats")).charges, 2);
  f.state.lost = false;
  await verifyWrite(f.appURL, f.providerURL, "recovery");
});

test("concurrent same-key retries produce one charge and reject conflicts", async t => {
  const f = await fixture(t);
  f.state.lost = true;
  const results = await Promise.all(Array.from({ length: 4 }, () => f.checkout("parallel")));
  for (const result of results) {
    assert.equal(result.status, 503);
    assert.equal(result.body.attempts, 3);
    assert.equal(result.body.payment_outcome, "unknown");
  }
  assert.equal((await f.get("/stats")).charges, 1);
  assert.equal((await f.get("/stats")).attempts, 12);
  const conflict = await f.checkout("parallel", { ...order, quantity: 2 });
  assert.equal(conflict.status, 409);
  assert.equal(conflict.body.attempts, 1);
  assert.deepEqual(await f.get("/charges/parallel"), { id: "parallel", status: "confirmed", order });
  f.state.lost = false;
  assert.equal((await f.checkout("parallel")).status, 200);
  assert.equal((await f.get("/stats")).charges, 1);
});

test("deadline leaves a committed unknown outcome without extra attempts", async t => {
  const f = await fixture(t, 100);
  f.state.lost = true;
  f.state.neverFinish = true;
  const result = await f.checkout("deadline");
  assert.equal(result.status, 503);
  assert.equal(result.body.payment_outcome, "unknown");
  assert.equal((await f.get("/stats")).charges, 1);
  assert.equal((await f.get("/stats")).attempts, 1);
  f.state.lost = false;
  assert.equal((await f.checkout("deadline")).status, 200);
  assert.equal((await f.get("/stats")).charges, 1);
});

test("invalid orders cannot reach the provider", async t => {
  const f = await fixture(t);
  assert.equal((await f.checkout("bad", { ...order, quantity: 0 })).status, 400);
  assert.equal((await f.get("/stats")).attempts, 0);
});

test("outage acceptance rejects a response fault that never took effect", async t => {
  const f = await fixture(t);
  await verifyWrite(f.appURL, f.providerURL, "baseline");
  await assert.rejects(verifyWrite(f.appURL, f.providerURL, "outage"));
});
