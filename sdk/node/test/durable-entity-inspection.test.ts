import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, inspectDurableEntity } from '../src/index.js';

test('inspection encodes selectors and rejects imprecise versions', async t => {
  let version = 1;
  let calls = 0;
  const key = 'document:/?+&雪';
  const server = createServer((req, res) => {
    calls++;
    assert.equal(req.method, 'GET');
    assert.equal(req.headers.authorization, 'Bearer token');
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(url.pathname, '/v1/apps/example/entities/inspect');
    assert.equal(url.searchParams.get('key'), key);
    assert.equal(url.searchParams.get('environment'), 'staging');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ entity: { account_id: 'account', app_id: 'app', environment_id: 'env', namespace: 'documents', key }, recovery_revision: 'a'.repeat(64), version, state_committed: true, alarm: { attempts: 0, exhausted: false }, outbox: { pending: 1, attempts: 0, exhausted: false, head_delivery: { status: 'unknown' } } }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const options = { slug: 'example', namespace: 'documents', key, environment: 'staging' };
  assert.equal((await inspectDurableEntity(options)).outbox.head_delivery?.status, 'unknown');
  await assert.rejects(inspectDurableEntity({ ...options, key: '' }), TypeError);
  assert.equal(calls, 1);
  version = Number.MAX_SAFE_INTEGER + 1;
  await assert.rejects(inspectDurableEntity(options), RangeError);
});
