import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  createApplicationToolkit,
  type ToolkitAdapter,
} from "../src/application-toolkit.js";
import {
  createPipelineTasks,
  type PipelineTaskBinding,
  type PipelineTaskStore,
} from "../src/pipeline-tasks.js";

const c = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/pipeline-task-cases.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const access = { principal: "alice", scopes: ["start", "status", "result"] };
const alice = {
  resource: "https://app.example.test/mcp",
  subject: "alice",
  clientId: "agent",
  access,
};
const settings = { timeoutMs: 1000 };
const args = { request_id: c.request_id, input: { rows: 2 } };
const meta = "io.gregale/pipeline";

function fixture() {
  const bindings = new Map<string, PipelineTaskBinding>();
  const state = {
    pipeline: "start_unknown",
    revoked: false,
    starts: 0,
    calls: 0,
    expired: false,
    identity: c.receipt_identity,
  };
  const store: PipelineTaskStore = {
    async load(ns, owner, query) {
      const row = bindings.get(ns + ":" + owner);
      return !state.expired &&
        row &&
        ("task_id" in query
          ? query.task_id === row.task_id
          : query.tool_name === row.tool_name &&
            query.request_id === row.request_id)
        ? row
        : null;
    },
    async bind(ns, owner, row) {
      const key = ns + ":" + owner,
        saved = bindings.get(key);
      if (saved && saved.receipt_identity !== row.receipt_identity) return null;
      if (saved && !state.expired) return saved;
      const binding = { ...row, created_at: c.created_at };
      bindings.set(key, binding);
      state.expired = false;
      return binding;
    },
  };
  const adapter: ToolkitAdapter<string> = {
    tools: ["start", "status", "result"].map((op) => ({
      name: c.tool + "_" + op,
      description: op,
      inputSchema: {},
      outputSchema: {},
      annotations: {
        readOnlyHint: op !== "start",
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: false,
      },
    })),
    async call(name, _args, principal) {
      state.calls++;
      if (state.revoked || principal !== "alice")
        return { isError: true, content: [{ type: "text", text: "denied" }] };
      if (name.endsWith("_start")) state.starts = 1; // The pipeline adapter owns deduplication.
      const result = {
        state:
          name.endsWith("_start") && state.pipeline !== "start_unknown"
            ? "started"
            : state.pipeline,
        receipt: {
          tool: c.tool,
          request_id: c.request_id,
          identity: state.identity,
          run_id: state.pipeline === "start_unknown" ? null : c.run_id,
        },
        steps: [c.step],
        approval:
          state.pipeline === "awaiting_approval"
            ? { ...c.review, name: "review" }
            : null,
        ...(name.endsWith("_result") ? { output: c.output } : {}),
      };
      return {
        isError: false,
        content: [{ type: "text", text: JSON.stringify(result) }],
        structuredContent: result,
      };
    },
  };
  const toolkit = createApplicationToolkit([
    {
      adapter,
      toolScopes: Object.fromEntries(
        ["start", "status", "result"].map((op) => [c.tool + "_" + op, [op]]),
      ),
    },
  ]);
  const open = () =>
    createPipelineTasks(toolkit, {
      store,
      namespace: "test",
      ownerKey: "task-key-for-tests-at-least-32-bytes",
      tools: [c.tool],
    });
  return { open, state };
}

test("resumes one Task across clients and reports current progress, human review, and typed result", async () => {
  const { open, state } = fixture(),
    start = c.tool + "_start";
  const [first, replay] = await Promise.all([
    open().create(start, args, alice, settings),
    open().create(start, args, alice, settings),
  ]);
  assert.equal(first.resultType, "task");
  assert.equal(replay.resultType, "task");
  assert.equal(first.taskId, replay.taskId);
  assert.equal(state.starts, 1);
  const fresh = open();
  const uncertain = await fresh.get(first.taskId, alice, settings);
  assert.equal(uncertain.status, "working");
  assert.equal(uncertain._meta[meta].state, "start_unknown");
  state.pipeline = "awaiting_approval";
  const pending = await fresh.get(first.taskId, alice, settings);
  assert.deepEqual(pending._meta[meta].review, c.review);
  assert.deepEqual(pending._meta[meta].steps, [c.step]);
  state.pipeline = "succeeded";
  const completed = await fresh.get(first.taskId, alice, settings);
  assert.equal(completed.status, "completed");
  assert.deepEqual(completed.result.structuredContent?.output, c.output);
  await assert.rejects(
    fresh.get(
      first.taskId,
      { ...alice, access: { ...access, scopes: ["start", "status"] } },
      settings,
    ),
    { code: "not_found" },
  );
  state.revoked = true;
  await assert.rejects(fresh.get(first.taskId, alice, settings), {
    code: "not_found",
  });
});

