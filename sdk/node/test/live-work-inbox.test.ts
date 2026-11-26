import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { setImmediate, setTimeout } from "node:timers/promises";
import {
  LiveWorkInboxError,
  parseLiveWorkInboxSnapshot,
  watchWorkInbox,
  workInboxInvalidation,
  type LiveWorkInboxSink,
  type LiveWorkInboxOptions,
} from "../src/live-work-inbox.js";
import { readWorkInboxSnapshot } from "../src/live-work-inbox-snapshot.js";
import type { QueryTool } from "../src/query-tools.js";
import type { WorkInboxEntry, WorkInboxFilter } from "../src/work-inbox.js";

const cases = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/live-work-inbox-cases.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const hint = (id: string, target = cases.alice) =>
  workInboxInvalidation(target.resource_id, id);

function fixture(
  loadSnapshot: LiveWorkInboxOptions["loadSnapshot"],
  options: Partial<LiveWorkInboxOptions> = {},
) {
  let sink!: LiveWorkInboxSink;
  let stopped = false;
  const stream = watchWorkInbox({
    timeoutMs: 2000,
    target: cases.alice,
    refreshIntervalMs: 10000,
    source(callback) {
      sink = callback;
      return () => {
        stopped = true;
      };
    },
    loadSnapshot,
    ...options,
  });
  return { stream, sink: () => sink, stopped: () => stopped };
}

test("shared snapshots are bound to a target and reject duplicate or unsafe entries", () => {
  for (const key of [
    "initial",
    "pending_review",
    "refund_unknown",
    "refund_succeeded",
  ])
    assert.deepEqual(
      parseLiveWorkInboxSnapshot(cases[key], cases.alice),
      cases[key],
    );
  assert.throws(() => parseLiveWorkInboxSnapshot(cases.initial, cases.bob), {
    code: "invalid_snapshot",
  });
  const altered = structuredClone(cases.pending_review);
  altered.items[0].review.review_url += "&token=secret";
  assert.throws(() => parseLiveWorkInboxSnapshot(altered, cases.alice), {
    code: "invalid_snapshot",
  });
  const duplicate = {
    ...cases.refund_unknown,
    items: Array(2).fill(cases.refund_unknown.items[0]),
  };
  assert.throws(() => parseLiveWorkInboxSnapshot(duplicate, cases.alice), {
    code: "invalid_snapshot",
  });
});

test("lazy authorized reads coalesce repeated hints and isolate two viewers", async () => {
  let aliceValue = cases.initial,
    reads = 0,
    bobReads = 0,
    authorized = true;
  const alice = fixture(async () => {
    reads++;
    if (!authorized) throw new LiveWorkInboxError("unauthorized");
    return aliceValue;
  });
  const bob = fixture(
    async () => {
      bobReads++;
      return cases.bob_empty;
    },
    { target: cases.bob },
  );
  try {
    assert.equal(alice.sink(), undefined);
    const initial = alice.stream.next(),
      other = bob.stream.next();
    await setImmediate();
    assert.equal(reads, 0);
    alice.sink().ready();
    bob.sink().ready();
    assert.deepEqual((await initial).value, cases.initial);
    assert.deepEqual((await other).value, cases.bob_empty);
    const review = alice.stream.next();
    aliceValue = cases.pending_review;
    alice.sink().changed({ ...hint("malformed"), items: aliceValue.items });
    alice.sink().changed(hint("other", cases.bob));
    bob.sink().changed(hint("review"));
    await setImmediate();
    assert.equal(reads, 1);
    assert.equal(bobReads, 1);
    alice.sink().changed(hint("review"));
    assert.deepEqual((await review).value, cases.pending_review);
    const revoked = alice.stream.next();
    alice.sink().changed(hint("review"));
    await setImmediate();
    assert.equal(reads, 2);
    // A new change ID can cause a read but identical state is still suppressed.
    alice.sink().changed(hint("same-state"));
    await setImmediate();
    assert.equal(reads, 3);
    authorized = false;
    alice.sink().changed(hint("revoked"));
    await assert.rejects(revoked, { code: "unauthorized" });
    assert.equal(alice.stopped(), true);
    alice.sink().ready();
    assert.equal((await alice.stream.next()).done, true);
    assert.equal(bob.stopped(), false);
  } finally {
    await Promise.all([alice.stream.return!(), bob.stream.return!()]);
  }
});

