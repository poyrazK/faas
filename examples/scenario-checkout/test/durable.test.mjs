import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { fixture, start } from "./support/fixture.mjs";
import { defaultOrder, fingerprint } from "../app/contract.js";

test("staged acceptance commands verify the stateful payment ledger", async t => {
  const f = await fixture(t, true);
  const run = phase => promisify(execFile)(process.execPath, ["../test/acceptance.mjs", phase], {
    cwd: fileURLToPath(new URL("../app/", import.meta.url)),
    env: { ...process.env, GREGALE_TEST_URL: f.urls().app, GREGALE_TEST_SERVICE_PAYMENT_URL: f.urls().payment },
    timeout: 15000,
  });
  await run("baseline");
  f.drop(true);
  await run("outage");
  const before = await f.durability();
  assert.equal(before.orders, 1);
  assert.equal(before.outage_state, "pending");
  // Both replacement processes start with new local ledger paths; only the
  // surviving dependency holds the order and charge state.
  await f.restartApp();
  assert.deepEqual(await f.durability(), before);
  f.drop(false);
  await run("recovery");
  assert.equal((await f.paymentStats()).charges, 2);
  assert.equal((await f.orderStats()).orders, 2);
  await f.restartApp();
  assert.equal((await f.orderStats()).orders, 2);
  const unauthorized = await fetch(`${f.urls().payment}/order-ledger`);
  assert.equal(unauthorized.status, 403);
});

test("lost payment ack survives checkout and provider crashes without duplicate charge or order", async t => {
  const f = await fixture(t);
  f.drop(true);
  const unavailable = await f.checkout();
  assert.equal(unavailable.status, 503);
  assert.deepEqual(unavailable.body, { error: "payment_unavailable", payment_outcome: "unknown", retryable: true });
  assert.deepEqual(await f.paymentStats(), { charges: 1, attempts: 2 });
  assert.equal((await f.orderStats()).orders, 0);
  assert.deepEqual(JSON.parse(await readFile(join(f.directory, "orders.json"), "utf8")).entries, [["lost-ack", { state: "pending", order: defaultOrder, fingerprint: fingerprint(defaultOrder) }]]);

  await f.restartPayment();
  assert.deepEqual(await f.paymentStats(), { charges: 1, attempts: 0 });
  await f.restartApp();
  f.drop(false);
  const recovered = await Promise.all([f.checkout(), f.checkout()]);
  assert.deepEqual(recovered, Array(2).fill({ status: 200, body: { id: "lost-ack", status: "confirmed" } }));
  assert.deepEqual(await f.paymentStats(), { charges: 1, attempts: 1 });
  assert.deepEqual(await f.orderStats(), { orders: 1, payment_attempts: 1, pending: 0 });

  await f.restartApp();
  assert.deepEqual(await f.checkout(), recovered[0]);
  assert.deepEqual(await f.paymentStats(), { charges: 1, attempts: 1 });
  assert.deepEqual(await f.orderStats(), { orders: 1, payment_attempts: 0, pending: 0 });
});

test("checkout killed after committed charge reconciles its pending intent", async t => {
  const f = await fixture(t);
  f.drop(true);
  const request = f.checkout().catch(() => undefined);
  for (let attempt = 0; attempt < 50 && (await f.paymentStats()).charges === 0; attempt++) await delay(10);
  assert.equal((await f.paymentStats()).charges, 1, "must charge before killing checkout");
  await f.crashApp();
  await request;
  await f.restartApp();
  f.drop(false);
  assert.equal((await f.checkout()).status, 200);
  assert.equal((await f.paymentStats()).charges, 1);
  assert.equal((await f.orderStats()).orders, 1);
});

test("corrupt persistent ledger fails startup instead of forgetting idempotency", async t => {
  const directory = await mkdtemp(join(tmpdir(), "gregale-bad-ledger-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const path = join(directory, "orders.json");
  await writeFile(path, '{"version":3,"id":"00000000-0000-0000-0000-000000000001","entries":[["key",{"state":"unknown"}]]}');
  await assert.rejects(start("../app/start.js", { ORDER_LEDGER_PATH: path }), /invalid ledger entry/);
});
