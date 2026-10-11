import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { reservationTransition } from './reservations.mjs';
import { createReservationServer } from './server.mjs';
import { decodeDurableEntityHandlerRequest, DURABLE_ENTITY_HANDLER_PATH } from './sdk.mjs';

const hook = '6dd283da-3c14-40de-9d47-bb71fb35be9a';
function fixture() {
  return decodeDurableEntityHandlerRequest(readFileSync(new URL('../../sdk/go/testdata/durable-entity-handler-request.json', import.meta.url)));
}

test('reservation and confirmation are returned together without sending', () => {
  const call = fixture();
  const body = JSON.parse(reservationTransition(call, hook));
  assert.deepEqual(body.data, { status: 'reserved', quantity: 2 });
  assert.deepEqual(body.result, body.data);
  assert.deepEqual(body.outbox, [{ webhook_id: hook, event_type: 'reservation.confirmed', payload: { reservation_key: 'reservation:123', quantity: 2 } }]);
  assert.deepEqual(call.state.data, {});
  // Even a new request identity cannot reconfirm the same business reservation.
  call.state = { data: body.data, version: 1 };
  call.request_id = 'another-request';
  const repeated = JSON.parse(reservationTransition(call, hook));
  assert.deepEqual(repeated.data, body.data);
  assert.equal(Object.hasOwn(repeated, 'outbox'), false);
  call.payload.quantity = 3;
  assert.throws(() => reservationTransition(call, hook));
});

test('reservation refuses invalid quantities, states, namespaces and old protocols', () => {
  for (const kind of ['quantity', 'state', 'committed-empty', 'namespace', 'protocol', 'event']) {
    const call = fixture();
    if (kind === 'quantity') call.payload.quantity = 0;
    if (kind === 'committed-empty') call.state.version = 1;
    if (kind === 'state') call.state.data = { status: 'cancelled', quantity: 2 };
    if (kind === 'namespace') call.entity.namespace = 'other';
    if (kind === 'protocol') { call.protocol_version = 1; delete call.limits; }
    if (kind === 'event') call.event = 'alarm';
    assert.throws(() => reservationTransition(call, hook), kind);
  }
});

test('guest listener returns v2 transitions and refuses public routes and malformed bodies', async t => {
  const server = createReservationServer({ webhookID: hook });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const base = `http://127.0.0.1:${server.address().port}`;
  const response = await fetch(base + DURABLE_ENTITY_HANDLER_PATH, { method: 'POST', body: JSON.stringify(fixture()) });
  assert.equal(response.status, 200);
  assert.equal((await response.json()).outbox.length, 1);
  const invalid = await fetch(base + DURABLE_ENTITY_HANDLER_PATH, { method: 'POST', body: '{}' });
  assert.equal(invalid.status, 422);
  assert.equal((await invalid.text()).includes('claim_token'), false);
  const missing = await fetch(base + '/reservations', { method: 'POST', body: '{}' });
  assert.equal(missing.status, 404);
  await missing.text();
});
