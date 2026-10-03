// event-worker — a minimal internal event subscription handler.
//
// Gregale delivers the canonical event envelope as the request body. The
// handler logs only bounded, non-sensitive routing fields and leaves the
// payload available to application code for its own processing.

export async function handler(event, ctx) {
  let envelope = event?.body;
  if (typeof envelope === "string") {
    try {
      envelope = JSON.parse(envelope);
    } catch {
      throw new Error("Gregale event body was not valid JSON");
    }
  }
  if (!envelope || typeof envelope !== "object" || Array.isArray(envelope) ||
      ![envelope.id, envelope.source, envelope.type].every((value) => typeof value === "string" && value.length > 0)) {
    throw new Error("Gregale event envelope is missing id, source, or type");
  }

  ctx.log.info("event received", {
    event_id: envelope.id,
    source: envelope.source,
    type: envelope.type,
  });

  return {
    statusCode: 202,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ accepted: true }),
  };
}
