import test from 'node:test';
import assert from 'node:assert/strict';
import {summarizeDurableEntity} from '../src/durable-entity-visibility.js';
import type {DurableEntityInspectResponse} from '../src/generated/models/DurableEntityInspectResponse.js';
function inspection(): DurableEntityInspectResponse {
  return {entity: {account_id: 'account', app_id: 'app', environment_id: 'env', namespace: 'sessions', key: 'run:1'}, recovery_revision: 'revision', version: 3, state_committed: true,
    alarm: {attempts: 1, exhausted: false, alarm_at: '2026-10-11T00:00:00Z'}, outbox: {pending: 1, attempts: 0, exhausted: false}};
}
test('diagnostics distinguish clock-based due work from delivery evidence and isolate returned metadata', () => {
  const input = inspection();
  const report = summarizeDurableEntity(input, new Date('2026-10-11T00:00:00Z'));
  assert.equal(report.alarm_status, 'due'); assert.equal(report.outbox_status, 'pending');
  assert.ok(report.findings.some(f => f.code === 'delivery_unknown'));
  report.inspection.outbox.pending = 0; assert.equal(input.outbox.pending, 1);
  assert.equal(report.limitations.length, 3);
});
test('backoff, exhaustion and successful transport with pending acknowledgement stay distinct', () => {
  const input = inspection(); const now = new Date('2026-10-11T00:00:00Z');
  input.alarm.next_attempt_at = '2026-10-11T00:01:00Z'; input.outbox.next_attempt_at = input.alarm.next_attempt_at;
  let report = summarizeDurableEntity(input, now);
  assert.equal(report.alarm_status, 'backoff'); assert.equal(report.outbox_status, 'backoff');
  input.alarm.exhausted = true; input.outbox.exhausted = true; input.outbox.head_delivery = {status: 'succeeded'};
  report = summarizeDurableEntity(input, now);
  assert.equal(report.alarm_status, 'exhausted'); assert.equal(report.outbox_status, 'exhausted');
  assert.ok(report.findings.some(f => f.code === 'delivery_succeeded_pending'));
  assert.equal(report.inspection.version, 3);
});
test('invalid clocks and unsafe versions cannot become successful diagnostics', () => {
  assert.throws(() => summarizeDurableEntity(inspection(), new Date('invalid')));
  assert.throws(() => summarizeDurableEntity({...inspection(), version: Number.MAX_SAFE_INTEGER+1}));
});
