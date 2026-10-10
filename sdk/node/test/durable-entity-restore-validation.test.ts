import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, decodeDurableEntityRestoreValidationRequest, encodeDurableEntityRestoreValidation, validateDurableEntityRestore } from '../src/index.js';

const envelope = { protocol_version: 1, event: 'validate_restore', entity: { account_id: 'a', app_id: 'b', namespace: 'counters', key: 'counter' }, request_id: 'restore', deployment_id: 'deployment', expected_version: 2, source_version: 1, candidate: { schema_version: 2, data: { count: 1 } } };

test('pure guest validator emits only a boolean and rejects unsafe versions', () => {
  const body = JSON.stringify(envelope);
  assert.equal(decodeDurableEntityRestoreValidationRequest(body).expected_version, 2);
  assert.deepEqual(JSON.parse(encodeDurableEntityRestoreValidation(body, () => true)), { protocol_version: 1, valid: true });
  assert.deepEqual(JSON.parse(encodeDurableEntityRestoreValidation(body, () => { throw new Error('private validation detail'); })), { protocol_version: 1, valid: false });
  // A mistaken asynchronous callback must never return an affirmative verdict.
  assert.deepEqual(JSON.parse(encodeDurableEntityRestoreValidation(body, (() => Promise.resolve(true)) as unknown as () => boolean)), { protocol_version: 1, valid: false });
  for (const changed of [{ ...envelope, event: 'invoke' }, { ...envelope, outbox: [] }, { ...envelope, expected_version: Number.MAX_SAFE_INTEGER + 1 }]) {
    assert.throws(() => encodeDurableEntityRestoreValidation(JSON.stringify(changed), () => { assert.fail('malformed input invoked validator'); }), TypeError);
  }
});

test('owner validation SDK preserves identity and returns the deployment pin', async t => {
  let calls = 0;
  const requestBody = { namespace: 'counters', key: 'counter', request_id: 'stable', expected_version: 2, export: { format: 1 as const, entity: { account_id: 'a', app_id: 'b', environment_id: 'e', namespace: 'counters', key: 'counter' }, version: 1, data: envelope.candidate, checksum: 'a'.repeat(64) } };
  const server = createServer(async (req, res) => {
    calls++; assert.equal(req.url, '/v1/apps/example/entities/restore/validate'); assert.equal(req.method, 'POST');
    const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(Buffer.from(chunk));
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), requestBody);
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ valid: true, deployment_id: 'deployment', expected_version: 2, source_version: 1 }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(e => e ? reject(e) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.equal((await validateDurableEntityRestore({ slug: 'example', requestBody })).deployment_id, 'deployment');
  await assert.rejects(validateDurableEntityRestore({ slug: 'example', requestBody: { ...requestBody, request_id: '' } }), TypeError);
  assert.equal(calls, 1);
});
