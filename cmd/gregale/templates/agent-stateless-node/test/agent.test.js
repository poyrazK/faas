// The slice contract against an in-memory store: two turns through one
// asynchronous tool call, a redelivered message landing once, and a
// redelivery finishing a dispatch the first delivery did not.
import assert from "node:assert/strict";
import { test } from "node:test";
import { handleSlice, maxAttempts, parseRecord, runSlice } from "../agent.js";
import { scriptedModel } from "../model.js";

class MemoryStore {
  constructor() { this.rows = []; }
  async transaction(threadID, fn) {
    const staged = [];
    const rows = () => [...this.rows, ...staged].filter((r) => r.thread_id === threadID);
    const out = await fn({
      seen: async (id) => rows().some((r) => r.message_id === id),
      undispatched: async (id) => rows().find((r) => r.message_id === `assistant:${id}` && r.content.tool_call?.dispatched === false)?.content.tool_call ?? null,
      answered: async (id) => rows().some((r) => r.message_id === `assistant:${id}` && r.content.final === true),
      history: async () => rows().map(({ role, content }) => ({ role, content })),
      append: async (message_id, role, content) => { staged.push({ thread_id: threadID, message_id, role, content: structuredClone(content) }); },
    });
    this.rows.push(...staged);
    return out;
  }
  async markDispatched(threadID, id) {
    this.rows.find((r) => r.thread_id === threadID && r.message_id === id).content.tool_call.dispatched = true;
  }
}

function fixture({ dispatchFails = 0 } = {}) {
  const calls = { issued: [], revoked: [], dispatched: [] };
  let failures = dispatchFails;
  return {
    calls,
    deps: {
      store: new MemoryStore(),
      model: scriptedModel,
      platform: {
        issueCallback: async (ep, thread) => { calls.issued.push(thread); return { url: `https://api.example.test/v1/agent-callbacks/${thread}`, expires_at: "2026-11-02T00:00:00Z" }; },
        revokeCallbacks: async (ep, thread) => { calls.revoked.push(thread); return {}; },
      },
      tools: { dispatch: async (thread, call) => {
        if (failures-- > 0) throw new Error("tool down");
        calls.dispatched.push(call.callback_url);
      } },
    },
  };
}

function record(kind, messageID, data, attempt) {
  return { endpoint_id: "ep-1", channel: "agent-inbox", metadata: { thread_id: "t-1", message_id: messageID, kind },
    data_base64: Buffer.from(JSON.stringify(data)).toString("base64"), attempt };
}

const quiet = { warn() {} };

test("two turns through one asynchronous tool call", async () => {
  const { deps, calls } = fixture();
  const first = await runSlice(parseRecord(record("user", "m-1", { text: "capital of Malta?" })), deps);
  assert.equal(first.pending.tool, "lookup");
  assert.deepEqual(calls.dispatched, ["https://api.example.test/v1/agent-callbacks/t-1"]);
  const second = await runSlice(parseRecord(record("tool_result", "r-1", { answer: "Valletta" })), deps);
  assert.equal(second.final, true);
  assert.deepEqual(calls.revoked, ["t-1"]);
  const answer = deps.store.rows.at(-1);
  assert.equal(answer.role, "assistant");
  assert.match(answer.content.text, /Valletta/);
});

test("a redelivered message lands once", async () => {
  const { deps, calls } = fixture();
  const message = parseRecord(record("user", "m-1", { text: "hi" }));
  await runSlice(message, deps);
  const again = await runSlice(message, deps);
  assert.equal(again.skipped, true);
  assert.equal(deps.store.rows.length, 2);
  assert.equal(calls.issued.length, 1);
  assert.equal(calls.dispatched.length, 1);
});

test("a redelivery dispatches a tool call the first delivery did not", async () => {
  const { deps, calls } = fixture({ dispatchFails: 1 });
  const message = parseRecord(record("user", "m-1", { text: "hi" }));
  await assert.rejects(runSlice(message, deps), /tool down/);
  await runSlice(message, deps);
  assert.equal(calls.issued.length, 1);
  assert.equal(calls.dispatched.length, 1);
  assert.equal(deps.store.rows.at(-1).content.tool_call.dispatched, true);
});

test("records that are not inbox messages are refused", () => {
  assert.equal(parseRecord({ endpoint_id: "ep-1", metadata: { thread_id: "t", message_id: "m", kind: "other" } }), null);
  assert.equal(parseRecord({ endpoint_id: "ep-1", metadata: { thread_id: "t", message_id: "m", kind: "user" }, data_base64: "!!" }), null);
});

