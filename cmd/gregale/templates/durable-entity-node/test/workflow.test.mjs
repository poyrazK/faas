import test from 'node:test';
import assert from 'node:assert/strict';
import {DurableEntitySimulator} from '@gregale/sdk-node/preview/durable-entities/testing';
import {durableSessionLifecycleFrame} from '@gregale/sdk-node/preview/durable-entities/session';
import {registry} from '../registry.mjs';
import validateRestore from '../validator.mjs';
import {createEntityServer} from '../server.mjs';
import {createLocalServer} from '../dev.mjs';
const input = {namespace: 'counters', key: 'customer:123', requestId: 'one', payload: {delta: 2}};

test('lost acknowledgement survives a local checkpoint and replays without applying twice', async () => {
  const sim = new DurableEntitySimulator(registry);
  await assert.rejects(sim.invoke(input, {fault: 'after-commit'}));
  const restored = new DurableEntitySimulator(registry); restored.restore(sim.checkpoint());
  assert.deepEqual(await restored.invoke(input), {value: {count: 2}, version: 1, replayed: true});
  await assert.rejects(restored.invoke({...input, payload: {delta: 3}}));
});
test('reservations capture one intent and sessions consume manually advanced alarms', async () => {
  const sim = new DurableEntitySimulator(registry);
  const reservation = {namespace: 'reservations', key: 'seat:1', requestId: 'reserve', payload: {action: 'reserve', quantity: 1, webhookId: '11111111-1111-4111-8111-111111111111'}};
  await sim.invoke(reservation); await sim.invoke(reservation);
  assert.equal(sim.pendingIntents('reservations', 'seat:1').length, 1);
  await sim.invoke({namespace: 'sessions', key: 'run:1', requestId: 'start', payload: {action: 'start', at: '2000-01-01T00:00:01Z'}});
  assert.equal((await sim.runDueAlarms()).length, 0);
  sim.advance(1000); assert.equal((await sim.runDueAlarms()).length, 1);
  assert.deepEqual(sim.inspect('sessions', 'run:1').data, {schema_version: 1, data: {status: 'done', steps: 2}});
  assert.equal((await sim.runDueAlarms()).length, 0);
});
test('isolated restore validator rejects unknown namespaces and malformed state', () => {
  const request = {protocol_version: 1, event: 'validate_restore', entity: {namespace: 'counters'}, candidate: {schema_version: 1, data: {count: 2}}};
  assert.equal(validateRestore(request).valid, true);
  assert.equal(validateRestore({...request, entity: {namespace: 'toString'}}).valid, false);
  assert.equal(validateRestore({...request, candidate: {schema_version: 1, data: {count: -1}}}).valid, false);
});
async function listen(server, t) {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {server.closeAllConnections(); server.close();});
  return `http://127.0.0.1:${server.address().port}`;
}
test('production server exposes health and refuses local simulator routes', async t => {
  const base = await listen(createEntityServer(), t);
  assert.equal((await fetch(base+'/healthz')).status, 200);
  assert.equal((await fetch(base+'/checkpoint')).status, 404);
  assert.equal((await fetch(base+'/__gregale/entities', {method: 'POST', body: '{}'})).status, 422);
});
test('local HTTP workflow injects faults, replays and restores only into an empty simulator', async t => {
  const {server} = createLocalServer(); const base = await listen(server, t);
  const post = (path, body) => fetch(base+path, {method: 'POST', body: JSON.stringify(body)});
  const wire = {namespace: 'counters', key: 'a', request_id: 'one', payload: {delta: 1}};
  assert.equal((await post('/invoke', {...wire, fault: 'after-commit'})).status, 409);
  assert.equal((await (await post('/invoke', wire)).json()).replayed, true);
  const saved = await (await fetch(base+'/checkpoint')).text();
  assert.equal((await fetch(base+'/checkpoint', {method: 'POST', body: saved})).status, 409);
  const second = await listen(createLocalServer().server, t);
  assert.equal((await fetch(second+'/checkpoint', {method: 'POST', body: saved})).status, 200);
  assert.equal((await (await fetch(second+'/inspect?namespace=counters&key=a')).json()).version, 1);
});

test('room session frames replay after checkpoint restart and validate isolated restore candidates', async () => {
  const sim=new DurableEntitySimulator(registry);
  const id='11111111-1111-4111-8111-111111111111';
  await sim.invoke({namespace:'rooms',key:'document:1',requestId:`session/${id}/open`,payload:{kind:'open',session_id:id}});
  const frame={namespace:'rooms',key:'document:1',requestId:`session/${id}/message/1`,payload:{kind:'message',session_id:id,sequence:1,payload:2}};
  await sim.invoke(frame);
  const restored=new DurableEntitySimulator(registry);restored.restore(sim.checkpoint());assert.equal((await restored.invoke(frame)).replayed,true);
  const candidate=restored.inspect('rooms','document:1').data;
  assert.equal(validateRestore({protocol_version:1,event:'validate_restore',entity:{namespace:'rooms'},candidate}).valid,true);
  candidate.data.sessions[0].sequence=-1;
  assert.equal(validateRestore({protocol_version:1,event:'validate_restore',entity:{namespace:'rooms'},candidate}).valid,false);
});

test('recoverable room expires, reclaims and fences a retired generation after restart', async () => {
  const sim = new DurableEntitySimulator(registry);
  const id = '11111111-1111-4111-8111-111111111111';
  const operation = n => `${n.toString(16).padStart(8,'0')}-1111-4111-8111-111111111111`;
  const invoke = command => {const frame = durableSessionLifecycleFrame(command);return sim.invoke({namespace:'recoverable-rooms',key:'document:1',requestId:frame.request_id,payload:frame.payload});};
  await invoke({kind:'open',session_id:id,generation:0});
  await invoke({kind:'message',session_id:id,generation:0,sequence:1,payload:2});
  const status = await invoke({kind:'status',session_id:id,query_id:operation(1)});
  assert.equal(status.value.sequence,1);
  await invoke({kind:'advance',generation:0,tick:10,operation_id:operation(2)});
  await assert.rejects(invoke({kind:'reclaim',generation:0,operation_id:operation(3)}));
  await invoke({kind:'advance',generation:0,tick:15,operation_id:operation(4)});
  await invoke({kind:'reclaim',generation:0,operation_id:operation(5)});
  const saved = JSON.parse(sim.checkpoint()); saved.entities[0].receipts = [];
  const restored = new DurableEntitySimulator(registry);restored.restore(JSON.stringify(saved));
  const old = durableSessionLifecycleFrame({kind:'open',session_id:id,generation:0});
  await assert.rejects(restored.invoke({namespace:'recoverable-rooms',key:'document:1',requestId:old.request_id,payload:old.payload}));
  const candidate = restored.inspect('recoverable-rooms','document:1').data;
  const request = {protocol_version:1,event:'validate_restore',entity:{namespace:'recoverable-rooms'},candidate};
  assert.equal(validateRestore(request).valid,true);
  candidate.data.generation = -1;assert.equal(validateRestore(request).valid,false);
});
