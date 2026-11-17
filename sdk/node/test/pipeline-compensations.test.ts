import test from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import {
  f,
  source,
  business,
  contracts,
  setup,
} from "./compensation-fixture.js";
import {
  compensationEffectIdentity,
  definePipelineCompensation,
  parseCompensationPlan,
} from "../src/pipeline-compensations.js";
import { createCompensationMCP } from "../src/pipeline-compensation-mcp.js";
import { jobValueHash } from "../src/job-runtime.js";
const settings = { timeoutMs: 1000 };
test("shared contracts, immutable plan, input digests and MCP descriptors agree", async () => {
  const s = setup(),
    p = await s.recovery.plan(source.workflow_run_id, "alice", settings);
  assert.deepEqual(p, f.expected_plan);
  assert.deepEqual(
    {
      worker: contracts.worker.spec,
      root: contracts.root.spec,
      review: contracts.review.spec,
    },
    f.contracts,
  );
  assert.deepEqual(createCompensationMCP(s.recovery).tools, f.tools);
  assert.equal(Object.isFrozen(p.commands), true);
  assert.equal(JSON.stringify(p).includes("backend"), false);
  await assert.rejects(
    s.recovery.plan(source.workflow_run_id, "bob", settings),
    { code: "unauthorized" },
  );
});
test("concurrent planning and lost reservation acknowledgement retain one exact plan", async () => {
  const s = setup();
  const plans = await Promise.all(
    Array.from({ length: 16 }, () =>
      s.recovery.plan(source.workflow_run_id, "alice", settings),
    ),
  );
  assert.equal(new Set(plans.map((p) => p.identity)).size, 1);
  assert.equal(s.plans.size, 1);
  const lost = setup();
  lost.state.loseReserve = true;
  await assert.rejects(
    lost.recovery.plan(source.workflow_run_id, "alice", settings),
    { code: "store_failed" },
  );
  lost.state.loseReserve = false;
  assert.deepEqual(
    await lost.recovery.plan(source.workflow_run_id, "alice", settings),
    f.expected_plan,
  );
});
test("planning blocks active, unresolved and uncertain forward effects", async () => {
  for (const mode of ["sourceStatus", "jobStatus", "unresolved"] as const) {
    const s = setup();
    if (mode === "unresolved") s.state.unresolved = true;
    else s.state[mode] = "running";
    await assert.rejects(
      s.recovery.plan(source.workflow_run_id, "alice", settings),
      { code: mode === "unresolved" ? "source_unresolved" : "source_active" },
    );
    assert.equal(s.plans.size, 0);
  }
  const s = setup();
  s.state.jobStatus = "failed";
  await assert.rejects(
    s.recovery.plan(source.workflow_run_id, "alice", settings),
    { code: "source_effects_unknown" },
  );
});
test("recovery requires exact human approval and runs ordered commands once after journal loss", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  assert.equal(s.state.applies, 0);
  const command = ctx.plan.commands[0]!,
    task = {
      plan_id: ctx.plan.identity,
      command_id: command.id,
      recovery_run_id: ctx.runId,
    };
  await assert.rejects(
    s.worker(task, {
      inputId: compensationEffectIdentity(ctx.plan, command.effect_id),
      attempt: 1,
      signal: new AbortController().signal,
    }),
    { code: "approval_required" },
  );
  await s.approve(ctx);
  const dropped = new Set<number>();
  let output;
  for (let n = 0; n < 30; n++) {
    const r = await s.invoke(ctx);
    if (r.result.status === "succeeded") {
      output = (r.result.output as any).output;
      break;
    }
    assert.notEqual(r.result.status, "failed", JSON.stringify(r));
    const seq = r.ops[0]!.seq;
    if (!dropped.has(seq)) {
      dropped.add(seq);
      continue;
    }
    s.commit(ctx, r);
  }
  assert.equal(output.state, "completed");
  assert.equal(s.data.size, 0);
  assert.equal(s.effects.size, 2);
  assert.equal(s.state.applies, 2);
});
test("changed rows survive and needs_review rolls back callback writes and stops later commands", async () => {
  const s = setup(),
    ctx = await s.start();
  s.data.set("one", { version: 2 });
  await s.untilGate(ctx);
  await s.approve(ctx);
  const out = await s.finish(ctx);
  assert.equal(out.state, "needs_review");
  assert.deepEqual(s.data.get("one"), { version: 2 });
  assert.deepEqual(s.data.get("two"), { version: 1 });
  assert.equal(s.effects.size, 1);
});
test("lost commit acknowledgement reconciles through the atomic effect receipt", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  await s.approve(ctx);
  const c = ctx.plan.commands[0]!,
    task = {
      plan_id: ctx.plan.identity,
      command_id: c.id,
      recovery_run_id: ctx.runId,
    },
    context = {
      inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
      attempt: 1,
      signal: new AbortController().signal,
    };
  s.state.loseCommit = true;
  await assert.rejects(s.worker(task, context), { code: "commit_unknown" });
  s.state.loseCommit = false;
  const saved = await s.worker(task, { ...context, attempt: 2 });
  assert.equal(saved.state, "compensated");
  assert.equal(s.state.applies, 1);
  assert.equal(s.effects.size, 1);
});
test("compensation callbacks and output validation roll back business SQL", async () => {
  for (const mode of ["failApply", "invalidOutput"] as const) {
    const s = setup(),
      ctx = await s.start();
    await s.untilGate(ctx);
    await s.approve(ctx);
    s.state[mode] = true;
    const c = ctx.plan.commands[0]!;
    await assert.rejects(
      s.worker(
        {
          plan_id: ctx.plan.identity,
          command_id: c.id,
          recovery_run_id: ctx.runId,
        },
        {
          inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
          attempt: 1,
          signal: new AbortController().signal,
        },
      ),
      { code: "store_failed" },
    );
    assert.equal(s.data.size, 2);
    assert.equal(s.effects.size, 0);
  }
});
test("authorization remains current for inspection, duplicate attempts, dispatch and SQL effects", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  s.state.revoked = true;
  await assert.rejects(
    s.recovery.inspectPlan(ctx.plan.identity, "alice", settings),
    { code: "unauthorized" },
  );
  await assert.rejects(
    s.recovery.tool.start(
      { plan_id: ctx.plan.identity, binding_sha256: ctx.plan.binding_sha256 },
      randomUUID(),
      "alice",
      settings,
    ),
    { code: "unauthorized" },
  );
  s.state.revoked = false;
  await s.approve(ctx);
  s.state.executeAllowed = false;
  const c = ctx.plan.commands[0]!;
  await assert.rejects(
    s.worker(
      {
        plan_id: ctx.plan.identity,
        command_id: c.id,
        recovery_run_id: ctx.runId,
      },
      {
        inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
        attempt: 1,
        signal: new AbortController().signal,
      },
    ),
    { code: "unauthorized" },
  );
  assert.equal(s.effects.size, 0);
});
test("forged command, different run and out-of-order effect cannot use an approved plan", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  await s.approve(ctx);
  for (const index of [1]) {
    const c = ctx.plan.commands[index]!;
    await assert.rejects(
      s.worker(
        {
          plan_id: ctx.plan.identity,
          command_id: c.id,
          recovery_run_id: ctx.runId,
        },
        {
          inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
          attempt: 1,
          signal: new AbortController().signal,
        },
      ),
      { code: "conflict" },
    );
  }
  assert.equal(s.state.applies, 0);
  const c = ctx.plan.commands[0]!;
  await assert.rejects(
    s.worker(
      {
        plan_id: ctx.plan.identity,
        command_id: c.id,
        recovery_run_id: source.workflow_run_id,
      },
      {
        inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
        attempt: 1,
        signal: new AbortController().signal,
      },
    ),
  );
  assert.equal(s.effects.size, 0);
});
test("new recovery attempts reuse completed effects across runs", async () => {
  const s = setup(),
    first = await s.start();
  await s.untilGate(first);
  await s.approve(first);
  await s.finish(first);
  const second = await s.start();
  await s.untilGate(second);
  await s.approve(second);
  const out = await s.finish(second);
  assert.equal(out.state, "completed");
  assert.equal(s.state.applies, 2);
  assert.equal(s.effects.size, 2);
  assert.notEqual(first.runId, second.runId);
});
test("configuration and tampered command bindings fail closed", async () => {
  const s = setup(),
    p = await s.recovery.plan(source.workflow_run_id, "alice", settings);
  assert.throws(
    () =>
      parseCompensationPlan(
        {
          ...p,
          commands: [
            {
              ...p.commands[0]!,
              input: { row_id: "other", expected_version: 1 },
            },
          ],
        },
        business,
      ),
    { code: "conflict" },
  );
  await assert.rejects(
    definePipelineCompensation(business, {
      ...s.definition,
      requiredRoles: ["admin"],
    }).inspectPlan(p.identity, "alice", settings),
    { code: "conflict" },
  );
});
test("MCP planning returns references and summaries without business input or approval powers", async () => {
  const s = setup(),
    mcp = createCompensationMCP(s.recovery),
    r = await mcp.call(
      "recover_" + f.name + "_plan",
      { source_run_id: source.workflow_run_id },
      "alice",
      settings,
    );
  assert.equal(r.structuredContent!.state, "ready");
  assert.equal(
    JSON.stringify(r.structuredContent).includes("expected_version"),
    false,
  );
  await assert.rejects(
    mcp.call("recover_" + f.name + "_approve", {}, "alice", settings),
    { code: -32601 },
  );
  assert.equal(s.state.applies, 0);
});
test("deadlines fence late planners before a plan is stored", async () => {
  let finish!: (value: any) => void;
  const s = setup();
  const late = definePipelineCompensation(business, {
    ...s.definition,
    plan: async () => new Promise<any>((r) => (finish = r)),
  });
  await assert.rejects(
    late.plan(source.workflow_run_id, "alice", { timeoutMs: 20 }),
    { code: "deadline" },
  );
  finish(f.commands);
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(s.plans.size, 0);
});
test("cancellation during COMMIT reports uncertainty and a retry reads the effect receipt", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  await s.approve(ctx);
  const c = ctx.plan.commands[0]!,
    signal = new AbortController();
  s.state.hangCommit = true;
  s.state.onCommit = () => signal.abort();
  const task = {
    plan_id: ctx.plan.identity,
    command_id: c.id,
    recovery_run_id: ctx.runId,
  };
  await assert.rejects(
    s.worker(task, {
      inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
      attempt: 1,
      signal: signal.signal,
    }),
    { code: "commit_unknown" },
  );
  s.state.onCommit = undefined;
  s.state.hangCommit = false;
  const result = await s.worker(task, {
    inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
    attempt: 2,
    signal: new AbortController().signal,
  });
  assert.equal(result.state, "compensated");
  assert.equal(s.state.applies, 1);
});
test("a backend SQL authorization denial cannot be bypassed by an earlier execution check", async () => {
  const s = setup(),
    ctx = await s.start();
  await s.untilGate(ctx);
  await s.approve(ctx);
  const worker = s.recovery.handler({
    pool: s.pool,
    authorize: async () => false,
    apply: async () => {
      throw Error("must not run");
    },
  });
  const c = ctx.plan.commands[0]!;
  await assert.rejects(
    worker(
      {
        plan_id: ctx.plan.identity,
        command_id: c.id,
        recovery_run_id: ctx.runId,
      },
      {
        inputId: compensationEffectIdentity(ctx.plan, c.effect_id),
        attempt: 1,
        signal: new AbortController().signal,
      },
    ),
    { code: "unauthorized" },
  );
  assert.equal(s.data.size, 2);
  assert.equal(s.effects.size, 0);
});