test("a failing slice is retried before its last attempt", async () => {
  const { deps } = fixture({ dispatchFails: 10 });
  const message = parseRecord(record("user", "m-1", { text: "hi" }, 4));
  await assert.rejects(handleSlice(message, deps, { attempts: 5, log: quiet }), /tool down/);
});

test("the last attempt closes the thread instead of blocking it", async () => {
  const { deps, calls } = fixture({ dispatchFails: 10 });
  const message = parseRecord(record("user", "m-1", { text: "hi" }, 5));
  const turn = await handleSlice(message, deps, { attempts: 5, log: quiet });
  assert.equal(turn.failed, true);
  assert.match(turn.reason, /tool down/);
  const last = deps.store.rows.at(-1);
  assert.equal(last.message_id, "failed:m-1");
  assert.equal(last.content.final, true);
  assert.equal(last.content.failed, true);
  assert.deepEqual(calls.revoked, ["t-1"]);
  // A redelivery of the same last attempt records the failure once.
  await handleSlice(message, deps, { attempts: 5, log: quiet });
  assert.equal(deps.store.rows.filter((r) => r.message_id === "failed:m-1").length, 1);
});

test("a message that failed before it was stored is kept with the failure", async () => {
  const { deps } = fixture();
  deps.model = { next() { throw new Error("model refused"); } };
  const turn = await handleSlice(parseRecord(record("user", "m-1", { text: "hi" }, 5)), deps, { attempts: 5, log: quiet });
  assert.equal(turn.failed, true);
  assert.deepEqual(deps.store.rows.map((r) => r.message_id), ["m-1", "failed:m-1"]);
});

test("a revoke failure does not fail the closing slice", async () => {
  const { deps } = fixture({ dispatchFails: 10 });
  deps.platform.revokeCallbacks = async () => { throw new Error("api down"); };
  const turn = await handleSlice(parseRecord(record("user", "m-1", { text: "hi" }, 5)), deps, { attempts: 5, log: quiet });
  assert.equal(turn.failed, true);
});

test("records carry their delivery attempt", () => {
  assert.equal(parseRecord(record("user", "m-1", {}, 3)).attempt, 3);
  assert.equal(parseRecord(record("user", "m-1", {})).attempt, 1);
});

test("AGENT_MAX_ATTEMPTS defaults to the group's five attempts", () => {
  assert.equal(maxAttempts({}), 5);
  assert.equal(maxAttempts({ AGENT_MAX_ATTEMPTS: "3" }), 3);
  assert.equal(maxAttempts({ AGENT_MAX_ATTEMPTS: "99" }), 5);
});

// Platform task acknowledgements are empty RAP 200 responses.
test("named task dispatch carries stable RAP identity and app authority", async () => {
  const { GregaleTaskTools } = await import("../tools.js");
  const calls = [];
  const tools = new GregaleTaskTools({ platform: { call: async (path, body) => calls.push({ path, body }) } });
  const call = { id: "call-1", input: { name: "report", version: 1, input: { rows: [1] } }, callback_url: "signed-callback" };
  await tools.dispatch("thread-1", call);
  await tools.dispatch("thread-1", call);
  assert.deepEqual(calls[0], calls[1]);
  assert.equal(calls[0].body.operation, "gregale_task");
  assert.equal(calls[0].body.group_id, "thread-1");
  assert.equal(calls[0].body.id, "call-1");
});

test("a named task persists its identity through dispatch retry and resumes the next slice once", async () => {
  const { GregaleTaskTools } = await import("../tools.js");
  const { deps, calls } = fixture();
  const invocations = [];
  deps.platform.call = async (_path, body) => { invocations.push(body); if (invocations.length === 1) throw new Error("lost acknowledgement"); };
  deps.tools = new GregaleTaskTools({ platform: deps.platform });
  deps.model = { next: (_history, message) => message.role === "user"
    ? { call: { tool: "gregale_task", input: { name: "report", version: 1, input: { rows: [1, 2, 3] } } } }
    : { text: JSON.stringify(message.content), final: true } };
  const user = parseRecord(record("user", "request-1", { text: "report" }));
  await assert.rejects(runSlice(user, deps), /lost acknowledgement/);
  await runSlice(user, deps);
  assert.deepEqual(invocations[0], invocations[1]);
  assert.equal(invocations[0].id, "assistant:request-1");
  assert.equal(calls.issued.length, 1);
  const completion = parseRecord(record("tool_result", "completion-1", {
    type: "tool_result", id: invocations[0].id,
    text: JSON.stringify({ execution_id: "run-1", status: "succeeded", result: { rows: 3 }, receipt: "/v1/agent-tools/runs/run-1" }),
  }));
  await runSlice(completion, deps);
  const replay = await runSlice(completion, deps);
  assert.equal(replay.skipped, true);
  assert.equal(deps.store.rows.length, 4);
  assert.match(deps.store.rows.at(-1).content.text, /run-1/);
});

