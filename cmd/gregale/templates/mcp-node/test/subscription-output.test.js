import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { createSubscriptionOutput } from '../resource-updates.js';

function fixture(options = {}) {
  const response = new EventEmitter();
  response.writableLength = 0;
  response.write = frame => { response.writableLength += Buffer.byteLength(frame); return false; };
  const timers = [];
  let blocked = 0;
  const output = createSubscriptionOutput(response, () => { blocked++; }, {
    ...options,
    schedule(callback, ms) { const timer = { callback, ms }; timers.push(timer); return timer; },
    cancel(timer) { if (timer) timer.cancelled = true; },
  });
  return { response, output, timers, blocked: () => blocked };
}

test('notification buffers count UTF-8 bytes and close before exceeding capacity', () => {
  const f = fixture({ maxBufferedBytes: 16 });
  assert.equal(f.output.write('€'.repeat(4)), true);
  assert.equal(f.output.write('€'.repeat(2)), false);
  assert.equal(f.response.writableLength, 12);
  assert.equal(f.blocked(), 1);
  assert.equal(f.output.write('x'), false);
  assert.equal(f.response.listenerCount('drain'), 0);
  assert.equal(f.timers[0].cancelled, true);
});

test('repeated writes cannot postpone a blocked reader deadline', () => {
  const f = fixture();
  for (let i = 0; i < 100; i++) f.output.write('event');
  assert.equal(f.timers.length, 1);
  assert.equal(f.timers[0].ms, 15_000);
  f.timers[0].callback();
  f.timers[0].callback();
  assert.equal(f.blocked(), 1);
  assert.equal(f.output.write('late'), false);
});

test('draining permits a fresh write budget and cleanup cancels the deadline', () => {
  const f = fixture();
  f.output.write('first');
  f.response.writableLength = 0;
  f.response.emit('drain');
  assert.equal(f.timers[0].cancelled, true);
  f.output.write('second');
  assert.equal(f.timers.length, 2);
  f.output.close();
  f.output.close();
  assert.equal(f.timers[1].cancelled, true);
  assert.equal(f.response.listenerCount('drain'), 0);
  f.timers[1].callback();
  assert.equal(f.blocked(), 0);
});
