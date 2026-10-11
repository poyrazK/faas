import http from 'node:http';
import {fileURLToPath} from 'node:url';
import pg from 'pg';
import {GregaleOperations} from './sdk.mjs';
import {fulfillOrder, OrderRequestError} from './orders.mjs';

export function createOrderServer({runtime, pool}) {
  return http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('Content-Type', 'application/json');
    try {
      if (req.method === 'GET' && req.url === '/healthz') { res.end('{"status":"ok"}'); return; }
      if (req.method !== 'POST' || req.url !== '/orders/fulfill') { res.statusCode = 404; res.end('{"error":"Not found"}'); return; }
      const chunks = []; let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 1024) throw new OrderRequestError(413, 'Order input too large');
        chunks.push(chunk);
      }
      const receipt = await fulfillOrder({runtime, pool, request: {headers: req.headers, method: req.method, path: req.url, body: Buffer.concat(chunks)}});
      res.end(receipt.body);
    } catch (error) {
      res.statusCode = error instanceof OrderRequestError ? error.status : 500;
      res.end(JSON.stringify({error: error instanceof OrderRequestError ? error.message : 'Order request failed'}));
    }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (!process.env.APPLICATION_DATABASE_URL) throw new Error('Set APPLICATION_DATABASE_URL');
  const pool = new pg.Pool({connectionString: process.env.APPLICATION_DATABASE_URL});
  const runtime = new GregaleOperations({apiURL: process.env.GREGALE_API_URL});
  const server = createOrderServer({runtime, pool});
  // Gregale guest hosting must set HOST=0.0.0.0 and use its trusted guest listener.
  server.listen(Number(process.env.PORT ?? 8080), process.env.HOST ?? '127.0.0.1');
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close(() => pool.end()));
}
