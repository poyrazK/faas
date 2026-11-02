// The slice contract against an in-memory store: two turns through one
// asynchronous tool call, a redelivered message landing once, and a
// redelivery finishing a dispatch the first delivery did not.
import assert from "node:assert/strict";
import { test } from "node:test";
import { parseRecord, runSlice } from "../agent.js";
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

function record(kind, messageID, data) {
  return { endpoint_id: "ep-1", channel: "agent-inbox", metadata: { thread_id: "t-1", message_id: messageID, kind },
    data_base64: Buffer.from(JSON.stringify(data)).toString("base64") };
}

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
