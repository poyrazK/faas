export async function handler(event, ctx) {
  ctx.log.info('function invoked', { invocation_id: ctx.invocation_id });
  return {
    statusCode: 200,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ok: true, invocation_id: ctx.invocation_id }),
  };
}