test("reconnect fences a stale read and refreshes without an invalidation", async () => {
  let start!: (signal: AbortSignal) => void;
  let finish!: (value: unknown) => void;
  const started = new Promise<AbortSignal>((resolve) => {
    start = resolve;
  });
  const late = new Promise<unknown>((resolve) => {
    finish = resolve;
  });
  let reads = 0;
  const client = fixture(async ({ signal }) => {
    if (++reads === 1) return cases.initial;
    if (reads === 2) {
      start(signal);
      return late;
    }
    return cases.refund_succeeded;
  });
  try {
    const initial = client.stream.next();
    client.sink().ready();
    assert.deepEqual((await initial).value, cases.initial);
    const reconnect = client.stream.next();
    client.sink().changed(hint("first"));
    const stale = await started;
    client.sink().disconnected();
    assert.equal(stale.aborted, true);
    client.sink().ready();
    assert.deepEqual((await reconnect).value, cases.refund_succeeded);
    finish(cases.refund_unknown);
    const waiting = client.stream.next();
    await setImmediate();
    const close = client.stream.return!();
    assert.equal((await waiting).done, true);
    await close;
    assert.equal(client.stopped(), true);
  } finally {
    finish(undefined);
    await client.stream.return!();
  }
});

test("fallback, snapshot budget, deadline and cancellation bound read and subscription lifetime", async () => {
  let reads = 0;
  const client = fixture(
    async () => (++reads === 1 ? cases.initial : cases.refund_unknown),
    { maxSnapshots: 2, refreshIntervalMs: 10 },
  );
  const first = client.stream.next();
  client.sink().ready();
  assert.deepEqual((await first).value, cases.initial);
  assert.deepEqual((await client.stream.next()).value, cases.refund_unknown);
  await assert.rejects(client.stream.next(), { code: "snapshot_limit" });
  assert.equal(client.stopped(), true);

  const idle = fixture(async () => assert.fail("Read before ready"), {
    timeoutMs: 15,
  });
  await assert.rejects(idle.stream.next(), { code: "deadline" });
  assert.equal(idle.stopped(), true);

  const controller = new AbortController();
  const pending = fixture(
    async ({ signal }) => setTimeout(1000, cases.initial, { signal }),
    {
      signal: controller.signal,
    },
  );
  const snapshot = pending.stream.next();
  pending.sink().ready();
  await setImmediate();
  controller.abort();
  await assert.rejects(snapshot, { code: "cancelled" });
  assert.equal(pending.stopped(), true);

  const denied = fixture(async () => assert.fail("Read before ready"));
  const subscription = denied.stream.next();
  denied.sink().failed("unauthorized");
  await assert.rejects(subscription, { code: "unauthorized" });
});

test("snapshot reader follows sparse pages and marks a bounded result truncated", async () => {
  type Inbox = QueryTool<WorkInboxFilter, WorkInboxEntry, string>;
  const inbox: Pick<Inbox, "pages"> = {
    async *pages(input, access, options) {
      assert.deepEqual(input, { business_ref: cases.alice.business_ref });
      assert.equal(access, "alice");
      yield { items: [], next_cursor: "next" };
      if (options.maxPages !== 1)
        yield { items: cases.pending_review.items, next_cursor: null };
    },
  };
  assert.deepEqual(
    await readWorkInboxSnapshot(inbox, cases.alice, "alice", {
      timeoutMs: 1000,
    }),
    cases.pending_review,
  );
  assert.deepEqual(
    await readWorkInboxSnapshot(inbox, cases.alice, "alice", {
      timeoutMs: 1000,
      maxPages: 1,
    }),
    { ...cases.initial, truncated: true },
  );
});
