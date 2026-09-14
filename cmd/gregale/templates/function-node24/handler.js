// Function template for gregale (node24).
//
// Mirrors cmd/gregale/templates/function-node/handler.js (node22) —
// the only differences are the runtime id (node24) and the handler
// filename (/app/node24.js inside the microVM, set by imaged's
// function-layer manifest). The CLI forces --runtime node24
// --handler handler.handler when deploying this template so the
// wiring is automatic; the `handler.handler` value in --handler is
// the customer's tarball stem (handler.js — the runner resolves the
// underlying filename per runtime).

export async function handler(event, ctx) {
  // Request events can contain cookies, API keys, authorization headers, and
  // customer payloads. Keep logs and the default response metadata-only.
  ctx.log.info("function invoked", { invocation_id: ctx.invocation_id, runtime: process.env.FAAS_RUNTIME });
  return {
    statusCode: 200,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      ok: true,
      invocation_id: ctx.invocation_id,
      runtime: process.env.FAAS_RUNTIME,
    }),
  };
}
