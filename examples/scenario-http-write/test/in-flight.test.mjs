import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createWriteCaller } from "../app/server.js";
import { createWriteProvider } from "../payment/server.js";
import { order } from "./acceptance.mjs";
import { checkpointStates, verifyInFlight } from "./in-flight-acceptance.mjs";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${server.address().port}`;
}
async function close(server) {
  server.cancelHeldWrites?.();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
async function get(url, path) {
  const response = await fetch(url + path, { signal: AbortSignal.timeout(2000) });
  assert.equal(response.status, 200);
  return response.json();
}
function write(url, key = "in-flight", payload = order) {
  return fetch(url + "/checkout", {
    method: "POST", headers: { "content-type": "application/json", "idempotency-key": key },
    body: JSON.stringify(payload), signal: AbortSignal.timeout(5000),
  });
}
async function checkpoint(providerURL, stage = "pending") {
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline) {
    const state = await get(providerURL, "/checkpoints/in-flight");
    if (Object.entries(checkpointStates[stage]).every(([key, value]) => state[key] === value)) return;
    await delay(10);
  }
  assert.fail("write did not reach expected held checkpoint");
}
async function fixture(t, holdMS = 5000, holdStage = "pending") {
  const directory = await mkdtemp(join(tmpdir(), "gregale-in-flight-"));
  const ledgerPath = join(directory, "ledger.json");
  let provider = createWriteProvider({ ledgerPath, holdFirstKey: "in-flight", holdStage, holdMS });
  let providerURL = await listen(provider);
  t.after(async () => { await close(provider); await rm(directory, { recursive: true, force: true }); });
  return { ledgerPath, get providerURL() { return providerURL; }, cancelHeld() { provider.cancelHeldWrites(); }, async restart() {
    await close(provider);
    provider = createWriteProvider({ ledgerPath, holdFirstKey: "in-flight", holdStage, holdMS });
    providerURL = await listen(provider);
  } };
}
async function callerProcess(t, dependencyURL) {
  const moduleURL = new URL("../app/server.js", import.meta.url).href;
  const child = spawn(process.execPath, ["--input-type=module", "-e", `
    import { createWriteCaller } from ${JSON.stringify(moduleURL)};
    const server = createWriteCaller({ dependencyURL: process.env.PAYMENT_URL, deadlineMS: 10000 });
    server.listen(0, "127.0.0.1", () => console.log("http://127.0.0.1:" + server.address().port));
  `], { env: { ...process.env, PAYMENT_URL: dependencyURL }, stdio: ["ignore", "pipe", "pipe"] });
  const exited = once(child, "exit");
  t.after(async () => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL"); await exited; });
  let output = "";
  const url = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("caller startup timeout")), 2000);
    child.stdout.on("data", chunk => { output += chunk; if (output.includes("\n")) { clearTimeout(timeout); resolve(output.trim()); } });
    child.once("exit", () => { clearTimeout(timeout); reject(new Error("caller exited before startup")); });
    child.once("error", reject);
  });
  return { child, exited, url };
}

for (const stage of Object.keys(checkpointStates)) {
  test(`SIGKILL at ${stage} reconciles one write with a fresh caller`, async t => {
    const f = await fixture(t, 5000, stage);
    const original = await callerProcess(t, f.providerURL);
    assert.equal((await write(original.url, "baseline")).status, 200);
    // Install rejection handling before killing the process.
    const interrupted = write(original.url).then(response => response.status, () => "connection_ended");
    await checkpoint(f.providerURL, stage);
    assert.equal((await get(original.url, "/in-flight")).active, 1);
    const ledger = await get(f.providerURL, "/stats");
    assert.equal(ledger.charges, stage === "committed" ? 2 : 1);
    assert.equal(ledger.pending, stage === "pending" ? 1 : 0);
    const persisted = JSON.parse(await readFile(f.ledgerPath, "utf8"));
    assert.equal(persisted.entries.length, stage === "before-acceptance" ? 1 : 2);
    const intent = persisted.entries.find(([key]) => key === "in-flight");
    assert.equal(intent?.[1].state, stage === "before-acceptance" ? undefined : stage === "pending" ? "pending" : "confirmed");
    const accepted = await fetch(f.providerURL + "/charges/in-flight");
    assert.equal(accepted.status, stage === "before-acceptance" ? 404 : 200);
    await accepted.body?.cancel();
    original.child.kill("SIGKILL");
    assert.deepEqual(await original.exited, [null, "SIGKILL"]);
    assert.equal(await interrupted, "connection_ended");
    assert.deepEqual(await get(f.providerURL, "/checkpoints/in-flight"), checkpointStates[stage]);
    const replacement = await callerProcess(t, f.providerURL);
    assert.equal((await get(replacement.url, "/in-flight")).active, 0);
    await verifyInFlight(replacement.url, f.providerURL, stage);
    assert.equal((await get(f.providerURL, "/stats")).ledger_id, ledger.ledger_id);
  });

  test(`concurrent matching replay releases ${stage} once; conflicts cannot release it`, async t => {
    const f = await fixture(t, 5000, stage);
    const caller = createWriteCaller({ dependencyURL: f.providerURL });
    const url = await listen(caller);
    t.after(() => close(caller));
    const first = write(url).then(response => response.status, () => "connection_ended");
    await checkpoint(f.providerURL, stage);
    assert.equal((await write(url, "in-flight", { ...order, quantity: 2 })).status, 409);
    assert.deepEqual(await get(f.providerURL, "/checkpoints/in-flight"), checkpointStates[stage]);
    const replayed = await Promise.all(Array.from({ length: 4 }, () => write(url)));
    for (const response of replayed) {
      assert.equal(response.status, 200);
      assert.equal((await response.json()).attempts, 1);
    }
    assert.equal(await first, 200);
    const after = await get(f.providerURL, "/stats");
    assert.equal(after.charges, 1);
    assert.equal(after.pending, 0);
    assert.equal(after.held, 0);
    assert.equal(after.attempts, 6);
  });

  test(`${stage} teardown releases held handlers without an extra commit`, async t => {
    const f = await fixture(t, 5000, stage);
    const caller = createWriteCaller({ dependencyURL: f.providerURL });
    const url = await listen(caller);
    t.after(() => close(caller));
    const first = write(url).then(response => response.status, () => "connection_ended");
    await checkpoint(f.providerURL, stage);
    await f.cancelHeld();
    assert.equal(await first, 503);
    const after = await get(f.providerURL, "/stats");
    assert.equal(after.held, 0);
    assert.equal(after.charges, stage === "committed" ? 1 : 0);
    assert.equal(after.pending, stage === "pending" ? 1 : 0);
  });
}

test("caller deadline leaves pending intent; conflicts cannot release it", async t => {
  const f = await fixture(t);
  const caller = createWriteCaller({ dependencyURL: f.providerURL, deadlineMS: 100 });
  const url = await listen(caller);
  t.after(() => close(caller));
  const pending = write(url);
  await checkpoint(f.providerURL);
  const conflict = await write(url, "in-flight", { ...order, quantity: 2 });
  assert.equal(conflict.status, 409);
  assert.equal((await get(f.providerURL, "/stats")).charges, 0);
  assert.equal((await pending).status, 503);
  assert.equal((await get(url, "/in-flight")).active, 0);
  assert.equal((await write(url)).status, 200);
  assert.equal((await get(f.providerURL, "/stats")).charges, 1);
});

test("checkpoint timeout fails closed and provider restart reconciles persisted intent", async t => {
  const f = await fixture(t, 100);
  let caller = createWriteCaller({ dependencyURL: f.providerURL });
  let url = await listen(caller);
  t.after(() => close(caller));
  assert.equal((await write(url)).status, 503);
  assert.equal((await write(url)).status, 503);
  const before = await get(f.providerURL, "/stats");
  assert.equal(before.charges, 0);
  assert.equal(before.pending, 1);
  await f.restart();
  await close(caller);
  caller = createWriteCaller({ dependencyURL: f.providerURL });
  url = await listen(caller);
  assert.equal((await write(url)).status, 200);
  const after = await get(f.providerURL, "/stats");
  assert.equal(after.ledger_id, before.ledger_id);
  assert.equal(after.charges, 1);
  assert.equal(after.pending, 0);
});

for (const stage of ["before-acceptance", "committed"]) {
  test(`${stage} checkpoint deadline preserves its business boundary`, async t => {
    const f = await fixture(t, 100, stage);
    const caller = createWriteCaller({ dependencyURL: f.providerURL });
    const url = await listen(caller);
    t.after(() => close(caller));
    assert.equal((await write(url)).status, 503);
    const after = await get(f.providerURL, "/stats");
    assert.equal(after.held, 0);
    assert.equal(after.pending, 0);
    assert.equal(after.charges, stage === "committed" ? 1 : 0);
    assert.equal((await write(url)).status, stage === "committed" ? 200 : 503);
    assert.equal((await get(f.providerURL, "/stats")).charges, after.charges);
    assert.equal((await write(url, "in-flight", { ...order, quantity: 2 })).status, 409);
  });
}

test("acceptance rejects missing holds and the wrong write checkpoint", async t => {
  const f = await fixture(t);
  const caller = createWriteCaller({ dependencyURL: f.providerURL });
  const url = await listen(caller);
  t.after(() => close(caller));
  assert.equal((await write(url, "baseline")).status, 200);
  await assert.rejects(verifyInFlight(url, f.providerURL));
  const first = write(url).then(response => response.status, () => "connection_ended");
  await checkpoint(f.providerURL);
  await assert.rejects(verifyInFlight(url, f.providerURL, "committed"));
  await f.cancelHeld();
  assert.equal(await first, 503);
});

test("provider reopens a committed checkpoint without recreating its acknowledgement hold", async t => {
  const f = await fixture(t, 5000, "committed");
  const original = await callerProcess(t, f.providerURL);
  const first = write(original.url).then(response => response.status, () => "connection_ended");
  await checkpoint(f.providerURL, "committed");
  const before = await get(f.providerURL, "/stats");
  await f.restart();
  assert.equal(await first, 503);
  const replacement = await callerProcess(t, f.providerURL);
  assert.equal((await write(replacement.url)).status, 200);
  const after = await get(f.providerURL, "/stats");
  assert.equal(after.ledger_id, before.ledger_id);
  assert.equal(after.charges, 1);
  assert.equal(after.pending, 0);
  assert.equal(after.held, 0);
});
