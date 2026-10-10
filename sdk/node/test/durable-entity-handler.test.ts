import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  decodeDurableEntityHandlerRequest, durableEntityWebhookIntent, encodeDurableEntityTransition,
  DURABLE_ENTITY_PROTOCOL_VERSION, DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION, DURABLE_ENTITY_MAX_REQUEST_BYTES,
  type DurableEntityHandlerRequest, type DurableEntityTransition,
} from '../src/index.js';

const hook = '6dd283da-3c14-40de-9d47-bb71fb35be9a';
function fixture(): DurableEntityHandlerRequest {
  return decodeDurableEntityHandlerRequest(readFileSync('../go/testdata/durable-entity-handler-request.json'));
}

test('durable entity helpers match the shared Go/platform wire contract', () => {
  const call = fixture();
  const payload = { reservation_key: call.entity.key, quantity: 2 };
  const intent = durableEntityWebhookIntent(call, hook, 'reservation.confirmed', payload);
  payload.quantity = 99;
  const next = { status: 'reserved', quantity: 2 };
  const body = encodeDurableEntityTransition(call, { data: next, result: next, outbox: [intent] });
  assert.deepEqual(JSON.parse(body), JSON.parse(readFileSync('../go/testdata/durable-entity-handler-transition.json', 'utf8')));
  const plain = JSON.parse(encodeDurableEntityTransition(call, { data: next, result: next }));
  assert.equal(Object.hasOwn(plain, 'outbox'), false);
  assert.equal(Object.hasOwn(plain, 'alarm_at'), false);
  assert.equal(body.includes('claim_token'), false);
});

test('outgoing work requires negotiated v2 while pure v1 handlers still work', () => {
  for (const protocol of [DURABLE_ENTITY_PROTOCOL_VERSION, DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION]) {
    const call = fixture();
    call.protocol_version = protocol;
    if (protocol === DURABLE_ENTITY_PROTOCOL_VERSION) delete call.limits;
    const decoded = decodeDurableEntityHandlerRequest(JSON.stringify(call));
    assert.deepEqual(JSON.parse(encodeDurableEntityTransition(decoded, { data: null, result: null })), { data: null, result: null });
    const intent = () => durableEntityWebhookIntent(decoded, hook, 'reservation.confirmed', null);
    const batch = () => encodeDurableEntityTransition(decoded, { data: null, result: null, outbox: [] });
    if (protocol === DURABLE_ENTITY_PROTOCOL_VERSION) { assert.throws(intent); assert.throws(batch); }
    else { assert.doesNotThrow(intent); assert.doesNotThrow(batch); }
  }
});

test('intents reject URLs, wrong UUIDs, private fields and encoded payload expansion', () => {
  const call = fixture();
  for (const webhook of ['https://receiver.example.test', hook.toUpperCase(), '00000000-0000-0000-0000-000000000000']) {
    assert.throws(() => durableEntityWebhookIntent(call, webhook, 'reservation.confirmed', {}));
  }
  for (const event of ['', ' ', 'event\x00private', 'x'.repeat(call.limits!.identity_bytes + 1)]) {
    assert.throws(() => durableEntityWebhookIntent(call, hook, event, {}));
  }
  assert.throws(() => durableEntityWebhookIntent(call, hook, 'event', '<'.repeat(call.limits!.outbox_payload_bytes / 2)));
  const intent = { webhook_id: hook, event_type: 'event', payload: {}, url: 'https://arbitrary.example.test' };
  assert.throws(() => encodeDurableEntityTransition(call, { data: null, result: null, outbox: [intent] }));
});

test('transition bounds and invalid JSON values fail before a guest can return work', () => {
  for (const kind of ['batch', 'batch-bytes', 'transition', 'undefined', 'bigint', 'infinity', 'alarm', 'invalid-day', 'invalid-hour', 'zero-alarm', 'authority']) {
    const call = fixture();
    const intent = durableEntityWebhookIntent(call, hook, 'event', {});
    const next: DurableEntityTransition = { data: null, result: null, outbox: [intent] };
    switch (kind) {
      case 'batch': next.outbox = Array(call.limits!.outbox_messages + 1).fill(intent); break;
      case 'batch-bytes':
        call.limits!.outbox_bytes = call.limits!.outbox_payload_bytes;
        intent.payload = 'x'.repeat(call.limits!.outbox_payload_bytes / 2);
        next.outbox = [intent, intent]; break;
      case 'transition': next.data = 'x'.repeat(call.limits!.transition_bytes); break;
      case 'undefined': next.result = undefined; break;
      case 'bigint': next.result = 1n; break;
      case 'infinity': next.result = Infinity; break;
      case 'alarm': next.alarm_at = 'tomorrow'; break;
      case 'invalid-day': next.alarm_at = '2026-02-30T12:00:00Z'; break;
      case 'invalid-hour': next.alarm_at = '2026-10-10T24:00:00Z'; break;
      case 'zero-alarm': next.alarm_at = '0001-01-01T00:00:00Z'; break;
      case 'authority': Object.assign(next, { claim_token: 'private' }); break;
    }
    assert.throws(() => encodeDurableEntityTransition(call, next), kind);
  }
  const call = fixture();
  const body = encodeDurableEntityTransition(call, { data: '<>&\u2028\u2029', result: null, alarm_at: '2026-10-10T12:00:00.123456789Z' });
  assert.equal(body.includes('<'), false);
  assert.equal(body.includes('\\u003c'), true);
});

test('envelopes reject unsupported versions, missing limits, unsafe versions and hidden authority', () => {
  for (const kind of ['protocol', 'limits', 'limit-value', 'state-version', 'scope', 'payload', 'authority', 'trailing', 'bytes', 'utf8']) {
    const call = fixture();
    let body: string | Uint8Array;
    switch (kind) {
      case 'protocol': Object.assign(call, { protocol_version: 99 }); break;
      case 'limits': delete call.limits; break;
      case 'limit-value': call.limits!.outbox_messages = 0; break;
      case 'state-version': call.state.version = Number.MAX_SAFE_INTEGER + 1; break;
      case 'scope': call.entity.key = 'key\x00private'; break;
      case 'payload': delete (call as Partial<DurableEntityHandlerRequest>).payload; break;
      case 'authority': Object.assign(call, { claim_token: 'private' }); break;
    }
    body = JSON.stringify(call);
    if (kind === 'trailing') body += ' {}';
    if (kind === 'bytes') body += ' '.repeat(DURABLE_ENTITY_MAX_REQUEST_BYTES);
    if (kind === 'utf8') body = Buffer.concat([Buffer.from(body), Buffer.from([0xff])]);
    assert.throws(() => decodeDurableEntityHandlerRequest(body), kind);
  }
});
