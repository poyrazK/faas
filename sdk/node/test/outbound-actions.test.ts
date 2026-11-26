import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { actionFixture, input, requestId } from "./outbound-action-fixture.js";
import { createOutboundActionMCP } from "../src/outbound-action-mcp.js";
import { stripeRefundContract } from "../src/stripe-refund-actions.js";
import { stripeRefundActionProvider } from "../src/stripe-refund-actions.js";
const settings = { timeoutMs: 1000 };
test("provider responses allow full objects while remaining bounded", async () => {
  for (const padding of [70000, 1048576]) {
    const f = await approved();
    f.state.accountPadding = padding;
    if (padding < 1048576) {
      assert.equal(
        (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
          .state,
        "succeeded",
      );
      assert.equal(f.state.posts, 1);
    } else {
      await assert.rejects(
        f.action.execute(f.p.action_id, f.ctx.runId, settings),
        {
          code: "limit",
        },
      );
      assert.equal(f.state.posts, 0);
      assert.equal(
        (await f.action.inspect(f.p.action_id, "alice", settings)).outcome
          .state,
        "prepared",
      );
    }
  }
});
test("a review stop retains the last known provider reference", async () => {
  const f = await approved();
  f.state.pending = true;
  const sent = await f.action.execute(f.p.action_id, f.ctx.runId, settings);
  f.state.refunds[0].amount = 999;
  const stopped = await f.action.reconcile(f.p.action_id, "alice", settings);
  assert.equal(stopped.outcome.state, "needs_review");
  assert.equal(stopped.outcome.reference, sent.outcome.reference);
  assert.equal(f.state.posts, 1);
});
async function approved() {
  const f = actionFixture(),
    p = await f.action.prepare(input, requestId, "alice", settings),
    ctx = await f.start(p);
  await f.untilGate(ctx);
  await f.approve(ctx);
  return { ...f, p, ctx };
}
test("shared action intent, contracts and MCP descriptors agree", async () => {
  const f = actionFixture(),
    p = await f.action.prepare(input, requestId, "alice", settings);
  const shared = JSON.parse(
    readFileSync(
      new URL(
        "../../../request-contracts/outbound-actions.json",
        import.meta.url,
      ),
      "utf8",
    ),
  );
  assert.deepEqual(f.records.get(p.action_id).intent, shared.intent);
  assert.deepEqual(f.action.contract.spec, shared.root_contract);
  assert.deepEqual(f.action.reviewContract.spec, shared.review_contract);
  assert.deepEqual(stripeRefundContract.spec, shared.business_contract);
  assert.deepEqual(createOutboundActionMCP(f.action).tools, shared.tools);
});
test("concurrent prepares, immutable inputs and configuration conflicts", async () => {
  const f = actionFixture();
  const all = await Promise.all(
    Array.from({ length: 10 }, () =>
      f.action.prepare(input, requestId, "alice", settings),
    ),
  );
  assert.ok(all.every((p) => p.action_id === all[0]!.action_id));
  assert.equal(f.records.size, 1);
  await assert.rejects(
    f.action.prepare(
      { ...input, currency: "eur" },
      requestId,
      "alice",
      settings,
    ),
    { code: "conflict" },
  );
  await assert.rejects(
    f.make("v2").inspect(all[0]!.action_id, "alice", settings),
    { code: "conflict" },
  );
  assert.equal(f.state.posts, 0);
});
test("unapproved, rejected and expired gates never dispatch a refund", async () => {
  for (const choice of ["rejected", "expired"]) {
    const f = actionFixture(),
      p = await f.action.prepare(input, requestId, "alice", settings),
      ctx = await f.start(p);
    await f.untilGate(ctx);
    await assert.rejects(f.action.execute(p.action_id, ctx.runId, settings), {
      code: "approval_required",
    });
    await f.approve(ctx, choice);
    const r = await f.finish(ctx);
    assert.equal(r.decision, choice);
    assert.equal(f.state.posts, 0);
  }
});
test("concurrent execution shares one acknowledged dispatch claim", async () => {
  const f = await approved();
  const outputs = await Promise.all(
    Array.from({ length: 8 }, () =>
      f.action.execute(f.p.action_id, f.ctx.runId, settings),
    ),
  );
  assert.equal(f.state.posts, 1);
  assert.ok(outputs.some((r) => r.outcome.state === "succeeded"));
  assert.equal(
    (await f.action.inspect(f.p.action_id, "alice", settings)).outcome.state,
    "succeeded",
  );
});
test("a lost provider response is recovered by GET without another POST", async () => {
  const f = await approved();
  f.state.loseResponse = true;
  const first = await f.action.execute(f.p.action_id, f.ctx.runId, settings);
  assert.equal(first.outcome.state, "unknown");
  assert.equal(f.state.posts, 1);
  assert.equal(
    (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
      .state,
    "unknown",
  );
  const fixed = await f.action.reconcile(f.p.action_id, "alice", settings);
  assert.equal(fixed.outcome.state, "succeeded");
  assert.equal(fixed.outcome.result?.amount_minor, 1000);
  assert.equal(f.state.posts, 1);
});
test("pending refunds use their retained provider reference", async () => {
  const f = await approved();
  f.state.pending = true;
  assert.equal(
    (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
      .state,
    "pending",
  );
  f.state.refunds[0].status = "succeeded";
  assert.equal(
    (await f.action.reconcile(f.p.action_id, "alice", settings)).outcome.state,
    "succeeded",
  );
  assert.equal(f.state.posts, 1);
});
test("missing and duplicate provider evidence cannot permit a retry", async () => {
  for (const duplicate of [false, true]) {
    const f = await approved();
    f.state.loseResponse = true;
    await f.action.execute(f.p.action_id, f.ctx.runId, settings);
    f.state.missing = !duplicate;
    f.state.duplicate = duplicate;
    const result = await f.action.reconcile(f.p.action_id, "alice", settings);
    assert.equal(result.outcome.state, duplicate ? "needs_review" : "unknown");
    await f.action.execute(f.p.action_id, f.ctx.runId, settings);
    assert.equal(f.state.posts, 1);
  }
});
test("current access and backend execution authorization cover duplicates", async () => {
  const f = await approved();
  await assert.rejects(f.action.inspect(f.p.action_id, "bob", settings), {
    code: "unauthorized",
  });
  f.state.revoked = true;
  await assert.rejects(f.action.prepare(input, requestId, "alice", settings), {
    code: "unauthorized",
  });
  await assert.rejects(f.action.execute(f.p.action_id, f.ctx.runId, settings), {
    code: "unauthorized",
  });
  await assert.rejects(f.action.reconcile(f.p.action_id, "alice", settings), {
    code: "unauthorized",
  });
  assert.equal(f.state.posts, 0);
});
test("authorization is rechecked after provider account verification", async () => {
  const f = await approved();
  f.state.onVerify = () => {
    f.state.executionAllowed = false;
  };
  await assert.rejects(f.action.execute(f.p.action_id, f.ctx.runId, settings), {
    code: "unauthorized",
  });
  assert.equal(f.state.posts, 0);
});
test("wrong Stripe account and mismatched approval bindings prevent writes", async () => {
  const f = await approved();
  f.state.badAccount = true;
  await assert.rejects(f.action.execute(f.p.action_id, f.ctx.runId, settings), {
    code: "conflict",
  });
  f.state.badAccount = false;
  f.nativeRuns.get(f.ctx.runId).input.input.action_id = "a".repeat(64);
  await assert.rejects(f.action.execute(f.p.action_id, f.ctx.runId, settings));
  assert.equal(f.state.posts, 0);
});
test("lost claim acknowledgement never dispatches; lost outcome acknowledgement reads saved result", async () => {
  for (const claim of [false, true]) {
    const f = await approved();
    f.state.loseClaim = claim;
    f.state.loseObserve = !claim;
    if (claim)
      await assert.rejects(
        f.action.execute(f.p.action_id, f.ctx.runId, settings),
        { code: "store_failed" },
      );
    else
      assert.equal(
        (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
          .state,
        "unknown",
      );
    f.state.loseClaim = false;
    assert.equal(
      (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
        .state,
      claim ? "unknown" : "succeeded",
    );
    assert.equal(f.state.posts, claim ? 0 : 1);
  }
});
test("discard and native cancellation fence dispatch", async () => {
  const f = await approved();
  await f.action.cancel(f.p.action_id, "alice", settings);
  assert.equal(
    (await f.action.execute(f.p.action_id, f.ctx.runId, settings)).outcome
      .state,
    "cancelled",
  );
  const g = await approved();
  g.nativeRuns.get(g.ctx.runId).status = "failed";
  g.nativeRuns.get(g.ctx.runId).cancelled_at = "2026-11-17T10:00:00Z";
  await assert.rejects(g.action.execute(g.p.action_id, g.ctx.runId, settings), {
    code: "execution_inactive",
  });
  assert.equal(f.state.posts + g.state.posts, 0);
});
test("deadlines and cancellation after provider dispatch retain uncertainty", async () => {
  for (const cancel of [false, true]) {
    const f = await approved(),
      controller = new AbortController();
    f.state.hangPost = true;
    f.state.onPost = () => {
      if (cancel) controller.abort();
    };
    const result = await f.action.execute(f.p.action_id, f.ctx.runId, {
      timeoutMs: 2000,
      signal: controller.signal,
    });
    assert.equal(result.outcome.state, "unknown");
    assert.equal(f.state.posts, 1);
    assert.equal(
      (await f.action.reconcile(f.p.action_id, "alice", settings)).outcome
        .state,
      "succeeded",
    );
  }
});
test("late claims after a deadline cannot reach the provider", async () => {
  const f = await approved();
  let release!: () => void;
  f.state.beforeClaim = () =>
    new Promise((r) => {
      release = r;
    });
  await assert.rejects(
    f.action.execute(f.p.action_id, f.ctx.runId, { timeoutMs: 30 }),
    { code: "deadline" },
  );
  release();
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(f.state.posts, 0);
});
test("MCP preparation exposes a reference, never an execution or approval bypass", async () => {
  const f = actionFixture(),
    mcp = createOutboundActionMCP(f.action);
  assert.equal(mcp.tools.length, 9);
  const r = await mcp.call(
    "action_csv_refund_prepare",
    { request_id: requestId, input },
    "alice",
    settings,
  );
  assert.equal(r.structuredContent?.outcome !== undefined, true);
  assert.equal(f.state.posts, 0);
  await assert.rejects(
    mcp.call(
      "action_csv_refund_prepare",
      { request_id: requestId, input, owner_id: "alice" },
      "alice",
      settings,
    ),
    { code: -32602 },
  );
  assert.ok(!mcp.tools.some((t) => /approve|execute/.test(t.name)));
});
test("workflow completion, fresh approval and lifecycle reads reuse retained results", async () => {
  const f = await approved();
  assert.equal((await f.finish(f.ctx)).action.outcome.state, "succeeded");
  assert.equal(f.state.posts, 1);
  const again = await f.start(f.p);
  await f.untilGate(again);
  await f.approve(again);
  assert.equal((await f.finish(again)).action.outcome.state, "succeeded");
  assert.equal(f.state.posts, 1);
});
const protocol = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/outbound-actions.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
for (const c of protocol.provider_cases)
  test("shared provider case " + c.status + "/" + c.refund_status, () => {
    const provider = stripeRefundActionProvider({
        accountId: "acct_fixture",
        apiVersion: "2026-10-28.endive",
      }),
      intent = protocol.intent;
    const refund = {
      id: "re_fixture",
      object: "refund",
      payment_intent: input.payment_intent,
      amount: 1000,
      currency: "usd",
      status: c.refund_status,
      metadata: {
        gregale_action_id: intent.identity,
        gregale_input_sha256: intent.input_sha256,
      },
    };
    assert.equal(
      provider.decode({ status: c.status, body: refund }, intent).state,
      c.state,
    );
  });

test("provider pagination is bounded and repeated cursors require review", async () => {
  const intent = protocol.intent,
    record = {
      intent,
      version: 1,
      outcome: { state: "unknown" as const, code: "dispatch_unconfirmed" },
    };
  for (const repeat of [false, true]) {
    let reads = 0;
    const provider = stripeRefundActionProvider({
      accountId: "acct_fixture",
      apiVersion: "2026-10-28.endive",
      maxPages: 2,
    });
    const result = await provider.reconcile(
      {
        get: async () => ({
          status: 200,
          body: {
            object: "list",
            has_more: true,
            data: [{ id: "re_" + (repeat ? "same" : ++reads), metadata: {} }],
          },
        }),
      },
      record,
    );
    assert.equal(result.state, repeat ? "needs_review" : "unknown");
    assert.equal(
      result.code,
      repeat ? "provider_response_invalid" : "reconciliation_limit",
    );
    if (!repeat) assert.equal(reads, 2);
  }
});

test("provider classes preserve prototype methods and private instance state", async () => {
  const base = stripeRefundActionProvider({
    accountId: "acct_fixture",
    apiVersion: "2026-10-28.endive",
  });
  class Provider {
    #inner = base;
    readonly name = base.name;
    readonly configuration = base.configuration;
    request(...args: Parameters<typeof base.request>) {
      return this.#inner.request(...args);
    }
    verify(...args: Parameters<typeof base.verify>) {
      return this.#inner.verify(...args);
    }
    decode(...args: Parameters<typeof base.decode>) {
      return this.#inner.decode(...args);
    }
    reconcile(...args: Parameters<typeof base.reconcile>) {
      return this.#inner.reconcile(...args);
    }
  }
  const f = actionFixture(new Provider()),
    p = await f.action.prepare(input, requestId, "alice", settings),
    ctx = await f.start(p);
  await f.untilGate(ctx);
  await f.approve(ctx);
  f.state.pending = true;
  assert.equal(
    (await f.action.execute(p.action_id, ctx.runId, settings)).outcome.state,
    "pending",
  );
  f.state.refunds[0].status = "succeeded";
  assert.equal(
    (await f.action.reconcile(p.action_id, "alice", settings)).outcome.state,
    "succeeded",
  );
  assert.equal(f.state.posts, 1);
});
