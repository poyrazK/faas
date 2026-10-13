// event-worker — a minimal internal event subscription handler.
//
// Gregale delivers the canonical event envelope as the request body. The
// handler logs only bounded, non-sensitive routing fields and leaves the
// payload available to application code for its own processing.

// A malformed envelope is the sender's error, not a handler failure: answer
// 400 so it is not retried and does not count as an app 5xx.
function badRequest(detail) {
  return {
    statusCode: 400,
    headers: { "content-type": "application/problem+json" },
    body: JSON.stringify({ title: "Invalid event envelope", status: 400, detail }),
  };
}

export async function handler(event, ctx) {
  let envelope = event?.body;
  if (typeof envelope === "string") {
    try {
      envelope = JSON.parse(envelope);
    } catch {
      return badRequest("Gregale event body was not valid JSON");
    }
  }
  if (!envelope || typeof envelope !== "object" || Array.isArray(envelope) ||
      ![envelope.id, envelope.source, envelope.type].every((value) => typeof value === "string" && value.length > 0)) {
    return badRequest("Gregale event envelope is missing id, source, or type");
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
