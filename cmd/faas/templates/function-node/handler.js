// Function template for faas.
//
// Unlike app templates (express on :8080), functions expose a single
// exported async handler(event, ctx) that the node22 runner invokes
// directly. The CLI forces --runtime node22 --handler handler.handler
// when deploying this template so the wiring is automatic.

export async function handler(event, ctx) {
  // event.body is the parsed JSON request body (string for non-JSON).
  // ctx.log is the structured logger guest-init wires up. Surface
  // both so a smoke test sees something useful.
  ctx.log.info("function invoked", { event, invocation_id: ctx.invocation_id });
  return {
    statusCode: 200,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      ok: true,
      invocation_id: ctx.invocation_id,
      received: event,
    }),
  };
}