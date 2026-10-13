import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { order } from "./acceptance.mjs";

export async function verifyDependency(appURL, providerURL, stage) {
  assert.ok(["pending", "committed"].includes(stage));
  const get = async path => {
    const response = await fetch(providerURL + path, { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const write = async payload => {
    const response = await fetch(appURL + "/checkout", { method: "POST", headers: { "content-type": "application/json", "idempotency-key": "in-flight" }, body: JSON.stringify(payload), signal: AbortSignal.timeout(5000) });
    return { status: response.status, body: await response.json() };
  };
  const before = await get("/stats");
  assert.equal(before.held, 0);
  assert.equal(before.charges, stage === "pending" ? 1 : 2);
  assert.equal(before.pending, stage === "pending" ? 1 : 0);
  const expected = { received: true, accepted: true, pending: stage === "pending", committed: stage === "committed", held: false };
  assert.deepEqual(await get("/checkpoints/in-flight"), expected);
  assert.equal((await write({ ...order, quantity: 2 })).status, 409);
  assert.deepEqual(await get("/checkpoints/in-flight"), expected);
  const results = await Promise.all(Array.from({ length: 4 }, () => write(order)));
  for (const result of results) { assert.equal(result.status, 200); assert.equal(result.body.status, "confirmed"); assert.equal(result.body.attempts, 1); }
  assert.equal((await write({ ...order, quantity: 2 })).status, 409);
  const after = await get("/stats");
  assert.equal(after.ledger_id, before.ledger_id);
  assert.equal(after.charges, 2);
  assert.equal(after.pending, 0);
  assert.equal(after.held, 0);
  assert.equal(after.attempts - before.attempts, 6);
  assert.deepEqual(await get("/charges/in-flight"), { id: "in-flight", status: "confirmed", order });
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyDependency(process.env.GREGALE_TEST_URL, process.env.GREGALE_TEST_SERVICE_PAYMENT_URL, process.argv[2]);
}
