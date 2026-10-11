import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, exportDurableEntity, restoreDurableEntity } from '../src/index.js';

test('export and restore retain the complete envelope and stable identity', async t => {
  let calls = 0;
  const exported = { format: 1 as const, entity: { account_id: 'a', app_id: 'b', environment_id: 'e', namespace: 'documents', key: 'doc' }, version: 1, data: { count: 1 }, checksum: 'a'.repeat(64) };
  const body = { namespace: 'documents', key: 'doc', request_id: 'stable', expected_version: 42, export: exported };
  const server = createServer(async (req, res) => {
    calls++; assert.equal(req.headers.authorization, 'Bearer token');
    res.setHeader('content-type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(new URL(req.url!, 'http://localhost').pathname, '/v1/apps/example/entities/export');
      res.end(JSON.stringify(exported)); return;
    }
    assert.equal(req.url, '/v1/apps/example/entities/restore');
    const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(Buffer.from(chunk));
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), body);
    res.end(JSON.stringify({ version: 43, replayed: calls > 2 }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(e => e ? reject(e) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await exportDurableEntity({ slug: 'example', namespace: 'documents', key: 'doc' }), exported);
  assert.equal((await restoreDurableEntity({ slug: 'example', requestBody: body })).replayed, false);
  assert.equal((await restoreDurableEntity({ slug: 'example', requestBody: body })).replayed, true);
  await assert.rejects(restoreDurableEntity({ slug: 'example', requestBody: { ...body, expected_version: Number.MAX_SAFE_INTEGER + 1 } }), TypeError);
  await assert.rejects(restoreDurableEntity({ slug: 'example', requestBody: { ...body, request_id: '' } }), TypeError);
  assert.equal(calls, 3);
});
