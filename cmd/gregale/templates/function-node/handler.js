// Function template for gregale.
//
// Unlike app templates (express on :8080), functions expose a single
// exported async handler(event, ctx) that the node22 runner invokes
// directly. The CLI forces --runtime node22 --handler handler.handler
// when deploying this template so the wiring is automatic.

export async function handler(event, ctx) {
  // Never log or echo the request event: it can contain cookies, API keys,
  // authorization headers, and customer payloads. Keep the starter useful
  // with a bounded, non-secret invocation marker instead.
  ctx.log.info("function invoked", { invocation_id: ctx.invocation_id });
  return {
    statusCode: 200,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      ok: true,
      invocation_id: ctx.invocation_id,
    }),
  };
}
