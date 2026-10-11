import { readFile } from 'node:fs/promises';
import pg from 'pg';
import { operationRequestFromHeaders, withOperationTransaction } from '../../dist/index.js';

const fixture = JSON.parse(await readFile(process.env.OPERATION_REQUEST_FILE, 'utf8'));
const request = operationRequestFromHeaders(fixture.headers, fixture.method, fixture.path, Buffer.from(fixture.body_base64, 'base64'));
const pool = new pg.Pool({ connectionString: process.env.OPERATION_CROSS_DATABASE_URL });
try {
  const result = await withOperationTransaction(pool, request, async tx => {
    if (process.env.OPERATION_MODE !== 'write') throw new Error('cross SDK receipt callback reran');
    await tx.query('UPDATE business.counter SET total=total+1 WHERE id=1');
    return { result: { value: 42, label: 'π <>&' }, effects: [{ name: 'notify', webhook_id: 'cccbbbaa-3333-4333-8333-cccccccccccc', type: 'order.fulfilled', payload: { order_id: 123 } }] };
  });
  process.stdout.write(JSON.stringify(result));
} finally { await pool.end(); }
