import assert from "node:assert/strict";
import { test } from "node:test";
import { createImporter, report } from "./importer.mjs";

for (const duplicate of [false, true]) {
  test(`retry preserves data and exposes duplicate notification: ${duplicate}`, async () => {
    let state = "pending", release;
    const held = new Promise((resolve) => { release = resolve; });
    const events = [], objects = [];
    const run = createImporter({
      state: async () => state,
      update: async (_id, value) => { state = value; events.push(value); },
      upload: async (body) => { objects.push(body); events.push("upload"); },
      checkpoint: async () => { events.push("checkpoint"); await held; },
      notify: async () => { events.push("notification"); }, duplicate,
    });
    const first = run("import-001");
    await new Promise(setImmediate);
    assert.deepEqual(events, ["committed", "upload", "checkpoint"]);
    assert.equal(state, "committed");
    release();
    assert.equal(await first, 503);
    assert.equal(await run("import-001"), 200);
    assert.equal(state, "complete");
    assert.deepEqual(objects, [report]);
    assert.equal(events.filter((e) => e === "notification").length, duplicate ? 2 : 1);
    assert.equal(events.filter((e) => e === "checkpoint").length, 1);
    await assert.rejects(run("import-001"), /additional dispatch/);
    await assert.rejects(run("unknown"), /unknown fixture/);
  });
}

test("lost checkpoint cannot deliver the notification", async () => {
  let delivered = false;
  const run = createImporter({ state: async () => "pending", update: async () => {}, upload: async () => {},
    checkpoint: async () => { throw new Error("relay lost"); }, notify: async () => { delivered = true; }, duplicate: false });
  await assert.rejects(run("import-001"), /relay lost/);
  assert.equal(delivered, false);
});

for (const duplicate of [false, true]) {
  test(`database recovery preserves the commit and exposes duplicate notification: ${duplicate}`, async () => {
    let current = "pending", unavailable = false, notices = 0;
    const objects = [];
    const run = createImporter({
      state: async () => { if (unavailable) throw new Error("database unavailable"); return current; },
      update: async (_id, value) => { current = value; },
      upload: async (body) => { objects.push(body); },
      checkpoint: async () => { unavailable = true; },
      notify: async () => { notices++; }, duplicate, expectDisconnect: true,
    });
    assert.equal(await run("import-001"), 503);
    assert.equal(current, "committed");
    unavailable = false;
    assert.equal(await run("import-001"), 200);
    assert.equal(current, "complete");
    assert.equal(notices, duplicate ? 2 : 1);
    assert.deepEqual(objects, [report]);
  });
}

test("a successful database query cannot stand in for a fault", async () => {
  let notified = false;
  const run = createImporter({ state: async () => "pending", update: async () => {}, upload: async () => {},
    checkpoint: async () => {}, notify: async () => { notified = true; }, duplicate: false, expectDisconnect: true });
  await assert.rejects(run("import-001"), /outage was not observed/);
  assert.equal(notified, false);
});
