// Synthetic, bounded instrumentation for the opt-in staging journey only.
import { randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';

export function installJourneyTools(runtime, z, wait = delay) {
  const calls = new Map();
  const record = nonce => {
    if (!/^e2ej-[a-z0-9-]{1,100}$/.test(nonce)) throw new Error('Invalid journey nonce');
    if (!calls.has(nonce)) {
      if (calls.size >= 128) throw new Error('Journey diagnostic capacity reached');
      calls.set(nonce, { run: randomUUID(), count: 0 });
    }
    const entry = calls.get(nonce);
    entry.count++;
    return { nonce, count: entry.count };
  };
  const definition = {
    description: 'Record one synthetic staging-journey call.',
    inputSchema: z.object({ nonce: z.string().regex(/^e2ej-[a-z0-9-]{1,100}$/) }),
    outputSchema: z.object({ nonce: z.string(), count: z.number().int() }),
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: false },
  };
  const result = value => ({ content: [{ type: 'text', text: 'Journey call recorded.' }], structuredContent: value });
  runtime.registerTool('journey_echo', definition, async ({ nonce }, _ctx, { authInfo } = {}) => {
    const out = result(record(nonce));
    // Deliberately reflect the synthetic application token only for private
    // acceptance calls. The native worker must redact it before journaling.
    if (nonce.endsWith('-reflect') && authInfo?.token) out.content.push({ type: 'text', text: authInfo.token });
    return out;
  });
  runtime.registerTool('journey_wait', { ...definition, description: 'Record one synthetic call, then wait for cancellation.' }, async ({ nonce }, ctx) => {
    const value = record(nonce);
    // Cancellation stops the wait; the already-recorded call remains observable.
    await wait(60_000, undefined, { signal: ctx.mcpReq.signal });
    return result(value);
  });
  runtime.app.get('/journey/calls/:nonce', (req, res) => {
    res.set('Cache-Control', 'no-store');
    const entry = calls.get(req.params.nonce);
    return entry ? res.json({ nonce: req.params.nonce, ...entry }) : res.status(404).json({ code: 'not_seen' });
  });
}
