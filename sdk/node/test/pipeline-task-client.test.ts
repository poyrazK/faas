import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import {
  createPipelineTaskClient,
  createPipelineTaskApprovalClient,
  createPipelineTaskHandoffClient,
  createPipelineTaskHttpTransport,
  createPipelineTaskReviewerInbox,
  workInboxInvalidation,
  type LiveWorkInboxSink,
  JobContract,
} from "../src/preview.js";

const f = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/pipeline-task-client-cases.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const metaID = "io.modelcontextprotocol/subscriptionId";
const options = { timeoutMs: 1000 };
const contract = new JobContract<{ id: string }, { rows: number }>(f.contract);
const replyContract = new JobContract<
  null,
  { email_column: "contact" | "alternate_contact" }
>(f.reply_contract);
const checkpoint = {
  version: 1,
  tool: f.tool,
  contract_sha256: contract.sha256,
  request_id: f.request_id,
  task_id: f.task_id,
};

function setup(fetchRequest: typeof fetch) {
  return createPipelineTaskClient({
    tool: f.tool,
    contract,
    replyContract,
    transport: createPipelineTaskHttpTransport({
      endpoint: "https://mcp.example.test/mcp",
      fetch: fetchRequest,
    }),
  });
}

function json(result: unknown) {
  return Response.json({ jsonrpc: "2.0", id: 1, result });
}
function rpcError(code: number) {
  return Response.json({
    jsonrpc: "2.0",
    id: 1,
    error: { code, message: "private" },
  });
}
function sse(messages: unknown[]) {
  const data = messages
    .map((message) => `data: ${JSON.stringify(message)}\r\n\r\n`)
    .join("");
  const split = data.indexOf("\r\n") + 1;
  return new Response(
    new ReadableStream({
      start(controller) {
        const encoder = new TextEncoder();
        controller.enqueue(encoder.encode(data.slice(0, split)));
        controller.enqueue(encoder.encode(data.slice(split)));
        controller.close();
      },
    }),
    { headers: { "content-type": "text/event-stream" } },
  );
}
function ack(ids: string[] = [f.task_id]) {
  return {
    jsonrpc: "2.0",
    method: "notifications/subscriptions/acknowledged",
    params: { notifications: { taskIds: ids }, _meta: { [metaID]: 1 } },
  };
}
function notification(wire: Record<string, any>) {
  const { resultType: _type, ...detail } = wire;
  return {
    jsonrpc: "2.0",
    method: "notifications/tasks",
    params: { ...detail, _meta: { ...wire._meta, [metaID]: 1 } },
  };
}

test("a dropped stream reconnects by checkpoint and keeps mutations explicit", async () => {
  const calls: string[] = [];
  let streams = 0;
  const client = setup(async (_url, init) => {
    const req = JSON.parse(String(init?.body));
    calls.push(req.method);
    assert.equal(new Headers(init?.headers).get("MCP-Method"), req.method);
    assert.ok(
      req.params._meta["io.modelcontextprotocol/clientCapabilities"].elicitation
        .form,
    );
    switch (req.method) {
      case "tools/call":
        assert.deepEqual(req.params.arguments, {
          request_id: f.request_id,
          input: f.input,
        });
        return json({
          resultType: "task",
          status: "working",
          taskId: f.task_id,
        });
      case "subscriptions/listen":
        assert.deepEqual(req.params.notifications.taskIds, [f.task_id]);
        return ++streams === 1
          ? sse([ack(), notification(f.working)])
          : sse([
              ack(),
              notification({
                ...f.working,
                lastUpdatedAt: "2026-11-18T09:00:01.000Z",
              }),
              notification(f.form),
              notification(f.completed),
            ]);
      case "tasks/update":
        assert.deepEqual(req.params.inputResponses, {
          [f.form_key]: { action: "accept", content: f.reply },
        });
        return json({ resultType: "complete" });
      case "resources/read":
        assert.equal(req.params.uri, f.resource.uri);
        return json({ contents: [f.resource] });
      default:
        throw new Error("unexpected " + req.method);
    }
  });
  const saved = await client.start(f.input, f.request_id, options);
  assert.deepEqual(saved, checkpoint);
  const task = client.resume(JSON.parse(JSON.stringify(saved)));
  const states = [];
  for await (const snapshot of task.watch(options)) {
    states.push(snapshot.status);
    if (snapshot.status === "input_required") {
      await assert.rejects(
        task.answer(
          snapshot,
          f.form_key,
          { email_column: "unknown" } as never,
          options,
        ),
        { code: "invalid_input" },
      );
      await task.answer(snapshot, f.form_key, f.reply, options);
    } else if (snapshot.status === "completed") {
      assert.equal(snapshot.output.rows, 2);
      assert.deepEqual(
        await task.readArtifact(snapshot, "report", options),
        f.resource,
      );
    }
  }
  assert.deepEqual(states, ["working", "input_required", "completed"]);
  assert.deepEqual(calls, [
    "tools/call",
    "subscriptions/listen",
    "subscriptions/listen",
    "tasks/update",
    "resources/read",
  ]);
});

