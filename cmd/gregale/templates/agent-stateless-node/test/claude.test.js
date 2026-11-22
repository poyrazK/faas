// The Claude model step against a fake client: the conversation rebuilt from
// thread rows, the request each slice sends, and the step each response
// becomes. No network and no key.
import assert from "node:assert/strict";
import { test } from "node:test";
import { ClaudeModel, claudeConfig, defaultModelID, stepFrom, toMessages } from "../claude.js";
import { Toolbox } from "../tools.js";

const useBlocks = [{ type: "thinking", thinking: "", signature: "sig-1" }, { type: "tool_use", id: "toolu_1", name: "gregale_sleep", input: { seconds: 60 } }];

test("tool results follow the tool_use they answer, and blocks are replayed unchanged", () => {
  const messages = toMessages([
    { role: "user", content: { text: "remind me in a minute" } },
    { role: "assistant", content: { tool_call: { tool: "gregale_sleep" }, blocks: useBlocks } },
    { role: "tool", content: { type: "tool_result", call_id: "toolu_1", text: "Slept for 60 seconds." } },
  ]);
  assert.deepEqual(messages, [
    { role: "user", content: [{ type: "text", text: "remind me in a minute" }] },
    { role: "assistant", content: useBlocks },
    { role: "user", content: [{ type: "tool_result", tool_use_id: "toolu_1", content: "Slept for 60 seconds." }] },
  ]);
});

test("a user message during a running tool closes the call, and the late result is text", () => {
  const messages = toMessages([
    { role: "user", content: { text: "a" } },
    { role: "assistant", content: { blocks: useBlocks } },
    { role: "user", content: { text: "still there?" } },
    { role: "tool", content: { answer: 42 } },
  ]);
  assert.equal(messages[2].content[0].type, "tool_result");
  assert.match(messages[2].content[0].content, /not arrived yet/);
  assert.deepEqual(messages[3].content, [{ type: "text", text: "still there?" }]);
  assert.deepEqual(messages[4].content, [{ type: "text", text: 'A tool result arrived late: {"answer":42}' }]);
});

test("turns without blocks (scripted or failed) render as text", () => {
  const messages = toMessages([
    { role: "user", content: { text: "a" } },
    { role: "assistant", content: { text: "This conversation stopped: boom", final: true, failed: true } },
    { role: "user", content: { text: "again" } },
  ]);
  assert.deepEqual(messages[1], { role: "assistant", content: [{ type: "text", text: "This conversation stopped: boom" }] });
});

test("responses become steps", () => {
  assert.deepEqual(stepFrom({ stop_reason: "tool_use", content: useBlocks }),
    { call: { tool: "gregale_sleep", input: { seconds: 60 }, provider_call_id: "toolu_1" }, blocks: useBlocks });
  const answer = stepFrom({ stop_reason: "end_turn", content: [{ type: "text", text: "Valletta" }] });
  assert.equal(answer.text, "Valletta");
  assert.equal(answer.final, true);
  const refused = stepFrom({ stop_reason: "refusal", content: [] });
  assert.equal(refused.final, true);
  assert.equal(refused.refused, true);
  assert.equal(refused.blocks, undefined);
  assert.equal(stepFrom({ stop_reason: "max_tokens", content: [{ type: "text", text: "par" }] }).truncated, true);
});

test("each slice sends one request with effort, one tool at a time and refusal fallbacks", async () => {
  const sent = [];
  const client = { beta: { messages: { create: async (params, opts) => { sent.push({ params, opts }); return { stop_reason: "end_turn", content: [{ type: "text", text: "hi" }] }; } } } };
  const tools = { definitions: async () => [{ name: "gregale_sleep", description: "d", input_schema: { type: "object" } }] };
  const config = claudeConfig({ ANTHROPIC_API_KEY: "k" });
  const model = new ClaudeModel(config, { tools, client });
  const step = await model.next([], { role: "user", content: { text: "hello" } });
  assert.equal(step.text, "hi");
  const { params, opts } = sent[0];
  assert.equal(params.model, defaultModelID);
  assert.deepEqual(params.output_config, { effort: "medium" });
  assert.deepEqual(params.tool_choice, { type: "auto", disable_parallel_tool_use: true });
  assert.deepEqual(params.betas, ["server-side-fallback-2026-07-01"]);
  assert.equal(params.fallbacks, "default");
  assert.equal(params.thinking, undefined);
  assert.deepEqual(params.messages, [{ role: "user", content: [{ type: "text", text: "hello" }] }]);
  assert.equal(opts.timeout, 50_000);
});

