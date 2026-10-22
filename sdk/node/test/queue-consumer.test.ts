import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleQueueConsumer, type QueueConsumerOptions, type QueueHandler } from '../src/queue-consumer.js';

type Call = { verb: string; body: Record<string, unknown>; auth: string | null };
type Leased = { id: string; receipt: string; payload: unknown; headers: Record<string, string>; attempt: number; enqueued_at: string };

// fakeQueue serves the lease API from memory. An empty lease waits briefly
// (a short stand-in for the server's long-poll) and honors aborts like fetch.
function fakeQueue(payloads: unknown[], opts: { extendFails?: boolean } = {}) {
  const calls: Call[] = [];
  const pending: Leased[] = payloads.map((payload, i) => ({
    id: `m${i}`, receipt: `m${i}.r`, payload, headers: { k: 'v' }, attempt: 1, enqueued_at: new Date(0).toISOString(),
  }));
  const fetchImpl = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input));
    if (url.hostname === '127.0.0.1') {
      calls.push({ verb: `identity:${url.searchParams.get('audience')}`, body: {}, auth: null });
      return Response.json({ access_token: 'eyJ.workload.token', token_type: 'Bearer', expires_in: 300 });
    }
    const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
    const verb = url.pathname.split(':')[1] ?? 'lease';
    calls.push({ verb, body, auth: new Headers(init?.headers).get('authorization') });
    if (verb === 'lease') {
      const taken = pending.splice(0, Number(body.max_messages));
      if (taken.length === 0) await delay(30, init?.signal ?? undefined);
      return Response.json({ messages: taken });
    }
    const receipts = body.receipts as string[];
    const held = !(verb === 'extend' && opts.extendFails);
    return Response.json({ succeeded: held ? receipts : [], failed: held ? [] : receipts.map(receipt => ({ receipt, reason: 'lease_expired' })) });
  };
  return { calls, fetch: fetchImpl as typeof globalThis.fetch };
}

function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener('abort', () => { clearTimeout(timer); reject(new DOMException('aborted', 'AbortError')); }, { once: true });
  });
}

async function until(check: () => boolean, ms = 3000): Promise<void> {
  const deadline = Date.now() + ms;
  while (!check()) {
    if (Date.now() > deadline) throw new Error('condition not met in time');
    await delay(5);
  }
}

function consumer<T>(fake: ReturnType<typeof fakeQueue>, handler: QueueHandler<T>, extra: Partial<QueueConsumerOptions> = {}) {
  return new GregaleQueueConsumer<T>({ app: 'worker', queue: 'jobs', apiURL: 'https://api.test', apiKey: 'key', fetch: fake.fetch, ...extra }, handler);
}

test('acks handled messages and nacks failures with the error and delay', async () => {
  const fake = fakeQueue([{ ok: true }, { ok: false }]);
  const errors: unknown[] = [];
  const seen: unknown[] = [];
  const c = consumer<{ ok: boolean }>(fake, message => {
    seen.push(message.payload);
    assert.equal(message.headers.k, 'v');
    if (!message.payload.ok) throw new Error('boom');
  }, { concurrency: 2, retryDelaySeconds: 5, onError: e => errors.push(e) });
  void c.run();
  await until(() => fake.calls.some(call => call.verb === 'ack') && fake.calls.some(call => call.verb === 'nack'));
  await c.stop();
  const lease = fake.calls.find(call => call.verb === 'lease')!;
  assert.deepEqual(lease.body, { max_messages: 2, visibility_timeout_seconds: 30, wait_seconds: 20 });
  assert.equal(lease.auth, 'Bearer key');
  assert.deepEqual(fake.calls.find(call => call.verb === 'ack')!.body, { receipts: ['m0.r'] });
  const nack = fake.calls.find(call => call.verb === 'nack')!.body;
  assert.deepEqual(nack.receipts, ['m1.r']);
  assert.equal(nack.delay_seconds, 5);
  assert.match(String(nack.error), /boom/);
  assert.equal(seen.length, 2);
  assert.ok(errors.some(e => e instanceof Error && e.message === 'boom'));
});

test('authenticates with a cached gregale:queues workload token', async () => {
  const fake = fakeQueue([1, 2, 3]);
  const c = consumer(fake, () => {}, { apiKey: undefined, identityEndpoint: 'http://127.0.0.1:9/token' });
  void c.run();
  await until(() => fake.calls.filter(call => call.verb === 'ack').length === 3);
  await c.stop();
  assert.deepEqual(fake.calls.filter(call => call.verb.startsWith('identity')).map(call => call.verb), ['identity:gregale:queues']);
  assert.ok(fake.calls.filter(call => call.verb !== 'identity:gregale:queues').every(call => call.auth === 'Bearer eyJ.workload.token'));
});

test('never runs more handlers than its concurrency', async () => {
  const fake = fakeQueue([1, 2, 3, 4, 5, 6, 7]);
  let active = 0;
  let peak = 0;
  const c = consumer(fake, async () => {
    active++;
    peak = Math.max(peak, active);
    await delay(15);
    active--;
  }, { concurrency: 2 });
  void c.run();
  await until(() => fake.calls.filter(call => call.verb === 'ack').length === 7);
  await c.stop();
  assert.equal(peak, 2);
  assert.ok(fake.calls.filter(call => call.verb === 'lease').every(call => Number(call.body.max_messages) <= 2));
});

test('extends a slow lease and aborts the handler when the lease is lost', async () => {
  const fake = fakeQueue(['slow'], { extendFails: true });
  let aborted = false;
  const c = consumer(fake, async (_message, { signal }) => {
    await new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }));
    aborted = true;
  }, { visibilityTimeoutSeconds: 2 });
  void c.run();
  await until(() => aborted, 4000);
  await c.stop();
  const extend = fake.calls.find(call => call.verb === 'extend')!;
  assert.deepEqual(extend.body, { receipts: ['m0.r'], visibility_timeout_seconds: 2 });
  // A lost message belongs to its next delivery: no ack or nack.
  assert.equal(fake.calls.some(call => call.verb === 'ack' || call.verb === 'nack'), false);
});

test('stop lets in-flight work finish and settles it without leasing more', async () => {
  const fake = fakeQueue(['first', 'second']);
  let release!: () => void;
  let started!: () => void;
  const running = new Promise<void>(resolve => { started = resolve; });
  const c = consumer(fake, () => {
    started();
    return new Promise<void>(done => { release = done; });
  });
  void c.run();
  await running;
  const stopping = c.stop();
  const leasesAtStop = fake.calls.filter(call => call.verb === 'lease').length;
  release();
  await stopping;
  assert.deepEqual(fake.calls.filter(call => call.verb === 'ack').map(call => call.body.receipts), [['m0.r']]);
  assert.equal(fake.calls.filter(call => call.verb === 'lease').length, leasesAtStop);
});

test('rejects unsafe options', () => {
  const fake = fakeQueue([]);
  const base = { app: 'worker', queue: 'jobs', apiURL: 'https://api.test', apiKey: 'key', fetch: fake.fetch };
  for (const bad of [
    { apiURL: 'http://api.test' },
    { concurrency: 0 },
    { concurrency: 101 },
    { waitSeconds: 21 },
    { visibilityTimeoutSeconds: 1 },
    { apiKey: undefined, identityEndpoint: 'http://metadata.example/token' },
    { app: '' },
  ]) {
    assert.throws(() => new GregaleQueueConsumer({ ...base, ...bad }, () => {}), Error, JSON.stringify(bad));
  }
});
