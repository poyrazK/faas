import { createServer } from 'node:http';
import { fileURLToPath } from 'node:url';
import { decodeDurableEntityHandlerRequest, DURABLE_ENTITY_HANDLER_PATH, DURABLE_ENTITY_MAX_REQUEST_BYTES } from './sdk.mjs';
import { reservationTransition } from './reservations.mjs';

export function createReservationServer({ webhookID }) {
  if (typeof webhookID !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u.test(webhookID)
    || webhookID === '00000000-0000-0000-0000-000000000000') {
    throw new TypeError('set CONFIRMATION_WEBHOOK_ID to a registered app webhook UUID');
  }
  return createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    if (req.method === 'GET' && req.url === '/healthz') { res.end('ok'); return; }
    if (req.method !== 'POST' || req.url !== DURABLE_ENTITY_HANDLER_PATH) { res.writeHead(404).end(); return; }
    try {
      const chunks = [];
      let bytes = 0;
      for await (const chunk of req) {
        bytes += chunk.length;
        if (bytes > DURABLE_ENTITY_MAX_REQUEST_BYTES) { res.writeHead(413).end('entity envelope too large'); return; }
        chunks.push(chunk);
      }
      const call = decodeDurableEntityHandlerRequest(Buffer.concat(chunks));
      const body = reservationTransition(call, webhookID);
      res.writeHead(200, { 'Content-Type': 'application/json' }).end(body);
    } catch {
      // Do not echo input, entity state, provider errors or webhook configuration.
      res.writeHead(422).end('invalid reservation transition');
    }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  createReservationServer({ webhookID: process.env.CONFIRMATION_WEBHOOK_ID })
    .listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
}