test("without tools or with fallbacks off, those fields are left out", async () => {
  let params;
  const client = { beta: { messages: { create: async (p) => { params = p; return { stop_reason: "end_turn", content: [] }; } } } };
  const model = new ClaudeModel(claudeConfig({ ANTHROPIC_API_KEY: "k", AGENT_FALLBACKS: "off" }), { client });
  await model.next([], { role: "user", content: { text: "x" } });
  assert.equal(params.tools, undefined);
  assert.equal(params.tool_choice, undefined);
  assert.equal(params.betas, undefined);
  assert.equal(params.fallbacks, undefined);
});

test("config: scripted without a key, gateway through the outbound relay, refusals of bad settings", () => {
  assert.equal(claudeConfig({}), null);
  const gw = claudeConfig({ GREGALE_AI_INTEGRATION_ID: "int/1", GREGALE_OUTBOUND_URL: "http://127.0.0.1:2774/" });
  assert.equal(gw.baseURL, "http://127.0.0.1:2774/i/int%2F1");
  assert.equal(gw.apiKey, "gregale-ai-gateway");
  assert.throws(() => claudeConfig({ GREGALE_AI_INTEGRATION_ID: "i" }), /GREGALE_OUTBOUND_URL/);
  assert.throws(() => claudeConfig({ GREGALE_AI_INTEGRATION_ID: "i", ANTHROPIC_API_KEY: "k", GREGALE_OUTBOUND_URL: "u" }), /unset ANTHROPIC_API_KEY/);
  assert.throws(() => claudeConfig({ GREGALE_AI_INTEGRATION_ID: "i", GREGALE_AI_PROVIDER: "openai", GREGALE_OUTBOUND_URL: "u" }), /anthropic/);
  assert.throws(() => claudeConfig({ ANTHROPIC_API_KEY: "k", AGENT_EFFORT: "huge" }), /AGENT_EFFORT/);
  assert.equal(claudeConfig({ ANTHROPIC_API_KEY: "k", AGENT_MODEL: "claude-sonnet-5-5" }).model, "claude-sonnet-5-5");
});

function discovery(tools) {
  let calls = 0;
  return {
    get calls() { return calls; },
    fetchImpl: async (url) => {
      calls++;
      assert.match(String(url), /\/v1\/agent-tools\/\.well-known\/rap-toolset$/);
      return { ok: true, json: async () => ({ tools }) };
    },
  };
}

test("the toolbox offers allowed platform tools from discovery, once per process", async () => {
  const d = discovery([
    { name: "gregale_sleep", description: "sleep", inputSchema: { type: "object" } },
    { name: "gregale_run", description: "run", inputSchema: { type: "object" } },
  ]);
  const box = new Toolbox({ platform: {}, env: { TOOL_URL: "https://tools.example.test" }, fetchImpl: d.fetchImpl });
  assert.deepEqual((await box.definitions()).map((t) => t.name), ["lookup", "gregale_sleep"]);
  await box.definitions();
  assert.equal(d.calls, 1);
  const all = new Toolbox({ platform: {}, env: { AGENT_PLATFORM_TOOLS: "gregale_sleep,gregale_run" }, fetchImpl: d.fetchImpl });
  assert.deepEqual((await all.definitions()).map((t) => [t.name, t.input_schema.type]), [["gregale_sleep", "object"], ["gregale_run", "object"]]);
});

test("a failed discovery leaves the agent its other tools and is retried", async () => {
  let fail = true;
  const box = new Toolbox({ platform: {}, env: {}, fetchImpl: async () => {
    if (fail) throw new Error("down");
    return { ok: true, json: async () => ({ tools: [{ name: "gregale_sleep", description: "s", inputSchema: {} }] }) };
  } });
  assert.deepEqual(await box.definitions(), []);
  fail = false;
  assert.deepEqual((await box.definitions()).map((t) => t.name), ["gregale_sleep"]);
});

test("platform tool calls go to the RAP invoke endpoint; others to TOOL_URL", async () => {
  const invoked = [];
  const http = [];
  const box = new Toolbox({ platform: { call: async (path, body) => invoked.push({ path, body }) },
    http: { dispatch: async (thread, call) => http.push(call.tool) }, env: {}, fetchImpl: async () => ({ ok: false }) });
  await box.dispatch("t-1", { id: "assistant:m-1", provider_call_id: "toolu_1", tool: "gregale_sleep", input: { seconds: 5 }, callback_url: "cb" });
  await box.dispatch("t-1", { id: "assistant:m-2", tool: "lookup", input: { query: "q" }, callback_url: "cb" });
  assert.deepEqual(invoked, [{ path: "/v1/agent-tools/invoke", body: { operation: "gregale_sleep", group_id: "t-1",
    id: "assistant:m-1", call_id: "toolu_1", arguments: { seconds: 5 }, callback_url: "cb" } }]);
  assert.deepEqual(http, ["lookup"]);
});
