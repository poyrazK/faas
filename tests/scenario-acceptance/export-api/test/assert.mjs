import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";

const url = process.env.GREGALE_TEST_URL;
const runID = process.env.GREGALE_TEST_RUN_ID;
const a = process.env.GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY;
const b = process.env.GREGALE_TEST_CONSUMER_CUSTOMER_B_KEY;
const owner = createHash("sha256").update(a).digest("hex");
const id = createHash("sha256").update(`${owner}:export-${runID}`).digest("hex").slice(0, 32);
const firstInvocation = JSON.parse(await readFile(process.env.GREGALE_TEST_TRIGGER_OUTPUT, "utf8")).worker_invocation_id;
const headers = (key) => ({ Authorization: `Bearer ${key}`, "Content-Type": "application/json" });

const duplicate = await fetch(`${url}/exports`, {
  method: "POST", headers: headers(a),
  body: JSON.stringify({ idempotency_key: `export-${runID}`, report: "Customer A report" }),
});
const duplicateBody = await duplicate.json();
assert.equal(duplicate.status, 202, JSON.stringify(duplicateBody));
assert.deepEqual(duplicateBody, { id, invocation_id: firstInvocation, created: true });
const owned = await fetch(`${url}/exports/${id}`, { headers: headers(a) });
assert.equal(owned.status, 200);
assert.deepEqual(await owned.json(), { id, report: "Customer A report", run_id: runID });

const forbidden = await fetch(`${url}/exports/${id}`, { headers: headers(b) });
assert.equal(forbidden.status, 403);
const anonymous = await fetch(`${url}/exports/${id}`);
assert.equal(anonymous.status, 401);
const directWorker = await fetch(`${process.env.GREGALE_TEST_SERVICE_WORKER_URL}/result/${id}`, {
  headers: { "x-owner-digest": owner },
});
assert.equal(directWorker.status, 401);

const sink = await fetch(`${process.env.GREGALE_TEST_SERVICE_NOTIFICATIONS_URL}/__gregale_test__/attempts`, {
  headers: { Authorization: `Bearer ${process.env.GREGALE_TEST_SINK_NOTIFICATIONS_TOKEN}` },
});
assert.equal(sink.status, 200);
const { attempts } = await sink.json();
assert.deepEqual(attempts.map(({ status }) => status), [503, 200]);
for (const attempt of attempts) {
  assert.deepEqual(JSON.parse(attempt.body), { export_id: id, run_id: runID });
}
