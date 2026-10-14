import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import { performance } from "node:perf_hooks";
import { pathToFileURL } from "node:url";

export async function verifyCheckout(baseURL, phase) {
  const request = async (path, options = {}) => {
    const started = performance.now();
    const response = await fetch(`${baseURL}${path}`, {
      signal: AbortSignal.timeout(2000), ...options,
    });
    const body = await response.json();
    return { status: response.status, body, elapsed_ms: performance.now() - started };
  };
  const checkout = (key, signal) => request("/checkout", {
    method: "POST", headers: { "idempotency-key": key }, ...(signal ? { signal } : {}),
  });
  const stats = async () => (await request("/stats")).body;
  const providerStats = async expectedCharges => {
    const url = process.env.GREGALE_TEST_SERVICE_PAYMENT_URL;
    if (!url) return; // The portable echo-only test has no provider HTTP URL.
    const response = await fetch(`${url}/stats`, { signal: AbortSignal.timeout(2000) });
    assert.equal(response.status, 200);
    const body = await response.json();
    assert.equal(body.charges, expectedCharges, "payment ledger contains duplicate or missing charges");
  };
  const confirmed = result => {
    assert.equal(result.status, 200);
    assert.equal(result.body.status, "confirmed");
  };

  if (phase === "baseline") {
    const results = await Promise.all([checkout("baseline"), checkout("baseline")]);
    results.forEach(confirmed);
    assert.deepEqual(results[0].body, results[1].body);
    assert.deepEqual(await stats(), { orders: 1, payment_attempts: 1, pending: 0 });
    await providerStats(1);
  } else if (phase === "outage") {
    const before = await stats();
    const failedCheckout = checkout("outage");
    // Attach a handler immediately, even if a later catalog assertion fails.
    const observation = Promise.all([failedCheckout]);
    observation.catch(() => {});
    let pending = false;
    for (let i = 0; i < 20; i++) {
      if ((await stats()).pending > 0) { pending = true; break; }
      await delay(10);
    }
    assert.ok(pending, "checkout must be in flight while checking catalog isolation");
    const catalog = await request("/catalog");
    assert.equal(catalog.status, 200);
    assert.equal(catalog.body.available, true);
    assert.ok(catalog.elapsed_ms < 500, `catalog stalled: ${catalog.elapsed_ms}ms`);
    const [result] = await observation;
    assert.equal(result.status, 503);
    assert.deepEqual(result.body, { error: "payment_unavailable", payment_outcome: "unknown", retryable: true });
    assert.ok(result.elapsed_ms < 2000, `checkout exceeded two seconds: ${result.elapsed_ms}ms`);
    const after = await stats();
    assert.equal(after.orders, before.orders, "failed checkout created an order");
    assert.equal(after.payment_attempts - before.payment_attempts, 2, "retry budget exceeded or not exercised");
    assert.equal(after.pending, 0);
    await providerStats(2); // Payment committed, but its acknowledgement was lost.
    console.log(JSON.stringify({ phase, checkout_ms: result.elapsed_ms, catalog_ms: catalog.elapsed_ms, attempts: 2 }));
  } else if (phase === "recovery") {
    const started = performance.now();
    const signal = AbortSignal.timeout(10000);
    let result;
    while (!signal.aborted) {
      result = await checkout("outage", signal);
      if (result.status === 200) break;
      assert.equal(result.status, 503);
      await delay(100, undefined, { signal });
    }
    confirmed(result);
    assert.ok(performance.now() - started < 10000, "checkout did not recover within ten seconds");
    const before = await stats();
    const duplicates = await Promise.all([checkout("outage"), checkout("outage")]);
    duplicates.forEach(confirmed);
    duplicates.forEach(value => assert.deepEqual(value.body, result.body));
    const after = await stats();
    assert.equal(after.orders, 2, "expected exactly one baseline and one recovered order");
    assert.equal(after.payment_attempts, before.payment_attempts, "duplicate checkout contacted payment again");
    assert.equal(after.pending, 0);
    await providerStats(2);
    console.log(JSON.stringify({ phase, recovery_ms: performance.now() - started, orders: after.orders }));
  } else {
    throw new Error(`unknown checkout phase: ${phase}`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await verifyCheckout(process.env.GREGALE_TEST_URL, process.argv[2]);
}
