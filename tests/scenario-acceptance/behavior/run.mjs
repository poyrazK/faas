import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import http from "node:http";
import { createExportAPI } from "../export-api/server.js";
import { createWorker } from "../export-worker/server.js";
import { createDeliverySink } from "../../../cmd/gregale/scenario_fixtures/delivery-sink/server.js";
import { observeExport } from "./observe.mjs";
import { implementationVariant } from "./implementation.mjs";

// A portable simulation of two implementations. No platform or external services
// are contacted. The injected candidate notifier duplicates successful delivery.
const variant = process.env.GREGALE_BEHAVIOR_EXAMPLE_VARIANT || implementationVariant;
assert.ok(["baseline", "duplicate"].includes(variant), "unknown example implementation");
const fixture = JSON.parse(await readFile(new URL("fixture.json", import.meta.url), "utf8"));
const servers = [];
const pending = [];
const objects = new Map();
const invocations = new Map();
const workerToken = "behavior-worker-token";
const sinkToken = "behavior-sink-token";
const customer = "behavior-customer-key";
const runID = "behavior-example";
const store = { get: async (key) => objects.get(key) ?? null, put: async (key, value) => { objects.set(key, value); } };

async function listen(server) {
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  servers.push(server);
  return `http://127.0.0.1:${server.address().port}`;
}

try {
  const sinkURL = await listen(createDeliverySink({ failFirst: 1, token: sinkToken }));
  const workerURL = await listen(createWorker({ store, notificationURL: `${sinkURL}/deliver`, runID, workerToken, failFirstProcess: true,
    request: async (...args) => {
      const response = await fetch(...args);
      if (variant === "duplicate" && response.ok) {
        const duplicate = await fetch(...args);
        await duplicate.arrayBuffer();
      }
      return response;
    },
  }));
  const ingressURL = await listen(http.createServer(async (request, response) => {
    const path = new URL(request.url, "http://localhost").pathname;
    if (request.method !== "POST" || path !== "/process") {
      response.writeHead(404).end();
      return;
    }
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const key = request.headers["idempotency-key"];
    if (!invocations.has(key)) {
      const invocation = { id: randomUUID(), state: "pending", attempts: 0 };
      invocations.set(key, invocation);
      pending.push((async () => {
        for (let attempt = 1; attempt <= 3; attempt++) {
          const result = await fetch(`${workerURL}/process`, { method: "POST", body: Buffer.concat(chunks),
            headers: { "content-type": "application/json", "x-owner-digest": request.headers["x-owner-digest"], "x-worker-test-token": workerToken } });
          await result.arrayBuffer();
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
    response.end(JSON.stringify({ id: invocations.get(key).id }));
  }));
  const apiURL = await listen(createExportAPI({ workerURL: ingressURL, workerToken }));
  const response = await fetch(`${apiURL}/exports`, { method: "POST",
    headers: { authorization: `Bearer ${customer}`, "content-type": "application/json" }, body: JSON.stringify(fixture) });
  const body = await response.json();
  assert.equal(response.status, 202);
  assert.equal(body.created, true);
  await Promise.all(pending);
  assert.equal(invocations.size, 1);
  assert.equal([...invocations.values()][0].state, "completed");
  assert.equal([...invocations.values()][0].attempts, 2);
  const owner = createHash("sha256").update(customer).digest("hex");
  const id = createHash("sha256").update(`${owner}:${fixture.idempotency_key}`).digest("hex").slice(0, 32);
  const report = objects.get(`reports/${runID}/${id}.json`);
  assert.equal(report.report, fixture.report);
  const sinkResponse = await fetch(`${sinkURL}/__gregale_test__/attempts`, { headers: { authorization: `Bearer ${sinkToken}` } });
  assert.equal(sinkResponse.status, 200);
  const { attempts } = await sinkResponse.json();
  await observeExport({ status: response.status, created: body.created, storedReport: report.report, attempts });
} finally {
  await Promise.allSettled(pending);
  await Promise.all(servers.map((server) => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); })));
}
