import assert from "node:assert/strict";
import { createHash } from "node:crypto";

const customerHeaders = (key) => ({ Authorization: `Bearer ${key}`, "Content-Type": "application/json" });

export async function submitCustomerExport({ url, runID, customerA }) {
  const response = await fetch(`${url}/exports`, {
    method: "POST",
    headers: customerHeaders(customerA),
    body: JSON.stringify({ idempotency_key: `export-${runID}`, report: "Customer A report" }),
  });
  const raw = await response.text();
  assert.equal(response.status, 202, raw);
  const body = JSON.parse(raw);
  assert.equal(body.created, true);
  assert.match(body.invocation_id, /^[a-f0-9-]{36}$/);
  return body;
}

export async function assertCustomerExport({ url, workerURL, sinkURL, sinkToken, runID, customerA, customerB, invocationID }) {
  const owner = createHash("sha256").update(customerA).digest("hex");
  const id = createHash("sha256").update(`${owner}:export-${runID}`).digest("hex").slice(0, 32);

  const duplicate = await submitCustomerExport({ url, runID, customerA });
  assert.deepEqual(duplicate, { id, invocation_id: invocationID, created: true });

  const owned = await fetch(`${url}/exports/${id}`, { headers: customerHeaders(customerA) });
  assert.equal(owned.status, 200);
  assert.deepEqual(await owned.json(), { id, report: "Customer A report", run_id: runID });
  assert.equal((await fetch(`${url}/exports/${id}`, { headers: customerHeaders(customerB) })).status, 403);
  assert.equal((await fetch(`${url}/exports/${id}`)).status, 401);

  const directWorker = await fetch(`${workerURL}/result/${id}`, { headers: { "x-owner-digest": owner } });
  assert.equal(directWorker.status, 401);

  const sink = await fetch(`${sinkURL}/__gregale_test__/attempts`, {
    headers: { Authorization: `Bearer ${sinkToken}` },
  });
  assert.equal(sink.status, 200);
  const { attempts } = await sink.json();
  assert.deepEqual(attempts.map(({ status }) => status), [503, 200]);
  for (const attempt of attempts) {
    assert.deepEqual(JSON.parse(attempt.body), { export_id: id, run_id: runID });
  }
  return id;
}
