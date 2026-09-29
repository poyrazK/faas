import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import http from "node:http";
import { test } from "node:test";
import { createExportAPI } from "../server.js";
import { createWorker } from "../../export-worker/server.js";
import { createDeliverySink } from "../../../../cmd/gregale/scenario_fixtures/delivery-sink/server.js";

async function listen(t, server) {
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  return `http://127.0.0.1:${server.address().port}`;
}

test("export crosses gateway, worker, object store and retrying notification", async (t) => {
  const objects = new Map();
  const store = {
    get: async (key) => objects.get(key) ?? null,
    put: async (key, value) => { objects.set(key, value); },
  };
  const sinkURL = await listen(t, createDeliverySink({ failFirst: 1, token: "fixture-token" }));
  const workerURL = await listen(t, createWorker({ store, notificationURL: `${sinkURL}/deliver`, runID: "simulated-run", workerToken: "simulated-secret", failFirstProcess: true }));
  const queued = new Map();
  const pending = [];
  const ingressURL = await listen(t, http.createServer(async (request, response) => {
    const path = new URL(request.url, "http://localhost").pathname;
    if (request.method === "POST" && path === "/process") {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const key = request.headers["idempotency-key"];
      if (!queued.has(key)) {
        const invocation = { id: randomUUID(), state: "pending" };
        queued.set(key, invocation);
        pending.push((async () => {
          for (let attempt = 1; attempt <= 3; attempt++) {
            const result = await fetch(`${workerURL}/process`, {
              method: "POST", body: Buffer.concat(chunks),
              headers: { "content-type": "application/json", "x-owner-digest": request.headers["x-owner-digest"], "x-worker-test-token": request.headers["x-worker-test-token"] },
            });
            invocation.attempts = attempt;
            if (result.ok) {
              invocation.state = "completed";
              return;
            }
          }
          invocation.state = "dead_letter";
        })());
      }
      response.writeHead(202, { "content-type": "application/json" });
      return response.end(JSON.stringify({ id: queued.get(key).id }));
    }
    const result = await fetch(`${workerURL}${path}`, {
      method: request.method, headers: { "x-owner-digest": request.headers["x-owner-digest"], "x-worker-test-token": request.headers["x-worker-test-token"] },
    });
    response.writeHead(result.status, { "content-type": "application/json" });
    response.end(await result.text());
  }));
  const appURL = await listen(t, createExportAPI({ workerURL: ingressURL, workerToken: "simulated-secret" }));
  const headers = (key) => ({ Authorization: `Bearer ${key}`, "Content-Type": "application/json" });
  const body = JSON.stringify({ idempotency_key: "same-input", report: "Customer A report" });
  const submit = () => fetch(`${appURL}/exports`, { method: "POST", headers: headers("customer-a-key"), body });

  const first = await submit();
  assert.equal(first.status, 202);
  const created = await first.json();
  assert.equal(created.created, true);
  await Promise.all(pending);
  assert.equal([...queued.values()][0].state, "completed");
  assert.equal([...queued.values()][0].attempts, 2);
  const duplicate = await submit();
  assert.equal(duplicate.status, 202);
  assert.deepEqual(await duplicate.json(), created);
  assert.equal(queued.size, 1);
  assert.equal([...objects.keys()].filter((key) => key.startsWith("reports/")).length, 1);

  const owned = await fetch(`${appURL}/exports/${created.id}`, { headers: headers("customer-a-key") });
  assert.equal(owned.status, 200);
  assert.deepEqual(await owned.json(), { id: created.id, report: "Customer A report", run_id: "simulated-run" });
  assert.equal((await fetch(`${appURL}/exports/${created.id}`, { headers: headers("customer-b-key") })).status, 403);
  assert.equal((await fetch(`${appURL}/exports/${created.id}`)).status, 401);
  const owner = createHash("sha256").update("customer-a-key").digest("hex");
  assert.equal((await fetch(`${workerURL}/result/${created.id}`, { headers: { "x-owner-digest": owner } })).status, 401);

  const evidence = await fetch(`${sinkURL}/__gregale_test__/attempts`, {
    headers: { Authorization: "Bearer fixture-token" },
  });
  assert.equal(evidence.status, 200);
  assert.deepEqual((await evidence.json()).attempts.map(({ status }) => status), [503, 200]);
});