test("unsupported subscriptions fall back to polling and denied subscriptions stop", async () => {
  const calls: string[] = [];
  let poll = 0;
  const client = setup(async (_url, init) => {
    const method = new Headers(init?.headers).get("MCP-Method")!;
    calls.push(method);
    return method === "subscriptions/listen"
      ? rpcError(-32601)
      : json(++poll === 1 ? f.working : f.completed);
  });
  const states = [];
  for await (const snapshot of client.resume(checkpoint).watch(options))
    states.push(snapshot.status);
  assert.deepEqual(states, ["working", "completed"]);
  assert.deepEqual(calls, ["subscriptions/listen", "tasks/get", "tasks/get"]);
  const oversized = setup(
    async () =>
      new Response("x".repeat(2 * 1024 * 1024), {
        headers: { "content-type": "text/event-stream" },
      }),
  );
  await assert.rejects(oversized.resume(checkpoint).watch(options).next(), {
    code: "invalid_response",
  });
  const denied = setup(async () => sse([ack([])]));
  await assert.rejects(denied.resume(checkpoint).watch(options).next(), {
    code: "not_found",
  });
  assert.throws(() => client.resume({ ...checkpoint, tool: "other" }), {
    code: "invalid_input",
  });
  const wrong = setup(async () => json({ ...f.working, taskId: f.run_id }));
  await assert.rejects(wrong.resume(checkpoint).get(options), {
    code: "invalid_response",
  });
});

test("watch deadline and cancellation close a blocked HTTP stream", async () => {
  for (const cancel of [false, true]) {
    let closed = false;
    let opened!: () => void;
    const ready = new Promise<void>((resolve) => {
      opened = resolve;
    });
    const client = setup(
      async () =>
        new Response(
          new ReadableStream({
            start() {
              opened();
            },
            cancel() {
              closed = true;
            },
          }),
          { headers: { "content-type": "text/event-stream" } },
        ),
    );
    const controller = new AbortController();
    const pending = client
      .resume(checkpoint)
      .watch({ timeoutMs: cancel ? 1000 : 25, signal: controller.signal })
      .next();
    await ready;
    if (cancel) controller.abort();
    await assert.rejects(pending, { code: cancel ? "cancelled" : "deadline" });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(closed, true);
  }
});

test("a lost mutation response never triggers an automatic retry", async () => {
  const calls: string[] = [];
  const client = setup(async (_url, init) => {
    const method = new Headers(init?.headers).get("MCP-Method")!;
    calls.push(method);
    if (method === "tasks/get") return json(f.form);
    throw new TypeError("lost acknowledgement");
  });
  await assert.rejects(client.start(f.input, f.request_id, options), {
    code: "request_failed",
  });
  const task = client.resume(checkpoint);
  const form = await task.get(options);
  assert.equal(form.status, "input_required");
  await assert.rejects(task.answer(form, f.form_key, f.reply, options), {
    code: "request_failed",
  });
  assert.deepEqual(calls, ["tools/call", "tasks/get", "tasks/update"]);
});

test("a handoff uses the compiled reply schema and a view from the same grant", async () => {
  const current = f.form.inputRequests[f.form_key];
  let form = { ...current, params: { ...current.params, requestedSchema: {
    ...current.params.requestedSchema, properties: {
      email_column: { type: "string", enum: ["contact"] },
    },
  } } };
  const calls: unknown[] = [];
  const reviewer = createPipelineTaskHandoffClient({
    handoffId: f.task_id,
    replyContract,
    transport: { async request(_method, params) {
      calls.push(params.arguments);
      return { isError: false, structuredContent: params.name === "pipeline_task_handoff_read"
        ? { handoff_id: f.task_id, expires_at: "2026-11-19T12:00:00.000Z", request_key: f.form_key, form }
        : { handoff_id: f.task_id, submitted: true },
      };
    } },
  });
  await assert.rejects(reviewer.get(options), { code: "invalid_response" });
  form = current;
  const view = await reviewer.get(options);
  const reply = { email_column: "alternate_contact" } as const;
  await assert.rejects(reviewer.answer({ ...view, handoff_id: f.request_id }, reply, options), { code: "invalid_input" });
  assert.equal(calls.length, 2);
  await reviewer.answer(view, reply, options);
  assert.deepEqual(calls.at(-1), { handoff_id: f.task_id, request_key: f.form_key, input: reply });
});


