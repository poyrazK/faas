import http from 'node:http';
import pg from 'pg';
import { operationRequestFromHeaders, withOperationTransaction, GregaleOperations } from '../../dist/index.js';

const pool = new pg.Pool({ connectionString: process.env.OPERATION_DATABASE_URL, max: 10 });
const mode = process.env.OPERATION_FAULT;
const customer = process.env.CUSTOMER_TRANSACTION === '1';
const runtime = new GregaleOperations({ apiURL: 'https://api.gregale.test', identityEndpoint: 'http://127.0.0.1/identity' });
const server = http.createServer(async (req, res) => {
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks);
    const callback = async tx => {
      await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
      if (mode === 'before-commit') {
        process.send({ phase: 'business-write' });
        await new Promise(() => {});
      }
      if (customer) return { order_id: 123, label: 'fulfilled π' };
      return {
        result: { order_id: 123, label: 'fulfilled π' },
        effects: [{ name: 'notify', webhook_id: process.env.OPERATION_WEBHOOK_ID, type: 'order.fulfilled', payload: { order_id: 123 } }],
      };
    };
    const result = customer
      ? await runtime.transaction({ headers: req.headers, method: req.method, path: req.url, body }, pool, callback)
      : await withOperationTransaction(pool, operationRequestFromHeaders(req.headers, req.method, req.url, body), callback);
    if (mode === 'after-commit') {
      process.send({ phase: 'committed', body: result.body });
      await new Promise(() => {});
    }
    res.writeHead(200, { 'content-type': 'application/json', 'x-replayed': String(result.replayed) });
    res.end(result.body);
  } catch (error) {
    res.writeHead(error.code === 'operation_receipt_conflict' ? 409 : 500);
    res.end(error.message);
  }
});
server.listen(0, '127.0.0.1', () => process.send({ phase: 'ready', port: server.address().port }));
