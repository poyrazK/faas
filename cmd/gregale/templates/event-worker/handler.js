// event-worker — a minimal internal event subscription handler.
//
// Gregale delivers the canonical event envelope as the request body. The
// handler logs only bounded, non-sensitive routing fields and leaves the
// payload available to application code for its own processing.

export async function handler(event, ctx) {
  let envelope = {};
  if (event && typeof event.body === "string" && event.body.length > 0) {
    try {
      envelope = JSON.parse(event.body);
    } catch {
      throw new Error("Gregale event body was not valid JSON");
    }
  }

  ctx.log.info("event received", {
    event_id: typeof envelope.id === "string" ? envelope.id : "unknown",
    source: typeof envelope.source === "string" ? envelope.source : "unknown",
    type: typeof envelope.type === "string" ? envelope.type : "unknown",
  });

  return {
    statusCode: 202,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ accepted: true }),
  };
}
