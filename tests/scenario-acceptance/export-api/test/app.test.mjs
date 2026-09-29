import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import http from "node:http";
import { test } from "node:test";
import { createExportAPI } from "../server.js";
import { createWorker } from "../../export-worker/server.js";
import { createDeliverySink } from "../../../../cmd/gregale/scenario_fixtures/delivery-sink/server.js";
import { assertCustomerExport, submitCustomerExport } from "./contract.mjs";

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
  const runID = "simulated-run";
  const customerA = "customer-a-key";
  const customerB = "customer-b-key";
  const created = await submitCustomerExport({ url: appURL, runID, customerA });
  await Promise.all(pending);
  assert.equal([...queued.values()][0].state, "completed");
  assert.equal([...queued.values()][0].attempts, 2);
  await assertCustomerExport({
    url: appURL, workerURL, sinkURL, sinkToken: "fixture-token",
    runID, customerA, customerB, invocationID: created.invocation_id,
  });
  assert.equal(queued.size, 1);
  assert.equal([...objects.keys()].filter((key) => key.startsWith("reports/")).length, 1);
});