test("platform accepts empty successful task acknowledgements", async () => {
  const { GregalePlatform } = await import("../platform.js");
  const platform = new GregalePlatform({ apiURL: "https://api.example.test", identityURL: "https://identity.example.test",
    fetchImpl: async (url) => String(url).startsWith("https://identity.example.test")
      ? new Response(JSON.stringify({ access_token: "workload" })) : new Response(null, { status: 200 }) });
  assert.equal(await platform.call("/v1/agent-tools/invoke", {}), undefined);
});

test("task result helpers read scoped paths with fresh app identity", async () => {
  const { GregalePlatform } = await import("../platform.js");
  const requests = [];
  const platform = new GregalePlatform({ apiURL: "https://api.example.test", identityURL: "https://identity.example.test",
    fetchImpl: async (url, init) => {
      if (String(url).startsWith("https://identity.example.test")) return new Response(JSON.stringify({ access_token: "workload" }));
      requests.push({ url: String(url), init });
      return new Response(JSON.stringify({ id: "run" }));
    } });
  await platform.taskReceipt("run");
  await platform.pipelineReceipt("pipeline");
  await platform.pipelineEvents("pipeline",7);
  await platform.taskArtifact("run", "reports/answer.txt");
  assert.equal(requests[0].url, "https://api.example.test/v1/agent-tools/runs/run");
  assert.equal(requests[1].url, "https://api.example.test/v1/agent-tools/pipelines/pipeline");
  assert.equal(requests[2].url,"https://api.example.test/v1/agent-tools/pipelines/pipeline/events?after=7");
  assert.equal(requests[3].url, "https://api.example.test/v1/agent-tools/runs/run/artifacts/reports/answer.txt");
  await assert.rejects(platform.pipelineEvents("pipeline",-1),/Invalid pipeline cursor/);
  for (const request of requests) assert.equal(request.init.headers.authorization, "Bearer workload");
  await assert.rejects(platform.taskArtifact("run", "../secret"), /invalid output path/);
});

test("pipeline dispatch preserves logical identity", async () => {
  const { GregalePipelineTools } = await import("../tools.js");
  const calls = [];
  const tools = new GregalePipelineTools({ platform: { call: async (path, body) => calls.push({ path, body }) } });
  await tools.dispatch("thread", { id: "call", provider_call_id: "provider", input: { batch: { name: "report", version: 1, items: [{ label: "one", input: {} }] } }, callback_url: "https://api.example/callback" });
  assert.equal(calls[0].body.operation, "gregale_pipeline");
  assert.equal(calls[0].body.id, "call");
  assert.equal(calls[0].body.call_id, "provider");
  assert.equal(calls[0].body.group_id, "thread");
});

test("artifact delivery helpers use app-owned routes after continuation", async () => {
  const { GregalePlatform } = await import("../platform.js");
  const requests = [];
  const platform = new GregalePlatform({ apiURL: "https://api.example.test", identityURL: "https://identity.example.test",
    fetchImpl: async (url, init) => {
      if (String(url).startsWith("https://identity.example.test")) return new Response(JSON.stringify({ access_token: "workload" }));
      requests.push({ path: new URL(url).pathname, body: init.body && JSON.parse(init.body), method: init.method ?? "GET" });
      return new Response(JSON.stringify({ id: "delivery", status: "completed" }));
    } });
  const intent = { idempotency_key: "completion-report", execution_id: "run", artifact_name: "report.html", sha256: "sha256:expected", size_bytes: 20, content_type: "text/html" };
  await platform.deliverRunArtifact("reports", "reports", intent);
  await platform.artifactDelivery("reports", "reports", "delivery");
  await platform.signArtifactDelivery("reports", "reports", "delivery");
  assert.deepEqual(requests.map(r => r.path), ["/v1/apps/reports/run-artifact-deliveries/reports", "/v1/apps/reports/run-artifact-deliveries/reports/delivery", "/v1/apps/reports/run-artifact-deliveries/reports/delivery/download-url"]);
  assert.deepEqual(requests[0].body, intent);
});
