// queue-worker — a minimal push function for Gregale's durable queue.
//
// The platform passes the queued JSON payload as event.body. Keep processing
// idempotent: a message may be delivered again after a transient failure.

export async function handler(event, ctx) {
  let payload = event?.body ?? {};
  if (typeof payload === "string" && payload.length > 0) {
    try {
      payload = JSON.parse(payload);
    } catch {
      throw new Error("Gregale queue body was not valid JSON");
    }
  }

  // This reserved payload is a safe delivery check, never a business job.
  // Capability is a deployment opt-in; publication still needs API consent.
  if (payload && Object.hasOwn(payload, "gregale_queue_canary")) {
    let bindings;
    try {
      bindings = JSON.parse(process.env.GREGALE_QUEUE_CANARY_BINDINGS ?? "[]");
    } catch {
      throw new Error("Queue verification capability is invalid");
    }
    if (payload.gregale_queue_canary !== "v1"
        || Object.keys(payload).length !== 3
        || typeof payload.canary_id !== "string"
        || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(payload.canary_id)
        || typeof payload.binding !== "string"
        || !Array.isArray(bindings)
        || !bindings.every((name) => typeof name === "string" && /^[a-z][a-z0-9-]{0,62}$/.test(name))
        || !bindings.includes(payload.binding)) {
      throw new Error("Queue verification payload or capability is invalid");
    }
    ctx.log.info("queue verification completed", { invocation_id: ctx.invocation_id });
    return {
      statusCode: 200,
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ canary: "completed", canary_id: payload.canary_id, binding: payload.binding }),
    };
  }

  const jobId = payload && typeof payload.job_id === "string"
    ? payload.job_id
    : "unknown";
  ctx.log.info("queue message received", {
    invocation_id: ctx.invocation_id,
    job_id: jobId,
  });

  return {
    statusCode: 202,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ accepted: true, job_id: jobId }),
  };
}
