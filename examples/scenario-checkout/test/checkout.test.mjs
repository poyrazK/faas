import assert from "node:assert/strict";
import net from "node:net";
import { once } from "node:events";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createCheckoutServer } from "../app/server.js";
import { verifyCheckout } from "./acceptance.mjs";

async function fixture(t) {
  let unavailable = false;
  const sockets = new Set();
  // A local downstream blackhole verifies the application and assertion
  // commands. Platform fault routing is exercised by the real-vm manifest.
  const payment = net.createServer(socket => {
    sockets.add(socket);
    socket.on("error", () => {});
    socket.once("close", () => sockets.delete(socket));
    socket.on("data", data => { if (!unavailable) socket.write(data); });
  });
  payment.listen(0, "127.0.0.1");
  await once(payment, "listening");
  const app = createCheckoutServer({ paymentHost: "127.0.0.1", paymentPort: payment.address().port });
  app.listen(0, "127.0.0.1");
  await once(app, "listening");
  t.after(async () => {
    app.closeAllConnections();
    for (const socket of sockets) socket.destroy();
    await Promise.all([new Promise(resolve => app.close(resolve)), new Promise(resolve => payment.close(resolve))]);
  });
  return { url: `http://127.0.0.1:${app.address().port}`, outage: value => { unavailable = value; } };
}

test("checkout commands prove outage isolation, bounded retries and idempotent recovery", async t => {
  const f = await fixture(t);
  const run = async phase => {
    // Use the same relative command and working directory as Gregale.
    const result = await promisify(execFile)(process.execPath, ["../test/acceptance.mjs", phase], {
      cwd: fileURLToPath(new URL("../app/", import.meta.url)),
      env: { ...process.env, GREGALE_TEST_URL: f.url }, timeout: 15000,
    });
    return result.stdout;
  };
  await run("baseline");
  f.outage(true);
  const outage = JSON.parse((await run("outage")).trim());
  assert.equal(outage.attempts, 2);
  assert.ok(outage.checkout_ms >= 600, "must actually wait through both payment deadlines");
  // Leave the dependency unavailable into recovery so its polling path must
  // tolerate another failed checkout before the acknowledgement returns.
  const restore = setTimeout(() => f.outage(false), 1400);
  try {
    const recovery = JSON.parse((await run("recovery")).trim());
    assert.equal(recovery.orders, 2);
    assert.ok(recovery.recovery_ms >= 700, "recovery polling was not exercised");
  } finally {
    clearTimeout(restore);
  }
});

test("outage acceptance rejects a dependency fault that never took effect", async t => {
  const f = await fixture(t);
  await verifyCheckout(f.url, "baseline");
  await assert.rejects(verifyCheckout(f.url, "outage"), assert.AssertionError);
});

test("invalid checkout keys never contact payment", async t => {
  const f = await fixture(t);
  const response = await fetch(`${f.url}/checkout`, { method: "POST" });
  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), { error: "invalid_idempotency_key" });
  assert.equal((await (await fetch(`${f.url}/stats`)).json()).payment_attempts, 0);
});
