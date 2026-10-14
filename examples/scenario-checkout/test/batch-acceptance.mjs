import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import { pathToFileURL } from "node:url";

export const batchOrders = Array.from({ length: 6 }, (_, index) => ({
  key: `batch-${index}`, order: { customer: `customer-${index}`, sku: `item-${index % 3}`, quantity: index + 1 },
}));

export async function verifyBatch(baseURL, paymentURL, phase) {
  const get = async (url, signal) => {
    const response = await fetch(url, { signal: signal || AbortSignal.timeout(2000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const checkout = async (key, order, signal) => {
    const response = await fetch(`${baseURL}/checkout`, {
      method: "POST", headers: { "idempotency-key": key, "content-type": "application/json" },
      body: JSON.stringify(order), signal: signal || AbortSignal.timeout(2000),
    });
    return { status: response.status, body: await response.json() };
  };
  const checkRecords = async state => {
    for (const { key, order } of batchOrders) {
      const saved = await get(`${baseURL}/orders/${key}`);
      const charge = await get(`${paymentURL}/charges/${key}`);
      assert.deepEqual(saved, { id: key, status: state, order });
      assert.deepEqual(charge, { id: key, status: "confirmed", order });
    }
  };
  const conflictChecks = async () => {
    const before = await get(`${paymentURL}/stats`);
    for (const { key, order } of batchOrders) {
      const result = await checkout(key, { ...order, quantity: order.quantity + 1 });
      assert.equal(result.status, 409);
      assert.deepEqual(result.body, { error: "idempotency_conflict" });
    }
    assert.deepEqual(await get(`${paymentURL}/stats`), before, "conflicting payload contacted payment");
  };
  if (phase === "outage") {
    const before = await get(`${baseURL}/stats`);
    const results = await Promise.all(batchOrders.flatMap(({ key, order }) =>
      Array.from({ length: 3 }, () => checkout(key, order))));
    for (const result of results) {
      assert.equal(result.status, 503);
      assert.deepEqual(result.body, { error: "payment_unavailable", payment_outcome: "unknown", retryable: true });
    }
    const after = await get(`${baseURL}/stats`);
    assert.equal(after.orders, 1);
    assert.equal(after.pending, 0);
    assert.equal(after.payment_attempts - before.payment_attempts, batchOrders.length * 2, "concurrent duplicate requests exceeded retry budget");
    assert.equal((await get(`${paymentURL}/stats`)).charges, 1 + batchOrders.length);
    await checkRecords("pending");
    await conflictChecks();
  } else if (phase === "recovery") {
    const signal = AbortSignal.timeout(10000);
    await Promise.all(batchOrders.flatMap(({ key, order }) => Array.from({ length: 3 }, async () => {
      while (!signal.aborted) {
        const result = await checkout(key, order, signal);
        if (result.status === 200) {
          assert.deepEqual(result.body, { id: key, status: "confirmed" });
          return;
        }
        assert.equal(result.status, 503);
        await delay(100, undefined, { signal });
      }
      throw new Error("batch recovery deadline exceeded");
    })));
    await checkRecords("confirmed");
    await conflictChecks();
    const stats = await get(`${baseURL}/stats`);
    assert.equal(stats.orders, 1 + batchOrders.length);
    assert.equal(stats.pending, 0);
    assert.equal((await get(`${paymentURL}/stats`)).charges, 1 + batchOrders.length);
    const before = await get(`${paymentURL}/stats`);
    for (const { key, order } of batchOrders) assert.equal((await checkout(key, order)).status, 200);
    assert.deepEqual(await get(`${paymentURL}/stats`), before, "confirmed replay contacted payment again");
  } else throw new Error(`unknown batch phase: ${phase}`);
  console.log(JSON.stringify({ phase: `batch-${phase}`, customers: batchOrders.length, duplicate_requests_per_key: 3 }));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyBatch(process.env.GREGALE_TEST_URL, process.env.GREGALE_TEST_SERVICE_PAYMENT_URL, process.argv[2]);
}
