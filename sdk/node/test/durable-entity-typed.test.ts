// ADR-848.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import {
  FaaSClient, decodeDurableEntityCall, durableEntityHandle, DurableEntityResultDecodeError,
  decodeDurableEntityHandlerRequest,
} from '../src/index.js';

function fixture() { return decodeDurableEntityHandlerRequest(readFileSync('../go/testdata/durable-entity-handler-request.json')); }
function decodeCounter(value: unknown): { count: number } {
  if (value === null || typeof value !== 'object' || !('count' in value) || !Number.isSafeInteger(value.count)) throw new TypeError('invalid counter state');
  return { count: value.count as number };
}
const options = { initialState: () => ({ count: 7 }), decodeState: decodeCounter, decodePayload: (value: unknown) => value };
const hook = '6dd283da-3c14-40de-9d47-bb71fb35be9a';

test('typed calls initialize only version zero and preserve, replace or clear alarms', () => {
  const request = fixture();
  request.state.alarm_at = '2026-10-10T12:00:00Z';
  const initial = { count: 7 };
  const call = decodeDurableEntityCall(JSON.stringify(request), { ...options, initialState: () => initial });
  assert.equal(call.state.count, 7);
  call.state.count++;
  assert.equal(initial.count, 7);
  const builder = call.transition(call.state, { count: 8 });
  assert.equal(JSON.parse(builder.encode()).alarm_at, request.state.alarm_at);
  assert.equal(Object.hasOwn(JSON.parse(builder.clearAlarm().encode()), 'alarm_at'), false);
  const later = new Date('2026-10-11T12:00:00Z');
  assert.equal(JSON.parse(builder.scheduleAlarm(later).encode()).alarm_at, later.toISOString());
  request.state.version = 3;
  request.state.data = { count: 'invalid' };
  assert.throws(() => decodeDurableEntityCall(JSON.stringify(request), options), TypeError);
  request.state.data = { count: 9 };
  const committed = decodeDurableEntityCall(JSON.stringify(request), { ...options, initialState: () => { throw new Error('must not initialize committed state'); } });
  assert.equal(committed.state.count, 9);
});

test('rejected operations poison builders and intent payloads are captured', () => {
  const request = fixture();
  const call = decodeDurableEntityCall(JSON.stringify(request), options);
  const payload = { count: 1 };
  const builder = call.transition(call.state, null).webhook(hook, 'counter.updated', payload);
  payload.count = 99;
  assert.equal(JSON.parse(builder.encode()).outbox[0].payload.count, 1);
  assert.throws(() => builder.webhook('invalid', 'counter.updated', payload));
  assert.throws(() => builder.encode());
  const alarm = call.transition(call.state, null);
  assert.throws(() => alarm.scheduleAlarm(new Date('invalid')));
  assert.throws(() => alarm.encode());
  request.protocol_version = 1; delete request.limits;
  const legacy = decodeDurableEntityCall(JSON.stringify(request), options);
  assert.throws(() => legacy.transition(legacy.state, null).webhook(hook, 'counter.updated', payload));
});

test('handles bind scope, retain commit metadata on decoder failure and require explicit replay identity', async t => {
  let calls = 0;
  const scope = { slug: 'example', namespace: 'documents', key: 'document:/?+&雪', environment: 'staging', platformTenantId: 'tenant' };
  const expected = { ...scope };
  const server = createServer(async (req, res) => {
    calls++;
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.headers.authorization, 'Bearer token');
    res.setHeader('content-type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(url.searchParams.get('key'), expected.key);
      assert.equal(url.searchParams.get('environment'), expected.environment);
      assert.equal(url.searchParams.get('platform_tenant_id'), expected.platformTenantId);
      res.end(JSON.stringify({ version: 1 })); return;
    }
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.from(chunk));
    const body = JSON.parse(Buffer.concat(chunks).toString());
    assert.equal(body.key, expected.key);
    assert.equal(body.environment, expected.environment);
    assert.equal(body.platform_tenant_id, expected.platformTenantId);
    if (url.pathname.endsWith('/retry')) {
      assert.equal(body.expected_recovery_revision, 'a'.repeat(64));
      res.end(JSON.stringify({ version: 1, target: 'outbox', rearmed: true })); return;
    }
    assert.equal(body.request_id, 'stable');
    assert.deepEqual(body.payload, { delta: 1 });
    res.end(JSON.stringify({ value: 'invalid-result', version: 42, replayed: true }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const handle = durableEntityHandle<{ delta: number }, { count: number }>(scope, decodeCounter);
  scope.key = 'changed';
  const payload = { delta: 1 };
  const invocation = handle.invoke('stable', payload);
  payload.delta = 99;
  await assert.rejects(invocation, (error: unknown) => error instanceof DurableEntityResultDecodeError && error.version === 42 && error.replayed);
  await assert.rejects(handle.invoke('', { delta: 1 }), TypeError);
  await assert.rejects(handle.invoke('stable', { delta: Number.NaN }), TypeError);
  assert.equal(calls, 1);
  await handle.inspect();
  await handle.retry({ target: 'outbox', expected_version: 1, expected_recovery_revision: 'a'.repeat(64), head_id: hook });
  assert.equal(calls, 3);
});
