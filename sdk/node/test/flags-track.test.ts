import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleFlags, GREGALE_FLAG_CONTEXT_HEADER, type FlagsBundle } from '../src/flags.js';

// ADR 20261115163113 §2: track() records conversion events for the request's
// verified customer and sends them in batches to /v1/runtime/flags/events.
const tenant = '00000000-0000-4000-8000-000000000001';
const bundle: FlagsBundle = { environment_id: 'env', version: 1, flags: [], groups: {} } as unknown as FlagsBundle;

type Sent = { url: string; body?: { events: Array<Record<string, unknown>> }; capabilities: string };

function client(sent: Sent[], status = 200, accepted?: number): GregaleFlags {
  return new GregaleFlags({
    apiURL: 'https://api.example.com', identityEndpoint: 'http://127.0.0.1/token', now: () => Date.UTC(2026, 10, 15, 12),
    fetch: async (input, init) => {
      const url = String(input);
      if (url.includes('127.0.0.1')) return new Response(JSON.stringify({ access_token: 'token' }));
      if (url.endsWith('/v1/runtime/flags')) return new Response(JSON.stringify(bundle));
      const body = JSON.parse(String(init?.body)) as Sent['body'];
      sent.push({ url, body, capabilities: new Headers(init?.headers).get('X-Faas-Flags-Capabilities') ?? '' });
      return new Response(JSON.stringify({ accepted: accepted ?? body?.events.length ?? 0 }), { status });
    },
  });
}

test('track sends the verified customer, name, value and time', async () => {
  const sent: Sent[] = [];
  const flags = client(sent);
  await flags.start();
  flags.close();
  const ok = await flags.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: tenant }, () => [
    flags.track('order_completed', { value: 1200 }),
    flags.track('signup'),
  ]);
  assert.deepEqual(ok, [true, true]);
  await flags.flush();
  assert.equal(sent.length, 1);
  assert.equal(sent[0]!.url, 'https://api.example.com/v1/runtime/flags/events');
  assert.match(sent[0]!.capabilities, /track-v1/);
  assert.deepEqual(sent[0]!.body?.events, [
    { name: 'order_completed', customer_id: tenant, value: 1200, at: '2026-11-15T12:00:00.000Z' },
    { name: 'signup', customer_id: tenant, at: '2026-11-15T12:00:00.000Z' },
  ]);
  assert.equal(flags.droppedEvents(), 0);
});

test('track drops events without a verified customer or with invalid input', async () => {
  const sent: Sent[] = [];
  const flags = client(sent);
  assert.equal(flags.track('signup'), false, 'outside runRequest');
  const results = await flags.runRequest({}, () => flags.track('signup'));
  assert.equal(results, false, 'no verified customer');
  const invalid = await flags.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: tenant }, () => [
    flags.track('Bad Name'), flags.track('signup', { value: 1.5 }), flags.track('signup', { value: 2e12 }),
  ]);
  assert.deepEqual(invalid, [false, false, false]);
  await flags.flush();
  assert.equal(sent.length, 0);
  assert.equal(flags.droppedEvents(), 5);
});

test('flush sends at most 100 events per call and counts refused events', async () => {
  const sent: Sent[] = [];
  const flags = client(sent, 200, 90);
  await flags.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: tenant }, () => {
    for (let i = 0; i < 150; i++) flags.track('page_view');
  });
  await flags.flush();
  assert.deepEqual(sent.map(s => s.body?.events.length), [100, 50]);
  // The fake API accepts 90 per call: 10 of the first batch are refused, none of the second.
  assert.equal(flags.droppedEvents(), 10);
  const failing: Sent[] = [];
  const down = client(failing, 503);
  await down.runRequest({ [GREGALE_FLAG_CONTEXT_HEADER]: tenant }, () => { down.track('signup'); });
  await down.flush();
  assert.equal(down.droppedEvents(), 1);
});
