// Tool dispatch. A tool gets its input and the thread's callback URL, acts
// in its own time, and POSTs its result there:
//
//   POST <callback_url>
//   {"message_id": "<unique per result>", "kind": "tool_result", "data": {...}}
//
// The result becomes the thread's next inbox message, and the next slice
// runs on it. A repeated message_id lands once.
//
// With TOOL_URL set, the starter POSTs each call there. Without it, the call
// waits in the thread (GET /threads/:id shows it) for a tool that reads it.
export class HTTPTools {
  constructor({ url = process.env.TOOL_URL, fetchImpl = fetch } = {}) {
    this.url = url;
    this.fetch = fetchImpl;
  }

  async dispatch(threadID, call) {
    if (!this.url) return;
    const res = await this.fetch(this.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ thread_id: threadID, tool: call.tool, input: call.input, callback_url: call.callback_url }),
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) throw new Error(`tool dispatch: HTTP ${res.status}`);
  }
}

// Named Run tasks return through the durable RAP inbox. The task version must
// explicitly allow this app's endpoint; the platform supplies workload identity.
export class GregaleTaskTools {
  constructor({ platform }) { this.platform = platform; }
  async dispatch(threadID, call) {
    await this.platform.call("/v1/agent-tools/invoke", {
      operation: "gregale_task", group_id: threadID, id: call.id,
      call_id: call.provider_call_id ?? call.id,
      arguments: call.input, callback_url: call.callback_url,
    });
  }
}

// Both task and pipeline calls use the durable Agent inbox. The caller persists
// the same logical call ID before dispatch, as for GregaleTaskTools.
export class GregalePipelineTools extends GregaleTaskTools {
  async dispatch(threadID, call) {
    await this.platform.call("/v1/agent-tools/invoke", {
      operation: "gregale_pipeline", group_id: threadID, id: call.id,
      call_id: call.provider_call_id ?? call.id,
      arguments: call.input, callback_url: call.callback_url,
    });
  }
}

// Gregale's platform tools (gregale_sleep, gregale_run, gregale_task, ...)
// are RAP tools apid serves at /v1/agent-tools. Each is acknowledged at once
// and its result arrives at the call's callback URL as the thread's next
// inbox message. The invocation carries the app's workload token
// (platform.call), which gregale_run and gregale_github_subscribe need.
export class GregaleRapTools {
  constructor({ platform }) { this.platform = platform; }
  async dispatch(threadID, call) {
    await this.platform.call("/v1/agent-tools/invoke", {
      operation: call.tool, group_id: threadID, id: call.id,
      call_id: call.provider_call_id ?? call.id,
      arguments: call.input, callback_url: call.callback_url,
    });
  }
}

const lookupTool = {
  name: "lookup",
  description: "Look something up with this app's own tool service. The answer arrives as a later message.",
  input_schema: { type: "object", properties: { query: { type: "string", description: "What to look up" } },
    required: ["query"], additionalProperties: false },
};

// Toolbox is what the model may call and where each call goes. Platform
// tools are read from Gregale's RAP discovery document once per process and
// filtered by AGENT_PLATFORM_TOOLS (comma-separated, default gregale_sleep;
// gregale_run starts agent runs, so turn it on deliberately). With TOOL_URL
// set, the app's own lookup tool is offered too.
export class Toolbox {
  constructor({ platform, http = new HTTPTools(), env = process.env, fetchImpl = fetch } = {}) {
    this.http = http;
    this.rap = new GregaleRapTools({ platform });
    this.apiURL = (env.GREGALE_API_URL || "https://api.gregale.dev").replace(/\/+$/, "");
    this.allowed = new Set((env.AGENT_PLATFORM_TOOLS ?? "gregale_sleep").split(",").map((s) => s.trim()).filter(Boolean));
    this.lookup = Boolean(env.TOOL_URL);
    this.fetch = fetchImpl;
    this.platformTools = null;
  }

  async definitions() {
    const tools = this.lookup ? [lookupTool] : [];
    return [...tools, ...(await this.discover())];
  }

  // discover caches a successful read only, so a failed one is retried by
  // the next slice; meanwhile the agent runs with its other tools.
  async discover() {
    if (this.platformTools) return this.platformTools;
    if (this.allowed.size === 0) return (this.platformTools = []);
    try {
      const res = await this.fetch(`${this.apiURL}/v1/agent-tools/.well-known/rap-toolset`, { signal: AbortSignal.timeout(5_000) });
      if (!res.ok) return [];
      const doc = await res.json();
      this.platformTools = (doc.tools ?? []).filter((t) => this.allowed.has(t.name))
        .map((t) => ({ name: t.name, description: t.description, input_schema: t.inputSchema }));
      return this.platformTools;
    } catch {
      return [];
    }
  }

  dispatch(threadID, call) {
    return (call.tool.startsWith("gregale_") ? this.rap : this.http).dispatch(threadID, call);
  }
}
