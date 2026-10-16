import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fixture } from "./support/fixture.mjs";
import { batchOrders, verifyBatch } from "./batch-acceptance.mjs";
import { defaultOrder } from "../app/contract.js";

test("concurrent customer retries survive crash with one charge and matching order per key", async t => {
  const f = await fixture(t, true);
  f.drop(true);
  const requests = batchOrders.flatMap(({ key, order }) => Array.from({ length: 3 }, () => f.checkout(key, order).catch(() => undefined)));
  let charged = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if ((await f.paymentStats()).charges === batchOrders.length && (await f.orderStats()).pending === batchOrders.length) { charged = true; break; }
    await delay(10);
  }
  assert.ok(charged, "all customers must be charged with requests in flight before crash");
  await f.crashApp();
  await Promise.all(requests);
  await f.restartApp();
  f.drop(false);
  const results = await Promise.all(batchOrders.flatMap(({ key, order }) => Array.from({ length: 3 }, () => f.checkout(key, order))));
  assert.ok(results.every(result => result.status === 200));
  // One charge per key is the invariant. Recovery normally takes one provider
  // attempt per key, but each attempt has a 350 ms deadline and the app may
  // retry once, so a loaded CI runner can add an attempt without a charge.
  const recovered = await f.paymentStats();
  assert.equal(recovered.charges, batchOrders.length);
  assert.ok(recovered.attempts >= batchOrders.length * 2 && recovered.attempts <= batchOrders.length * 3, `attempts ${recovered.attempts}`);
  assert.equal((await f.orderStats()).orders, batchOrders.length);
  for (const { key, order } of batchOrders) {
    const saved = await (await fetch(`${f.urls().app}/orders/${key}`)).json();
    assert.deepEqual(saved, { id: key, status: "confirmed", order });
    assert.deepEqual(await f.charge(key), saved);
  }
  await f.restartPayment();
  await f.restartApp();
  for (const { key, order } of batchOrders) {
    assert.equal((await f.checkout(key, order)).status, 200);
    assert.equal((await f.checkout(key, { ...order, customer: "different-customer" })).status, 409);
  }
  assert.deepEqual(await f.paymentStats(), { charges: batchOrders.length, attempts: 0 });
});

test("staged batch assertions reject missing faults and verify pending/recovered payloads", async t => {
  const f = await fixture(t, true);
  assert.equal((await f.checkout("baseline")).status, 200);
  f.drop(true);
  await verifyBatch(f.urls().app, f.urls().payment, "outage");
  const before = await f.durability();
  assert.equal(before.pending_orders, batchOrders.length);
  await f.restartApp();
  assert.deepEqual(await f.durability(), before);
  f.drop(false);
  await verifyBatch(f.urls().app, f.urls().payment, "recovery");
  await assert.rejects(verifyBatch(f.urls().app, f.urls().payment, "outage"), assert.AssertionError);
});

test("conflicting keys fail while pending and canonical JSON field order remains idempotent", async t => {
  const f = await fixture(t, true);
  f.drop(true);
  const pending = f.checkout("conflicting", defaultOrder);
  pending.catch(() => {});
  for (let i = 0; i < 50 && (await f.paymentStats()).charges === 0; i++) await delay(10);
  assert.equal((await f.paymentStats()).charges, 1);
  const before = await f.paymentStats();
  assert.equal((await f.checkout("conflicting", { ...defaultOrder, sku: "different-sku" })).status, 409);
  assert.equal((await f.paymentStats()).attempts, before.attempts);
  assert.equal((await pending).status, 503);
  await f.restartApp();
  assert.equal((await f.checkout("conflicting", { ...defaultOrder, quantity: 2 })).status, 409);
  f.drop(false);
  assert.equal((await f.checkout("conflicting", { quantity: 1, sku: defaultOrder.sku, customer: defaultOrder.customer })).status, 200);
  assert.equal((await f.paymentStats()).charges, 1);
  const invalid = await f.checkout("invalid", { ...defaultOrder, quantity: 0 });
  assert.equal(invalid.status, 400);
  assert.equal((await f.paymentStats()).charges, 1);
});

test("provider keeps payload binding even if checkout loses its local ledger", async t => {
  const f = await fixture(t);
  assert.equal((await f.checkout("provider-binding", defaultOrder)).status, 200);
  await f.discardLocalOrders();
  const conflicting = await f.checkout("provider-binding", { ...defaultOrder, quantity: 2 });
  assert.deepEqual(conflicting, { status: 409, body: { error: "idempotency_conflict" } });
  assert.equal((await f.paymentStats()).charges, 1);
  assert.equal((await f.orderStats()).orders, 0);
});

test("malformed and oversized orders are rejected before any payment", async t => {
  const f = await fixture(t, true);
  for (const [body, status] of [["not-json", 400], ["null", 400], [JSON.stringify({ ...defaultOrder, price: 1 }), 400], [JSON.stringify({ ...defaultOrder, customer: "x".repeat(2000) }), 413]]) {
    const response = await fetch(`${f.urls().app}/checkout`, {
      method: "POST", headers: { "idempotency-key": "invalid", "content-type": "application/json" }, body,
      signal: AbortSignal.timeout(2000),
    });
    assert.equal(response.status, status);
  }
  assert.deepEqual(await f.paymentStats(), { charges: 0, attempts: 0 });
  assert.equal((await f.durability()).pending_orders, 0);
});
