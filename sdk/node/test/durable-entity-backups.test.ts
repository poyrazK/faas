import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, listDurableEntityBackups, getDurableEntityBackup, previewDurableEntityRestore } from '../src/index.js';

test('backup reads preserve selectors and preview sends no restore mutation', async t => {
  let calls = 0;
  const exported = { format: 1 as const, entity: { account_id: 'a', app_id: 'b', environment_id: 'e', namespace: 'documents', key: 'doc' }, version: 1, data: {}, checksum: 'a'.repeat(64) };
  const body = { namespace: 'documents', key: 'doc', request_id: 'preview', expected_version: 42, export: exported };
  const server = createServer(async (req, res) => {
    calls++; const url = new URL(req.url!, 'http://localhost');
    res.setHeader('content-type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(url.searchParams.get('key'), 'doc/?雪');
      if (url.pathname.endsWith('/get')) {
        assert.equal(url.searchParams.get('backup_id'), '20261009T120000Z');
        res.end(JSON.stringify({ captured_at: '2026-10-09T12:00:00Z', export: exported })); return;
      }
      assert.equal(url.searchParams.get('cursor'), 'opaque+/=');
      res.end(JSON.stringify({ items: [{ id: '20261009T120000Z', captured_at: '2026-10-09T12:00:00Z', version: 1 }] })); return;
    }
    assert.equal(url.pathname, '/v1/apps/example/entities/restore/preview');
    const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(Buffer.from(chunk));
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), body);
    res.end(JSON.stringify({ current_version: 42, source_version: 1, expected_version_matches: true, schema_relation: 'unknown', compatibility: 'unverified', alarm_pending: false, outbox_pending: 0, alarm_exhausted: false, outbox_exhausted: false }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(e => e ? reject(e) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const selectors = { slug: 'example', namespace: 'documents', key: 'doc/?雪' };
  assert.equal((await listDurableEntityBackups({ ...selectors, cursor: 'opaque+/=' })).items.length, 1);
  assert.equal((await getDurableEntityBackup({ ...selectors, backupId: '20261009T120000Z' })).export.version, 1);
  assert.equal((await previewDurableEntityRestore({ slug: 'example', requestBody: body })).compatibility, 'unverified');
  await assert.rejects(previewDurableEntityRestore({ slug: 'example', requestBody: { ...body, expected_version: Number.MAX_SAFE_INTEGER + 1 } }), RangeError);
  assert.equal(calls, 3);
});
