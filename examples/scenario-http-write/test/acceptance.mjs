import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

export const order = { customer: "customer-one", sku: "item-one", quantity: 1 };
export async function verifyWrite(baseURL, providerURL, phase) {
  const get = async path => {
    const response = await fetch(providerURL + path, { signal: AbortSignal.timeout(2000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const checkout = async (key, payload = order) => {
    const response = await fetch(baseURL + "/checkout", {
      method: "POST", headers: { "idempotency-key": key, "content-type": "application/json" },
      body: JSON.stringify(payload), signal: AbortSignal.timeout(6000),
    });
    return { status: response.status, body: await response.json() };
  };
  const key = phase === "baseline" ? "baseline" : "ambiguous";
  const before = await get("/stats");
  const result = await checkout(key);
  if (phase === "outage") {
    assert.equal(result.status, 503);
    assert.equal(result.body.payment_outcome, "unknown");
    assert.equal(result.body.attempts, 3);
    assert.deepEqual(result.body.statuses, [201, 201, 201]);
    assert.deepEqual(result.body.body_errors, ["body_read_failed", "body_read_failed", "body_read_failed"]);
    const after = await get("/stats");
    assert.equal(after.charges, 2, "upstream must have committed despite lost replies");
    assert.equal(after.attempts - before.attempts, 3, "proxy or caller added hidden retries");
    const conflict = await checkout(key, { ...order, quantity: 2 });
    assert.equal(conflict.status, 409, "conflicting payload must pass through the response fault");
    assert.equal(conflict.body.attempts, 1);
    assert.equal((await get("/stats")).charges, 2);
  } else if (phase === "baseline" || phase === "recovery") {
    assert.equal(result.status, 200);
    assert.equal(result.body.attempts, 1);
    assert.equal(result.body.id, key);
    assert.equal(result.body.status, "confirmed");
    const charges = phase === "baseline" ? 1 : 2;
    assert.equal((await get("/stats")).charges, charges);
    if (phase === "recovery") {
      for (let i = 0; i < 3; i++) assert.equal((await checkout(key)).status, 200);
      assert.equal((await get("/stats")).charges, charges, "confirmed replays duplicated a charge");
      assert.equal((await checkout(key, { ...order, sku: "changed" })).status, 409);
    }
  } else throw new Error("unknown write acceptance phase");
  assert.deepEqual(await get(`/charges/${key}`), { id: key, status: "confirmed", order });
  console.log(JSON.stringify({ phase, ...result.body }));
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyWrite(process.env.GREGALE_TEST_URL, process.env.GREGALE_TEST_SERVICE_PAYMENT_URL, process.argv[2]);
}