test("rejects foreign owners, OAuth clients, changed receipt bindings and expired handles", async () => {
  const { open, state } = fixture();
  const runtime = open();
  const first = await runtime.create(c.tool + "_start", args, alice, settings);
  assert.equal(first.resultType, "task");
  const calls = state.calls;
  for (const caller of [
    { ...alice, subject: "bob" },
    { ...alice, clientId: "different-agent" },
  ])
    await assert.rejects(runtime.get(first.taskId, caller, settings), {
      code: "not_found",
    });
  assert.equal(state.calls, calls);
  state.identity = "f".repeat(64);
  await assert.rejects(runtime.get(first.taskId, alice, settings), {
    code: "not_found",
  });
  await assert.rejects(
    runtime.create(c.tool + "_start", args, alice, settings),
    { code: "not_found" },
  );
  state.expired = true;
  assert.equal(runtime.canCreate(c.tool + "_result"), false);
  await assert.rejects(runtime.get(first.taskId, alice, settings), {
    code: "not_found",
  });
});

test("repairs a replacement link after a failed bind and isolates OAuth clients", async () => {
  const bindings = new Map<string, PipelineTaskBinding>();
  const receipts = new Set([c.request_id]);
  const token = "2000000000." + "a".repeat(64);
  const hash = "c".repeat(64);
  let starts = 0, preparations = 0, loseLink = true, eligible = true;
  const store: PipelineTaskStore = {
    async load(ns, owner, lookup) {
      return [...bindings.entries()].find(([key, row]) => key.startsWith(ns + ":" + owner + ":") &&
        ("task_id" in lookup ? row.task_id === lookup.task_id :
          row.tool_name === lookup.tool_name && row.request_id === lookup.request_id))?.[1] ?? null;
    },
    async bind(ns, owner, row) {
      const key = ns + ":" + owner + ":" + row.request_id;
      const saved = bindings.get(key) ?? { ...row, created_at: c.created_at };
      bindings.set(key, saved);
      return saved;
    },
    loadLinked(ns, owner, lookup, signal) { return store.load(ns, owner, lookup, signal); },
    async bindLinked(ns, owner, row, signal) {
      if (loseLink) { loseLink = false; throw Error("link write interrupted"); }
      return store.bind(ns, owner, row, signal);
    },
  };
  const toolkit = {
    toolScopes: Object.fromEntries(["start", "status", "result", "preview"].map((op) => [c.tool + "_" + op, [op]])),
    listTools() { return Object.keys(this.toolScopes).map((name) => ({ name })); },
    listPipelineContracts() { return [{ tool: c.tool, contract_sha256: hash, preview: { type: "integer" } }]; },
    async call(name: string, args: { request_id: string }) {
      const id = args.request_id;
      let content: Record<string, unknown>;
      if (name.endsWith("_preview")) content = { summary: 2, preview_token: token, expires_at: 2000000000 };
      else {
        if (name.endsWith("_start") && !receipts.has(id)) { starts++; receipts.add(id); }
        content = {
          state: name.endsWith("_start") ? "started" : !receipts.has(id) ? "not_found" : id === c.request_id ? "failed" : "running",
          receipt: { tool: c.tool, request_id: id, identity: c.receipt_identity, run_id: c.run_id },
          steps: [], approval: null,
          ...(id === c.request_id && { source_failure: "source_unavailable" }),
        };
      }
      return { isError: false, structuredContent: content, content: [{ type: "text", text: JSON.stringify(content) }] };
    },
  };
  const open = () => createPipelineTasks(toolkit as unknown as Parameters<typeof createPipelineTasks<string>>[0], {
    store, namespace: "linked", ownerKey: "task-key-for-tests-at-least-32-bytes", tools: [c.tool],
    recovery: {
      scopes: ["replace"],
      async prepare(source) {
        preparations++;
        assert.equal(source.receipt_identity, c.receipt_identity);
        return eligible ? { input: { rows: 2 }, previous_source_sha256: "d".repeat(64) } : null;
      },
    },
  });
  const caller = { ...alice, access: { ...access, scopes: [...access.scopes, "preview", "replace"] } };
  const runtime = open();
  const original = await runtime.create(c.tool + "_start", args, caller, settings);
  assert.ok("taskId" in original);
  const previewArgs = {
    task_id: original.taskId, recovery_request_id: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", expected_contract_sha256: hash,
  };
  const confirmName = runtime.recoveryTools!.confirm.name;
  const confirmed = { ...previewArgs, preview_token: token };
  const foreign = await runtime.previewRecovery!(previewArgs, { ...caller, clientId: "other" }, settings);
  const rejection = foreign.content[0];
  assert.ok(rejection?.type === "text");
  assert.deepEqual(JSON.parse(rejection.text), { code: "not_found" });
  assert.equal((await runtime.previewRecovery!(previewArgs, caller, settings)).structuredContent?.preview_token, token);
  assert.equal(starts, 0);
  await assert.rejects(runtime.create(confirmName, confirmed, caller, settings));
  eligible = false;
  const recovered = await open().create(confirmName, confirmed, caller, settings);
  const replayed = await runtime.create(confirmName, confirmed, caller, settings);
  assert.ok("taskId" in recovered && "taskId" in replayed);
  assert.equal(recovered.taskId, replayed.taskId);
  assert.equal((await runtime.get(recovered.taskId, caller, settings))._meta[meta].replaces_task_id, original.taskId);
  assert.equal(starts, 1);
  assert.equal(preparations, 2);
});
