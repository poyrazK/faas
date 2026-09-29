import assert from "node:assert/strict";
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
  const workerURL = await listen(t, createWorker({ store, notificationURL: `${sinkURL}/deliver`, runID: "simulated-run" }));
  const appURL = await listen(t, createExportAPI({ workerURL }));
  const headers = (key) => ({ Authorization: `Bearer ${key}`, "Content-Type": "application/json" });
  const body = JSON.stringify({ idempotency_key: "same-input", report: "Customer A report" });
  const submit = () => fetch(`${appURL}/exports`, { method: "POST", headers: headers("customer-a-key"), body });

  const first = await submit();
  assert.equal(first.status, 202);
  const created = await first.json();
  assert.equal(created.created, true);
  assert.deepEqual(created.delivery_statuses, [503, 200]);
  const duplicate = await submit();
  assert.equal(duplicate.status, 200);
  assert.deepEqual(await duplicate.json(), { id: created.id, created: false });
  assert.equal(objects.size, 1);

  const owned = await fetch(`${appURL}/exports/${created.id}`, { headers: headers("customer-a-key") });
  assert.equal(owned.status, 200);
  assert.deepEqual(await owned.json(), { id: created.id, report: "Customer A report", run_id: "simulated-run" });
  assert.equal((await fetch(`${appURL}/exports/${created.id}`, { headers: headers("customer-b-key") })).status, 403);
  assert.equal((await fetch(`${appURL}/exports/${created.id}`)).status, 401);

  const evidence = await fetch(`${sinkURL}/__gregale_test__/attempts`, {
    headers: { Authorization: "Bearer fixture-token" },
  });
  assert.equal(evidence.status, 200);
  assert.deepEqual((await evidence.json()).attempts.map(({ status }) => status), [503, 200]);
});
