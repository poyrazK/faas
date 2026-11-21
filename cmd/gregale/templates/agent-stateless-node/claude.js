// A real model step: one Claude call per slice (ADR-20261030125501 §1).
//
// The slice contract allows at most one model completion per inbox message,
// and a tool's result comes back later as the thread's next message. So
// there is no in-process tool loop here: each slice rebuilds the
// conversation from the thread's rows, asks Claude for the next step, and
// returns either one tool call or the answer. Claude's response blocks are
// stored with the turn and sent back unchanged on later slices, which keeps
// its thinking blocks valid across slices.
//
// Reach Claude in one of two ways:
//
//   Gregale AI gateway (recommended; no key in the app):
//     GREGALE_AI_INTEGRATION_ID  managed outbound integration bound to this
//                                app, created with ai_provider anthropic.
//     Calls go to GREGALE_OUTBOUND_URL, which Gregale sets inside the VM;
//     the gateway injects the provider key, applies the integration's
//     budgets and records token usage per app.
//
//   Or ANTHROPIC_API_KEY, set as a secret.
//
// AGENT_MODEL (default claude-opus-5-5), AGENT_EFFORT (default medium),
// AGENT_SYSTEM_PROMPT, AGENT_MODEL_TIMEOUT_MS (default 50000: a slice must
// finish inside the app's request timeout, 1 min on Hobby) and
// AGENT_FALLBACKS=off tune it.
import Anthropic from "@anthropic-ai/sdk";

export const defaultModelID = "claude-opus-5-5";
const fallbackBeta = "server-side-fallback-2026-07-01";
const efforts = new Set(["low", "medium", "high", "xhigh", "max"]);

const defaultSystemPrompt =
  "You are an assistant that runs as a stateless agent. Use a tool when it helps; " +
  "a tool's result arrives as a later message, so call at most one tool at a time. " +
  "When you have what you need, answer plainly.";

// claudeConfig reads the environment. It returns null when no way to reach
// Claude is configured, so the template falls back to its scripted model.
export function claudeConfig(env = process.env) {
  const integration = env.GREGALE_AI_INTEGRATION_ID ?? "";
  const key = env.ANTHROPIC_API_KEY ?? "";
  if (!integration && !key) return null;
  if (integration && key) {
    throw new Error("GREGALE_AI_INTEGRATION_ID uses the gateway's key; unset ANTHROPIC_API_KEY");
  }
  let baseURL;
  if (integration) {
    if (env.GREGALE_AI_PROVIDER && env.GREGALE_AI_PROVIDER !== "anthropic") {
      throw new Error("this template calls Claude: the integration's ai_provider must be anthropic");
    }
    const outbound = (env.GREGALE_OUTBOUND_URL ?? "").replace(/\/+$/, "");
    if (!outbound) throw new Error("GREGALE_OUTBOUND_URL is not set; the Gregale outbound relay is unavailable on this runtime");
    baseURL = `${outbound}/i/${encodeURIComponent(integration)}`;
  }
  const effort = env.AGENT_EFFORT || "medium";
  if (!efforts.has(effort)) throw new Error(`AGENT_EFFORT must be one of ${[...efforts].join(", ")}`);
  const timeoutMS = Number.parseInt(env.AGENT_MODEL_TIMEOUT_MS ?? "", 10);
  return {
    baseURL,
    // In gateway mode outboundd replaces the credential headers.
    apiKey: integration ? "gregale-ai-gateway" : key,
    model: env.AGENT_MODEL || defaultModelID,
    effort,
    system: env.AGENT_SYSTEM_PROMPT || defaultSystemPrompt,
    fallbacks: env.AGENT_FALLBACKS !== "off",
    timeoutMS: Number.isInteger(timeoutMS) && timeoutMS > 0 ? timeoutMS : 50_000,
  };
}

export class ClaudeModel {
  // tools: { definitions(): Promise<Anthropic tool definitions[]> }
  constructor(config, { tools, client } = {}) {
    this.config = config;
    this.tools = tools ?? { definitions: async () => [] };
    this.client = client ?? new Anthropic({ apiKey: config.apiKey, baseURL: config.baseURL, maxRetries: 1 });
  }

  async next(history, message) {
    const tools = await this.tools.definitions();
    const params = {
      model: this.config.model,
      max_tokens: 16000,
      system: this.config.system,
      messages: toMessages([...history, { role: message.role, content: message.content }]),
      output_config: { effort: this.config.effort },
    };
    if (tools.length > 0) {
      params.tools = tools;
      // One asynchronous tool call per slice (§1).
      params.tool_choice = { type: "auto", disable_parallel_tool_use: true };
    }
    if (this.config.fallbacks) {
      // A refused request is re-served by the recommended fallback model in
      // the same call instead of stopping the thread.
      params.betas = [fallbackBeta];
      params.fallbacks = "default";
    }
    const response = await this.client.beta.messages.create(params, { timeout: this.config.timeoutMS });
    return stepFrom(response);
  }
}

// stepFrom turns one response into the slice's step.
export function stepFrom(response) {
  const blocks = response.content ?? [];
  if (response.stop_reason === "tool_use") {
    const use = blocks.find((b) => b.type === "tool_use");
    if (use) return { call: { tool: use.name, input: use.input ?? {}, provider_call_id: use.id }, blocks };
  }
  if (response.stop_reason === "refusal") {
    return { text: "I can't help with that request.", final: true, refused: true };
  }
  const text = blocks.filter((b) => b.type === "text").map((b) => b.text).join("\n").trim();
  return { text: text || "(no answer)", final: true, blocks, truncated: response.stop_reason === "max_tokens" };
}

// toMessages rebuilds the Messages API conversation from the thread's rows.
// A tool result follows the tool_use it answers. A user message that arrives
// while a tool is still running first closes that call with a note, and the
// late result then arrives as ordinary text.
export function toMessages(rows) {
  const out = [];
  let open = null; // the tool_use id still waiting for its result
  const push = (role, content) => out.push({ role, content });
  for (const row of rows) {
    const c = row.content ?? {};
    if (row.role === "assistant") {
      if (Array.isArray(c.blocks) && c.blocks.length > 0) {
        push("assistant", c.blocks);
        const use = c.blocks.find((b) => b.type === "tool_use");
        open = use ? use.id : null;
      } else {
        push("assistant", [{ type: "text", text: (c.text ?? (c.tool_call ? `(called ${c.tool_call.tool})` : "")) || "(no answer)" }]);
        open = null;
      }
      continue;
    }
    if (row.role === "tool") {
      const text = resultText(c);
      if (open) {
        push("user", [{ type: "tool_result", tool_use_id: open, content: text }]);
        open = null;
      } else {
        push("user", [{ type: "text", text: `A tool result arrived late: ${text}` }]);
      }
      continue;
    }
    // user
    if (open) {
      push("user", [{ type: "tool_result", tool_use_id: open, content: "The result has not arrived yet; it will come as a later message." }]);
      open = null;
    }
    push("user", [{ type: "text", text: typeof c.text === "string" ? c.text : JSON.stringify(c) }]);
  }
  return out;
}

// resultText reads a tool result: a RAP tool_result carries its text; any
// other tool posts JSON of its own.
function resultText(content) {
  if (content && typeof content === "object" && typeof content.text === "string") return content.text;
  return typeof content === "string" ? content : JSON.stringify(content);
}
