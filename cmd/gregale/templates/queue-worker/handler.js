// queue-worker — a minimal push worker for Gregale's durable queue.
//
// The platform passes the queued JSON payload as event.body. Keep processing
// idempotent: a message may be delivered again after a transient failure.

export async function handler(event, ctx) {
  let payload = {};
  if (event && typeof event.body === "string" && event.body.length > 0) {
    try {
      payload = JSON.parse(event.body);
    } catch {
      throw new Error("Gregale queue body was not valid JSON");
    }
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
