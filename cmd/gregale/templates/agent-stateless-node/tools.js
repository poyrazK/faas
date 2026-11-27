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
