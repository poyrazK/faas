import test from 'node:test';
import assert from 'node:assert/strict';
import { installJourneyTools } from './journey-tools.mjs';

function fixture(wait) {
  const tools = new Map();
  let observe;
  const runtime = { registerTool(name, definition, handler) { tools.set(name, handler); }, app: { get(path, handler) { observe = handler; } } };
  const schema = { regex() { return this; }, int() { return this; } };
  const z = { object: value => value, string: () => schema, number: () => schema };
  installJourneyTools(runtime, z, wait);
  return {
    tools,
    read(nonce) {
      const out = { statusCode: 200, headers: {} };
      const res = { set(k, v) { out.headers[k] = v; return this; }, status(n) { out.statusCode = n; return this; }, json(body) { out.body = body; return out; } };
      return observe({ params: { nonce } }, res);
    },
  };
}

test('observer detects repeated calls and distinguishes fresh runtimes', async () => {
  const f = fixture();
  assert.equal(f.read('e2ej-abc').statusCode, 404);
  await f.tools.get('journey_echo')({ nonce: 'e2ej-abc' });
  const first = f.read('e2ej-abc');
  assert.equal(first.body.count, 1);
  assert.equal(first.headers['Cache-Control'], 'no-store');
  await f.tools.get('journey_echo')({ nonce: 'e2ej-abc' });
  assert.equal(f.read('e2ej-abc').body.count, 2);
  assert.equal(f.read('e2ej-abc').body.run, first.body.run);
  const other = fixture();
  await other.tools.get('journey_echo')({ nonce: 'e2ej-abc' });
  assert.notEqual(other.read('e2ej-abc').body.run, first.body.run);
});

test('cancellation preserves the recorded effect', async () => {
  let started;
  const entered = new Promise(resolve => { started = resolve; });
  const f = fixture((_, __, { signal }) => new Promise((resolve, reject) => {
    signal.addEventListener('abort', () => reject(signal.reason), { once: true });
    started();
  }));
  const controller = new AbortController();
  const pending = f.tools.get('journey_wait')({ nonce: 'e2ej-cancel' }, { mcpReq: { signal: controller.signal } });
  await entered;
  assert.equal(f.read('e2ej-cancel').body.count, 1);
  controller.abort(new Error('cancelled'));
  await assert.rejects(pending, /cancelled/);
  assert.equal(f.read('e2ej-cancel').body.count, 1);
});

test('private acceptance reflects only explicitly selected synthetic calls', async () => {
  const f = fixture();
  const echo = f.tools.get('journey_echo');
  const auth = { authInfo: { token: 'synthetic-provider-token' } };
  const ordinary = await echo({ nonce: 'e2ej-normal' }, {}, auth);
  assert.ok(!JSON.stringify(ordinary).includes(auth.authInfo.token));
  const reflected = await echo({ nonce: 'e2ej-private-reflect' }, {}, auth);
  assert.equal(reflected.content[1].text, auth.authInfo.token);
  assert.ok(!JSON.stringify(f.read('e2ej-private-reflect')).includes(auth.authInfo.token));
});

test('diagnostics are bounded and reject invalid nonces', async () => {
  const f = fixture();
  const echo = f.tools.get('journey_echo');
  await assert.rejects(echo({ nonce: 'customer-input' }), /Invalid journey nonce/);
  for (let i = 0; i < 128; i++) await echo({ nonce: `e2ej-${i}` });
  await assert.rejects(echo({ nonce: 'e2ej-overflow' }), /capacity/);
  assert.equal(f.read('e2ej-overflow').statusCode, 404);
  await echo({ nonce: 'e2ej-0' });
  assert.equal(f.read('e2ej-0').body.count, 2);
});
