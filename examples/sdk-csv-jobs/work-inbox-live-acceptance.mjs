import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";
import { setTimeout as pause } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { createCsvWorkInboxServer } from "./work-inbox-live.mjs";

export async function liveWorkInboxAcceptance({
  sdk,
  pool,
  env,
  cleanup,
  client,
  scope,
  realtime,
  inbox,
  importId,
  python,
}) {
  const bobImport = randomUUID();
  await pool.query(
    "INSERT INTO sdk_csv_query_imports (import_id,owner_id,source_run_id,failure_report,report_sha256) SELECT $2,'bob',source_run_id,failure_report,report_sha256 FROM sdk_csv_query_imports WHERE import_id=$1",
    [importId, bobImport],
  );
  const routes = createCsvWorkInboxServer({
    sdk,
    inbox,
    client,
    scope,
    realtime: realtime.config(),
    authenticate(req) {
      return new Map([
        ["Bearer alice-review", "alice"],
        ["Bearer bob-review", "bob"],
      ]).get(req.headers.authorization);
    },
    async ownsImport(actor, id) {
      const { rowCount } = await pool.query(
        "SELECT 1 FROM sdk_csv_query_imports WHERE import_id=$1 AND owner_id=$2 AND enabled",
        [id, actor],
      );
      return rowCount === 1;
    },
  });
  await new Promise((resolve) => routes.server.listen(0, "127.0.0.1", resolve));
  cleanup.push(() => new Promise((resolve) => routes.server.close(resolve)));
  const base = "http://127.0.0.1:" + routes.server.address().port;
  const request = (id, actor, action, signal) =>
    fetch(base + "/imports/" + id + "/work-inbox/" + action, {
      headers: { authorization: "Bearer " + actor + "-review" },
      signal: signal ?? AbortSignal.timeout(10000),
    });
  for (const action of ["ticket", "snapshot"]) {
    assert.equal((await request(importId, "bob", action)).status, 404);
    assert.equal((await request(bobImport, "alice", action)).status, 404);
  }

  const packets = { node: [], python: [] };
  const watchers = new AbortController();
  const node = [
    ["alice", importId],
    ["bob", bobImport],
  ].map(async ([actor, id]) => {
    const read = async (action, signal) => {
      const response = await request(id, actor, action, signal);
      if (response.status === 404)
        throw new sdk.LiveWorkInboxError("unauthorized");
      assert.equal(response.status, 200);
      return response.json();
    };
    try {
      const first = await read("ticket");
      let initial = first;
      for await (const snapshot of sdk.watchWorkInbox({
        target: first.target,
        timeoutMs: 300000,
        refreshIntervalMs: 60000,
        signal: watchers.signal,
        loadSnapshot: ({ signal }) => read("snapshot", signal),
        source: sdk.realtimeLiveWorkInboxSource({
          url: first.url,
          channel: first.channel,
          retryInitialMs: 10,
          retryMaxMs: 100,
          webSocketFactory: sdk.createBrowserRealtimeTicketSocketFactory(
            async () => {
              const current =
                initial ?? (await read("ticket", watchers.signal));
              initial = undefined;
              assert.equal(current.url, first.url);
              assert.equal(current.channel, first.channel);
              assert.deepEqual(current.target, first.target);
              return current.ticket;
            },
          ),
        }),
      }))
        packets.node.push({ actor, snapshot });
    } catch (error) {
      packets.node.push({ actor, error: error.code ?? "unexpected" });
    }
  });
  cleanup.push(async () => {
    watchers.abort();
    await Promise.all(node);
  });

  const worker = spawn(
    python,
    [
      fileURLToPath(new URL("./work-inbox-live-client.py", import.meta.url)),
      base,
      importId,
      bobImport,
    ],
    { env, stdio: ["ignore", "pipe", "pipe"] },
  );
  const lines = createInterface({ input: worker.stdout });
  let workerError = "";
  worker.stderr.on("data", (data) => {
    workerError += data;
  });
  lines.on("line", (line) => packets.python.push(JSON.parse(line)));
  const exit = new Promise((resolve) => worker.once("exit", resolve));
  cleanup.push(async () => {
    worker.kill();
    await exit;
    lines.close();
  });

  const offsets = () => ({
    node: packets.node.length,
    python: packets.python.length,
  });
  async function waitFor(match, { after = {}, revoked } = {}) {
    const deadline = performance.now() + 25000;
    while (performance.now() < deadline) {
      if (
        Object.entries(packets).every(([runtime, list]) =>
          list.slice(after[runtime] ?? 0).some(match),
        )
      )
        return;
      const failed = Object.values(packets)
        .flat()
        .find(
          (packet) =>
            packet.error &&
            !match(packet) &&
            !(packet.actor === revoked && packet.error === "unauthorized"),
        );
      if (failed || worker.exitCode !== null)
        assert.fail(
          "Live inbox watcher stopped: " +
            JSON.stringify(failed) +
            " " +
            workerError,
        );
      await pause(20);
    }
    assert.fail(
      "Live inbox observations timed out: " + JSON.stringify(packets),
    );
  }
  const has =
    (state) =>
    ({ actor, snapshot }) =>
      actor === "alice" && snapshot?.items.some((item) => item.state === state);

  for (const actor of ["alice", "bob"])
    await waitFor(
      (packet) => packet.actor === actor && packet.snapshot?.items.length === 0,
    );

  return {
    async pendingReview() {
      await routes.notify("alice", importId, "cleanup-review");
      await routes.notify("alice", importId, "cleanup-review");
      await waitFor(has("awaiting_approval"));
    },
    async refundUnknown() {
      await routes.notify("alice", importId, "refund-unknown");
      await waitFor(has("unknown"));
    },
    async reconciled(actionId) {
      // Skip the notification and force a new ticket. Both watchers read current state.
      const { issued } = realtime.counts(),
        after = offsets();
      realtime.expire("inbox-alice");
      await waitFor(
        ({ actor, snapshot }) =>
          actor === "alice" &&
          snapshot?.items.some(
            (item) => item.action_id === actionId && item.state === "succeeded",
          ),
        { after },
      );
      assert.ok(realtime.counts().issued >= issued + 2);
      for (const list of Object.values(packets)) {
        assert.equal(list.filter(has("awaiting_approval")).length, 1);
        assert.equal(list.filter(has("unknown")).length, 1);
      }
    },
    async revoke() {
      await pool.query(
        "UPDATE sdk_csv_query_imports SET enabled=false WHERE import_id=$1",
        [importId],
      );
      try {
        // Even the still-valid ticket cannot authorize a snapshot after revocation.
        await routes.notify("alice", importId, "revoked");
        await waitFor(
          ({ actor, error }) => actor === "alice" && error === "unauthorized",
        );
        realtime.revoke("inbox-alice");
        const after = offsets();
        realtime.expire("inbox-bob");
        await waitFor(
          ({ actor, snapshot }) =>
            actor === "bob" && snapshot?.business_ref === bobImport,
          { after, revoked: "alice" },
        );
        for (const list of Object.values(packets)) {
          const bob = list.filter((packet) => packet.actor === "bob");
          assert.ok(bob.length >= 1);
          assert.ok(
            bob.every(
              (packet) =>
                packet.snapshot?.business_ref === bobImport &&
                packet.snapshot.items.length === 0,
            ),
          );
        }
        assert.equal((await request(importId, "alice", "ticket")).status, 404);
        assert.equal(
          (await request(importId, "alice", "snapshot")).status,
          404,
        );
        // Also fence an active watcher when the realtime grant alone is revoked.
        realtime.revoke("inbox-bob");
        await waitFor(
          ({ actor, error }) => actor === "bob" && error === "unauthorized",
          { revoked: "alice" },
        );
        assert.equal((await request(bobImport, "bob", "snapshot")).status, 200);
      } finally {
        await pool.query(
          "UPDATE sdk_csv_query_imports SET enabled=true WHERE import_id=$1",
          [importId],
        );
      }
    },
  };
}
