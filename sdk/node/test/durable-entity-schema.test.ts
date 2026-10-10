// ADR-939.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { decodeDurableEntitySchemaCall, decodeDurableEntityHandlerRequest, type DurableEntityStateSchema, DURABLE_ENTITY_MAX_TRANSITION_BYTES } from '../src/index.js';

type Counter = { count: number };
function counter(value: unknown): Counter {
  if (value === null || typeof value !== 'object' || !('count' in value) || !Number.isSafeInteger(value.count) || (value.count as number) < 0) throw new TypeError('invalid counter');
  return { count: value.count as number };
}
function schema(): DurableEntityStateSchema<Counter> {
  return { version: 2, initialState: () => ({ count: 0 }), decodeState: counter, migrations: new Map<number, (value: unknown) => unknown>([[1, value => {
    if (value === null || typeof value !== 'object' || !('total' in value) || !Number.isSafeInteger(value.total)) throw new TypeError('invalid old counter');
    return { count: value.total };
  }]]) };
}
function fixture(data?: unknown) {
  const request = decodeDurableEntityHandlerRequest(readFileSync('../go/testdata/durable-entity-handler-request.json'));
  if (data !== undefined) { request.state.data = data; request.state.version = 7; }
  return request;
}
const decodePayload = (value: unknown) => value;

test('upgrade remains local and is wrapped with normal alarm and outbox transition', () => {
  const request = fixture({ schema_version: 1, data: { total: 4 } });
  request.state.alarm_at = '2026-10-10T12:00:00Z';
  const original = JSON.stringify(request);
  const config = schema();
  const call = decodeDurableEntitySchemaCall(original, config, decodePayload);
  assert.equal(call.version, 7); assert.equal(call.storedSchemaVersion, 1); assert.equal(call.migrated, true);
  assert.deepEqual(call.state, { count: 4 }); assert.equal(JSON.stringify(request), original);
  config.version = 99;
  const output = JSON.parse(call.transition({ count: 5 }, { count: 5 }).webhook('6dd283da-3c14-40de-9d47-bb71fb35be9a', 'counter.updated', { count: 5 }).encode());
  assert.deepEqual(output.data, { schema_version: 2, data: { count: 5 } });
  assert.equal(output.alarm_at, request.state.alarm_at); assert.equal(output.outbox.length, 1);
  request.state.data = output.data; request.state.version++;
  const current = schema(); current.migrations = new Map(); current.initialState = () => { throw new Error('must not initialize committed state'); };
  const decoded = decodeDurableEntitySchemaCall(JSON.stringify(request), current, decodePayload);
  assert.equal(decoded.migrated, false); assert.equal(decoded.state.count, 5);
  assert.throws(() => decoded.transition({ count: -1 }, null).encode());
});

test('rollback, malformed envelopes and incomplete chains fail before callbacks', () => {
  for (const data of [{ schema_version: 3, data: { count: 4 } }, { schema_version: 1 }, { schema_version: 0, data: {} }, { schema_version: 1, data: {}, extra: true }, { total: 4 }]) {
    assert.throws(() => decodeDurableEntitySchemaCall(JSON.stringify(fixture(data)), schema(), decodePayload));
  }
  const config = schema(); config.version = 3;
  let calls = 0; config.migrations = new Map<number, (value: unknown) => unknown>([[1, value => { calls++; return value; }]]);
  assert.throws(() => decodeDurableEntitySchemaCall(JSON.stringify(fixture({ schema_version: 1, data: { total: 4 } })), config, decodePayload));
  assert.equal(calls, 0);
});

test('legacy adoption is explicit and initial state starts at the current schema', () => {
  const initial = decodeDurableEntitySchemaCall(JSON.stringify(fixture()), schema(), decodePayload);
  assert.equal(initial.migrated, false); assert.equal(initial.storedSchemaVersion, 2);
  const config = schema(); config.legacyVersion = 1;
  const adopted = decodeDurableEntitySchemaCall(JSON.stringify(fixture({ total: 6 })), config, decodePayload);
  assert.equal(adopted.migrated, true); assert.equal(adopted.state.count, 6);
  config.legacyVersion = 2;
  const wrapped = decodeDurableEntitySchemaCall(JSON.stringify(fixture({ count: 6 })), config, decodePayload);
  assert.equal(wrapped.migrated, true);
  assert.deepEqual(JSON.parse(wrapped.transition(wrapped.state, null).encode()).data, { schema_version: 2, data: { count: 6 } });
});

test('failed, async, oversized or invalid transformations never produce a call', () => {
  for (const migrate of [() => { throw new Error('failed upgrade'); }, () => Promise.resolve({ count: 1 }), () => 'x'.repeat(DURABLE_ENTITY_MAX_TRANSITION_BYTES), () => ({ count: -1 }), () => undefined]) {
    const config = schema(); config.migrations = new Map([[1, migrate]]);
    assert.throws(() => decodeDurableEntitySchemaCall(JSON.stringify(fixture({ schema_version: 1, data: { total: 4 } })), config, decodePayload));
  }
});

test('migration callbacks cannot redirect the captured chain', () => {
  const config = schema(); config.version = 3;
  const original = config.migrations.get(1)!;
  const steps = new Map<number, (value: unknown) => unknown>([
    [1, value => { steps.delete(2); return original(value); }],
    [2, value => ({ count: counter(value).count + 1 })],
  ]);
  config.migrations = steps;
  const call = decodeDurableEntitySchemaCall(JSON.stringify(fixture({ schema_version: 1, data: { total: 4 } })), config, decodePayload);
  assert.equal(call.state.count, 5);
  assert.deepEqual(JSON.parse(call.transition(call.state, null).encode()).data, { schema_version: 3, data: { count: 5 } });
});