test("reviewer inbox paginates and refreshes authoritative snapshots after reconnect", async () => {
  const frames = f.reviewer_inbox;
  const resourceId = "reviewer-test";
  let mode = "pending", calls = 0, sink!: LiveWorkInboxSink, closed = false;
  const inbox = createPipelineTaskReviewerInbox({
    async request(method, params) {
      assert.equal(method, "tools/call");
      assert.equal(params.name, "pipeline_task_handoff_list");
      const args = params.arguments as { page_size: number; cursor?: string };
      assert.equal(args.page_size, 1);
      calls++;
      if (mode === "unauthorized") {
        return { isError: true, content: [{ type: "text", text: '{"code":"not_found"}' }] };
      }
      const page = mode === "empty" ? frames.empty : (args.cursor ? frames.next : frames.first);
      if (args.cursor) assert.equal(args.cursor, frames.first.next_cursor);
      return { isError: false, structuredContent: page };
    },
  });
  assert.deepEqual(await inbox.page({ ...options, pageSize: 1 }), frames.first);
  const watch = inbox.watch({
    timeoutMs: 3000, pageSize: 1, refreshIntervalMs: 2000,
    notifications: { resourceId, source(observer) {
      sink = observer;
      sink.ready();
      return () => { closed = true; };
    } },
  });
  assert.deepEqual((await watch.next()).value, { items: [...frames.first.items, ...frames.next.items], truncated: false });
  mode = "empty";
  const event = workInboxInvalidation(resourceId, "removed");
  sink.changed(event);
  sink.changed(event);
  assert.deepEqual((await watch.next()).value, { items: [], truncated: false });
  assert.equal(calls, 4);
  sink.changed(event);
  sink.changed(workInboxInvalidation("another-reviewer", "unrelated"));
  const fresh = watch.next();
  await delay(20);
  assert.equal(calls, 4);
  sink.disconnected();
  mode = "pending";
  sink.ready();
  assert.deepEqual((await fresh).value, { items: [...frames.first.items, ...frames.next.items], truncated: false });
  mode = "unauthorized";
  sink.changed(workInboxInvalidation(resourceId, "revoked"));
  await assert.rejects(watch.next(), { code: "unauthorized" });
  assert.equal(closed, true);
});

test("reviewer inbox bounds watches and rejects malformed pages", async () => {
  const frames = f.reviewer_inbox;
  let body = frames.first;
  const inbox = createPipelineTaskReviewerInbox({
    async request() { return { isError: false, structuredContent: body }; },
  });
  const watch = inbox.watch({ ...options, pageSize: 1, maxPages: 1 });
  try {
    assert.deepEqual((await watch.next()).value, { items: frames.first.items, truncated: true });
  } finally {
    await watch.return?.();
  }
  await assert.rejects(inbox.page({ ...options, cursor: frames.first.next_cursor }), { code: "invalid_response" });
  body = { items: [{ ...frames.first.items[0], task_id: f.task_id }], next_cursor: null };
  await assert.rejects(inbox.page(options), { code: "invalid_response" });
});


test("reviewer inbox polling recovers a lost MCP read", async () => {
  const frames = f.reviewer_inbox;
  let calls = 0;
  const inbox = createPipelineTaskReviewerInbox({
    async request() {
      if (++calls === 1) throw new Error("disconnected");
      return { isError: false, structuredContent: frames.empty };
    },
  });
  const watch = inbox.watch({ ...options, refreshIntervalMs: 5 });
  try {
    assert.deepEqual((await watch.next()).value, { items: [], truncated: false });
    assert.equal(calls, 2);
  } finally {
    await watch.return?.();
  }
});

test("approval clients bind the displayed review and return the durable decision", async () => {
  const { jobValueHash } = await import("../src/job-runtime.js");
  const review = contract.input(f.input);
  const view = {
    handoff_id: f.task_id, expires_at: "2026-11-19T12:00:00.000Z", title: "Import validated rows",
    review, review_sha256: jobValueHash(review), contract_sha256: contract.sha256, decision_key: "a".repeat(64),
  };
  const receipt = { handoff_id: f.task_id, state: "approved", decided_at: "2026-11-19T11:50:00.000Z" };
  const calls: unknown[] = [];
  const client = createPipelineTaskApprovalClient({
    handoffId: f.task_id, contract, transport: {
      async request(_method, params) {
        calls.push(params.arguments);
        return { isError: false, structuredContent: params.name === "pipeline_task_handoff_approval" ? view : receipt };
      },
    },
  });
  const preview = await client.get(options);
  assert.deepEqual(preview, view);
  await assert.rejects(client.decide({ ...preview, review: { id: "changed" } }, "approved", { rows: 2 }, options), {
    code: "invalid_input",
  });
  assert.equal(calls.length, 1);
  assert.deepEqual(await client.decide(preview, "approved", { rows: 2 }, options), receipt);
  assert.deepEqual(calls.at(-1), {
    handoff_id: f.task_id, decision_key: view.decision_key, state: "approved", input: { rows: 2 },
  });
});
