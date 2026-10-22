import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { order } from "./acceptance.mjs";

export const checkpointStates = Object.freeze({
  "before-acceptance": { received: true, accepted: false, pending: false, committed: false, held: true },
  pending: { received: true, accepted: true, pending: true, committed: false, held: true },
  committed: { received: true, accepted: true, pending: false, committed: true, held: true },
});

export async function verifyInFlight(appURL, providerURL, stage = "pending") {
  assert.ok(Object.hasOwn(checkpointStates, stage), "unknown write checkpoint");
  const get = async path => {
    const response = await fetch(providerURL + path, { signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const checkout = async payload => {
    const response = await fetch(appURL + "/checkout", {
      method: "POST", headers: { "content-type": "application/json", "idempotency-key": "in-flight" },
      body: JSON.stringify(payload), signal: AbortSignal.timeout(5000),
    });
    return { status: response.status, body: await response.json() };
  };
  assert.deepEqual(await get("/checkpoints/in-flight"), checkpointStates[stage]);
  const before = await get("/stats");
  assert.equal(before.charges, stage === "committed" ? 2 : 1);
  assert.equal(before.pending, stage === "pending" ? 1 : 0);
  assert.equal(before.held, 1);
  assert.equal((await checkout({ ...order, quantity: 2 })).status, 409);
  assert.deepEqual(await get("/checkpoints/in-flight"), checkpointStates[stage]);
  for (let i = 0; i < 4; i++) {
    const result = await checkout(order);
    assert.equal(result.status, 200);
    assert.equal(result.body.status, "confirmed");
    assert.equal(result.body.attempts, 1);
  }
  assert.equal((await checkout({ ...order, quantity: 2 })).status, 409);
  const after = await get("/stats");
  assert.equal(after.ledger_id, before.ledger_id);
  assert.equal(after.charges, 2);
  assert.equal(after.pending, 0);
  assert.equal(after.held, 0);
  assert.equal(after.attempts - before.attempts, 6);
  assert.deepEqual(await get("/charges/in-flight"), { id: "in-flight", status: "confirmed", order });
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyInFlight(process.env.GREGALE_TEST_URL, process.env.GREGALE_TEST_SERVICE_PAYMENT_URL, process.argv[2] || "pending");
}
