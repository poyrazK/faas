import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, retryDurableEntity } from '../src/index.js';

test('recovery preserves comparison fields and rejects unsafe or incomplete requests', async t => {
  let calls = 0;
  const requestBody = {
    namespace: 'documents', key: 'document:123', target: 'outbox' as const,
    expected_version: 42, expected_recovery_revision: 'a'.repeat(64),
    head_id: '6dd283da-3c14-40de-9d47-bb71fb35be9a',
  };
  const server = createServer(async (req, res) => {
    calls++;
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/v1/apps/example/entities/retry');
    assert.equal(req.headers.authorization, 'Bearer token');
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.from(chunk));
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), requestBody);
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ version: 42, target: 'outbox', rearmed: true }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const options = { slug: 'example', requestBody };
  assert.equal((await retryDurableEntity(options)).rearmed, true);
  for (const invalid of [
    { ...requestBody, expected_recovery_revision: '' },
    { ...requestBody, expected_version: Number.MAX_SAFE_INTEGER + 1 },
    { ...requestBody, alarm_at: '2026-10-09T12:00:00Z' },
  ]) await assert.rejects(retryDurableEntity({ ...options, requestBody: invalid }), TypeError);
  assert.equal(calls, 1);
});
