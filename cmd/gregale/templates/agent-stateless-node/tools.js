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
