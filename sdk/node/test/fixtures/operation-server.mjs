import http from 'node:http';
import pg from 'pg';
import { operationRequestFromHeaders, withOperationTransaction, customerOperationRequestFromHeaders, withCustomerOperationTransaction } from '../../dist/index.js';

const pool = new pg.Pool({ connectionString: process.env.OPERATION_DATABASE_URL, max: 10 });
const mode = process.env.OPERATION_FAULT;
const customer = process.env.OPERATION_ADAPTER === 'customer';
const server = http.createServer(async (req, res) => {
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const request = (customer ? customerOperationRequestFromHeaders : operationRequestFromHeaders)(req.headers, req.method, req.url, Buffer.concat(chunks));
    const result = await (customer ? withCustomerOperationTransaction : withOperationTransaction)(pool, request, async tx => {
      await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
      if (mode === 'before-commit') {
        process.send({ phase: 'business-write' });
        await new Promise(() => {});
      }
      if (customer) return { file: 'ready.csv', order_id: 123, label: 'fulfilled π' };
      return {
        result: { order_id: 123, label: 'fulfilled π' },
        effects: [{ name: 'notify', webhook_id: process.env.OPERATION_WEBHOOK_ID, type: 'order.fulfilled', payload: { order_id: 123 } }],
      };
    });
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
