import assert from 'node:assert/strict';
import test from 'node:test';
import {defineDurableEntity, createDurableEntityRegistry} from '../src/durable-entity-runtime.js';
import {DurableEntitySimulator, LocalEntityFaultError} from '../src/durable-entity-testing.js';

function state(value: unknown): {count: number} {
  if (value === null || typeof value !== 'object' || !('count' in value) || !Number.isSafeInteger(value.count) || (value.count as number) < 0) throw new TypeError('invalid count');
  return {count: value.count as number};
}
function counter(onCall = () => {}) {
  return defineDurableEntity({namespace: 'counters', schema: {version: 1, initialState: () => ({count: 0}), decodeState: state, migrations: new Map()},
    decodePayload(value: unknown) {
      if (value === null || typeof value !== 'object' || !('delta' in value) || !Number.isSafeInteger(value.delta)) throw new TypeError('invalid delta');
      return {delta: value.delta as number};
    },
    handle(call) { onCall(); const next = state({count: call.state.count + call.payload.delta}); return call.transition(next, next); },
  });
}
const input = (id: string, delta = 1, key = 'one') => ({namespace: 'counters', key, requestId: id, payload: {delta}});

test('registration rejects duplicate/unknown namespaces before running business code', async () => {
  let calls = 0; const definition = counter(() => {calls++;});
  assert.throws(() => createDurableEntityRegistry([definition, definition]));
  const registry = createDurableEntityRegistry([definition]);
  registry.describe()[0]!.namespace = 'changed';
  const simulator = new DurableEntitySimulator(registry);
  await assert.rejects(simulator.invoke({...input('unknown'), namespace: 'missing'}));
  assert.equal(calls, 0);
  assert.equal((await simulator.invoke(input('ok'))).version, 1);
});

test('concurrent calls serialize per entity; replay, payload capture and key isolation preserve results', async () => {
  let calls = 0;
  const simulator = new DurableEntitySimulator(createDurableEntityRegistry([counter(() => {calls++;})]));
  const payload = {delta: 1};
  const pending = simulator.invoke({...input('one'), payload}); payload.delta = 999;
  const results = await Promise.all([pending, simulator.invoke(input('two')), simulator.invoke(input('one'))]);
  assert.deepEqual(results.map(result => result.version), [1, 2, 1]);
  assert.equal(results[2]!.replayed, true);
  assert.deepEqual(results[0]!.value, {count: 1});
  assert.equal(calls, 2);
  await assert.rejects(simulator.invoke(input('one', 2)), /different payload/);
  await assert.rejects(simulator.invoke(input('invalid', -100)));
  assert.equal(simulator.inspect('counters', 'one')!.version, 2);
  assert.equal((await simulator.invoke(input('one', 7, 'another'))).version, 1);
  const observation = simulator.inspect('counters', 'one')!;
  (observation.data as {data: {count: number}}).data.count = 100;
  assert.deepEqual(simulator.inspect('counters', 'one')!.data, {schema_version: 1, data: {count: 2}});
});

test('pre-commit failure reruns computation; lost acknowledgement and checkpoint restart replay without rerunning', async () => {
  let calls = 0;
  const registry = createDurableEntityRegistry([counter(() => {calls++;})]);
  const simulator = new DurableEntitySimulator(registry);
  await assert.rejects(simulator.invoke(input('before'), {fault: 'before-commit'}), LocalEntityFaultError);
  assert.equal(simulator.inspect('counters', 'one'), undefined);
  await simulator.invoke(input('before'));
  assert.equal(calls, 2);
  await assert.rejects(simulator.invoke(input('lost'), {fault: 'after-commit'}), error => error instanceof LocalEntityFaultError && error.phase === 'after-commit');
  const restarted = new DurableEntitySimulator(registry);
  restarted.restore(simulator.checkpoint());
  const result = await restarted.invoke(input('lost'));
  assert.deepEqual(result, {value: {count: 2}, version: 2, replayed: true});
  assert.equal(calls, 3);
  const malformed = JSON.parse(simulator.checkpoint()); malformed.entities[0].receipts[0].version = 999;
  const empty = new DurableEntitySimulator(registry);
  assert.throws(() => empty.restore(JSON.stringify(malformed)));
  assert.equal(empty.inspect('counters', 'one'), undefined);
});

test('virtual alarms consume deadlines and committed outgoing intents are recorded once without sending', async () => {
  const definition = defineDurableEntity({namespace: 'sessions', schema: {version: 1, initialState: () => ({count: 0}), decodeState: state, migrations: new Map()},
    decodePayload: (_value: unknown, event: 'invoke' | 'alarm') => ({event}),
    handle(call) {
      const next = {count: call.state.count + 1}; const transition = call.transition(next, next);
      return call.event === 'alarm' ? transition.clearAlarm().webhook('44444444-4444-4444-8444-444444444444', 'done', next)
        : transition.scheduleAlarm('2000-01-01T00:01:00Z');
    },
  });
  const simulator = new DurableEntitySimulator(createDurableEntityRegistry([definition]));
  const request = {namespace: 'sessions', key: 'session', requestId: 'start', payload: null};
  await simulator.invoke(request);
  assert.deepEqual(await simulator.runDueAlarms(), []);
  simulator.advance(60000);
  assert.equal((await simulator.runDueAlarms()).length, 1);
  assert.deepEqual(await simulator.runDueAlarms(), []);
  assert.equal(simulator.inspect('sessions', 'session')!.alarm_at, undefined);
  assert.equal(simulator.pendingIntents('sessions', 'session').length, 1);
  assert.equal((await simulator.invoke(request)).replayed, true);
  assert.equal(simulator.pendingIntents('sessions', 'session').length, 1);
  await assert.rejects(simulator.invoke({...request, requestId: '__gregale_alarm/1'}));
  const legacy = new DurableEntitySimulator(createDurableEntityRegistry([definition]), {protocolVersion: 1});
  await legacy.invoke(request); legacy.advance(60000);
  await assert.rejects(legacy.runDueAlarms());
  assert.equal(legacy.inspect('sessions', 'session')!.version, 1);
});

test('checkpoint restart runs explicit schema upgrades with the next transition; restore validation never migrates', async () => {
  const old = new DurableEntitySimulator(createDurableEntityRegistry([counter()])); await old.invoke(input('first'));
  let upgrades = 0;
  const definition = defineDurableEntity({namespace: 'counters', schema: {version: 2, initialState: () => ({count: 0}), decodeState: state,
    migrations: new Map([[1, (value: unknown) => {upgrades++; return state(value);}]]),
  }, decodePayload: (_value: unknown) => null, handle: call => call.transition(call.state, call.state)});
  const registry = createDurableEntityRegistry([definition]);
  const simulator = new DurableEntitySimulator(registry); simulator.restore(old.checkpoint());
  await simulator.invoke(input('upgrade'));
  assert.deepEqual(simulator.inspect('counters', 'one')!.data, {schema_version: 2, data: {count: 1}});
  assert.equal(upgrades, 1);
  assert.equal(definition.validateCandidate({schema_version: 1, data: {count: 1}}), false);
  assert.equal(definition.validateCandidate({schema_version: 2, data: {count: -1}}), false);
  assert.equal(definition.validateCandidate({schema_version: 2, data: {count: 1}}), true);
  assert.equal(upgrades, 1);
});
