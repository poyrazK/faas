import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient } from '../src/index.js';
import { EventsService } from '../src/generated/services/EventsService.js';
import type { EventReceiptResponse } from '../src/generated/models/EventReceiptResponse.js';

test('event receipt exposes backfill handler state while preserving acceptance counts', async t => {
  const receipt: EventReceiptResponse = {
    event_id: 'evt/?+&', event_source: 'orders', event_type: 'created', accepted_at: '2026-10-07T12:00:00Z',
    snapshot_captured: true, routing_mode: 'recipient', recipient_count: 0, routing_summary: {},
    backfill_recipient_count: 1, backfill_routing_summary: { enqueued: 1 }, recipients: [{
      app_id: 'app', subscription_id: 'added', origin: 'backfill', backfill_job_id: 'job', backfill_job_url: '/v1/event-replays/job',
      routing: { state: 'enqueued', attempts: 1, retryable: false, replay_count: 0 }, recovery_actions: [],
      execution: { invocation_id: 'handler', state: 'pending', attempts: 0, replay_generation: 0, created_at: '2026-10-07T12:01:00Z' },
    }],
  };
  const server = createServer((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer token');
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(url.pathname, '/v1/events/receipt');
    assert.equal(url.searchParams.get('source'), receipt.event_source);
    assert.equal(url.searchParams.get('id'), receipt.event_id);
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify(receipt));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const result = await EventsService.getEventReceipt({ source: receipt.event_source, id: receipt.event_id });
  assert.deepEqual(result, receipt);
  assert.equal(result.recipients[0]?.execution?.state, 'pending');
});
