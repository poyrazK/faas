import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once, getEventListeners } from 'node:events';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { createClient, OpenAPI, ErrCapacity, ErrNotFound, type ResponseInfo, type RequestOptions, type CancelablePromise, type AppResponse } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';
import { scopedRequest } from '../src/request-scope.js';
import { bindClient } from '../src/client-instance.js';
import { AppsService } from '../src/generated-public/services/AppsService.js';

const uuid = (i: number) => `11111111-1111-4111-8111-${String(i).padStart(12, '0')}`;
const json = (value: unknown, id = uuid(1), status = 200) => Response.json(value, { status, headers: { 'X-Request-ID': id } });
const unavailable = () => json({ code: 'capacity_unavailable', tx_id: 'tx-capacity' }, uuid(1), 503);
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

test('40 concurrent HTTP calls keep response diagnostics and options with their caller', { timeout: 10_000 }, async t => {
  const originalFetch = globalThis.fetch;
  const originalConfig = { ...OpenAPI };
  const server = createServer(async (req, res) => {
    await nextTurn();
    const id = Number(req.url!.split('/').at(-1));
    res.writeHead(200, { 'Content-Type': 'application/json', 'X-Request-ID': uuid(id) });
    res.end(JSON.stringify({ id: uuid(id), token: req.headers.authorization }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => { const closed = once(server, 'close'); server.close(); server.closeAllConnections(); await closed; });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  const aResponses: ResponseInfo[] = [], bResponses: ResponseInfo[] = [];
  const a = createClient(`http://127.0.0.1:${address.port}`, { token: 'a', onResponse: info => { aResponses.push(info); } });
  const b = createClient(`http://127.0.0.1:${address.port}`, { token: 'b', onResponse: info => { bResponses.push(info); } });
  await Promise.all(Array.from({ length: 40 }, async (_, i) => {
    const client = i < 20 ? a : b;
    const own: ResponseInfo[] = [];
    const getApp = client.apps.getApp;
    const app = await getApp({ slug: String(i) }, { timeoutMs: 5000, onResponse: info => { own.push(info); } });
    assert.equal(app.id, uuid(i));
    assert.equal(own.length, 1);
    assert.equal(own[0]!.requestId, app.id);
    assert.equal(own[0]!.attempt, 1);
    assert.equal(own[0]!.status, 200);
    assert.ok(Number.isFinite(own[0]!.durationMs) && own[0]!.durationMs >= 0);
    assert.ok(Object.isFrozen(own[0]));
    assert.deepEqual(Object.keys(own[0]!).sort(), ['attempt', 'durationMs', 'requestId', 'status']);
  }));
  assert.deepEqual(aResponses.map(x => x.requestId).sort(), Array.from({ length: 20 }, (_, i) => uuid(i)).sort());
  assert.deepEqual(bResponses.map(x => x.requestId).sort(), Array.from({ length: 20 }, (_, i) => uuid(i + 20)).sort());
  assert.equal(globalThis.fetch, originalFetch);
  assert.deepEqual(OpenAPI, originalConfig);
});

test('concurrent deadlines and retry overrides do not change sibling calls or client defaults', async () => {
  const counts = new Map<string, number>();
  const keys = new Map<string, string[]>();
  const reports = new Map<string, ResponseInfo[]>();
  const client = createClient('https://retry.test', {
    retry: { maxAttempts: 3, backoffMs: 1 },
    fetch: async (_url, init) => {
      const slug = JSON.parse(String(init?.body)).slug as string;
      const count = (counts.get(slug) ?? 0) + 1; counts.set(slug, count);
      keys.set(slug, [...(keys.get(slug) ?? []), new Headers(init?.headers).get('Idempotency-Key')!]);
      return count < (slug === 'default' ? 3 : 2) ? unavailable() : json({ id: slug }, uuid(count));
    },
  });
  const options = { retry: { maxAttempts: 2, backoffMs: 1 }, timeoutMs: 1000 };
  const pending = client.apps.createApp({ requestBody: { slug: 'two' } }, {
    ...options, onResponse: info => { reports.set('two', [...(reports.get('two') ?? []), info]); },
  });
  options.retry.maxAttempts = 1;
  const [success, failure] = await Promise.allSettled([
    pending,
    client.apps.createApp({ requestBody: { slug: 'one' } }, { retry: false, onResponse: info => { reports.set('one', [info]); } }),
  ]);
  assert.equal(success.status, 'fulfilled'); assert.equal(failure.status, 'rejected');
  if (failure.status === 'rejected') { assert.ok(failure.reason instanceof ErrCapacity); assert.equal(failure.reason.txId, 'tx-capacity'); }
  await client.apps.createApp({ requestBody: { slug: 'default' } });
  assert.deepEqual(Object.fromEntries(counts), { two: 2, one: 1, default: 3 });
  assert.deepEqual(reports.get('two')!.map(info => info.attempt), [1, 2]);
  assert.equal(reports.get('one')![0]!.attempt, 1);
  for (const values of keys.values()) assert.equal(new Set(values).size, 1);
  assert.notEqual(keys.get('two')![0], keys.get('one')![0]);
});

test('a timed-out call leaves a sibling with a disabled deadline running', { timeout: 5000 }, async () => {
  const release = deferred();
  const client = createClient('https://budgets.test', { timeoutMs: 10, fetch: async (url, init) => {
    if (String(url).endsWith('/short')) return new Promise<Response>((_resolve, reject) => {
      init!.signal!.addEventListener('abort', () => reject(init!.signal!.reason), { once: true });
    });
    await release.promise; return json({ id: 'long' });
  } });
  const short = client.apps.getApp({ slug: 'short' });
  const long = client.apps.getApp({ slug: 'long' }, { timeoutMs: 0 });
  await assert.rejects(short, { name: 'TimeoutError' });
  release.resolve();
  assert.equal((await long).id, 'long');
});

test('caller abort, pre-abort and cancel() retain independent cancellation contracts', async () => {
  let calls = 0;
  const started = deferred();
  const client = createClient('https://cancel.test', { fetch: async (_url, init) => {
    calls++; started.resolve();
    return new Promise<Response>((_resolve, reject) => { init!.signal!.addEventListener('abort', () => reject(init!.signal!.reason), { once: true }); });
  } });
  const reason = new Error('caller reason');
  await assert.rejects(client.apps.getApp({ slug: 'pre' }, { signal: AbortSignal.abort(reason) }), error => error === reason);
  assert.equal(calls, 0);
  const controller = new AbortController();
  const pending = client.apps.getApp({ slug: 'abort' }, { signal: controller.signal });
  const rejected = assert.rejects(pending, error => error === reason);
  await started.promise; controller.abort(reason); await rejected;
  const cancelled = client.apps.getApp({ slug: 'cancel' });
  cancelled.cancel();
  await assert.rejects(cancelled, { name: 'CancelError' });
  assert.ok(cancelled.isCancelled);
  assert.equal(calls, 1);
});

for (const kind of ['json', 'blob', 'problem'] as const) {
  test(`deadline includes blocked ${kind} body consumption and cancels a custom stream`, { timeout: 5000 }, async t => {
    // The pinned decoder logs parse failures after cancellation; the request's
    // outer scope must still report the original deadline, never undefined.
    t.mock.method(console, 'error', () => {});
    let cancelled = 0;
    const responses: ResponseInfo[] = [];
    const client = createClient('https://body.test', { onResponse: info => { responses.push(info); }, fetch: async () => new Response(new ReadableStream({
      start(controller) { controller.enqueue(new TextEncoder().encode(kind === 'blob' ? 'bytes' : '{"id":')); },
      cancel() { cancelled++; },
    }), { status: kind === 'problem' ? 503 : 200, headers: { 'Content-Type': kind === 'blob' ? 'application/zip' : 'application/json', 'X-Request-ID': uuid(1) } }) });
    const pending = kind === 'blob' ? client.billing.exportFocusInvoices({ month: '2026-11' }, { timeoutMs: 20 }) : client.apps.getApp({ slug: 'slow' }, { timeoutMs: 20 });
    await assert.rejects(pending, { name: 'TimeoutError' });
    await nextTurn();
    assert.equal(cancelled, 1);
    assert.equal(responses.length, 1);
    assert.equal(responses[0]!.status, kind === 'problem' ? 503 : 200);
  });
}

test('deadline interrupts Retry-After without another attempt or metadata event', { timeout: 5000 }, async () => {
  let calls = 0;
  const reports: ResponseInfo[] = [];
  const client = createClient('https://backoff.test', { retry: { maxAttempts: 3, backoffMs: 1 }, fetch: async () => {
    calls++; return new Response('{}', { status: 503, headers: { 'Content-Type': 'application/json', 'Retry-After': '60' } });
  } });
  await assert.rejects(client.apps.getApp({ slug: 'wait' }, { timeoutMs: 20, onResponse: info => { reports.push(info); } }), { name: 'TimeoutError' });
  assert.equal(calls, 1); assert.equal(reports.length, 1);
});

test('one deadline budget is shared across response time, backoff and later attempts', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const first = deferred(), release = deferred(), second = deferred();
  let attempts = 0, settled = false;
  const client = createClient('https://total-budget.test', { retry: { maxAttempts: 3, backoffMs: 20 }, fetch: async (_url, init) => {
    if (++attempts === 1) { first.resolve(); await release.promise; return unavailable(); }
    second.resolve();
    return new Promise<Response>((_resolve, reject) => {
      init!.signal!.addEventListener('abort', () => reject(init!.signal!.reason), { once: true });
    });
  } });
  const pending = client.apps.getApp({ slug: 'total' }, { timeoutMs: 100 });
  const rejected = assert.rejects(pending, { name: 'TimeoutError' }).then(() => { settled = true; });
  await first.promise;
  t.mock.timers.tick(60); release.resolve(); await nextTurn();
  t.mock.timers.tick(20); await second.promise;
  t.mock.timers.tick(19); await nextTurn();
  assert.equal(settled, false);
  t.mock.timers.tick(1); await rejected;
  assert.equal(attempts, 2);
});

test('deadline also bounds asynchronous header resolution before fetch starts', { timeout: 5000 }, async () => {
  let calls = 0;
  const binding = bindClient({ AppsService }, 'https://headers.test', { fetch: async () => { calls++; return json({}); } }, getConfig => ({
    testRequest() {
      const config = { ...getConfig(), TOKEN: async () => new Promise<string>(() => {}) };
      return scopedRequest(config, { method: 'GET', url: '/v1/apps' }, { timeoutMs: 20 });
    },
  }));
  await assert.rejects(binding.testRequest(), { name: 'TimeoutError' });
  assert.equal(calls, 0);
});

test('observers are body-free, frozen, nonblocking and cannot change typed failures', async () => {
  const seen: ResponseInfo[] = [];
  const client = createClient('https://private.test/secret', {
    token: 'secret-token', onResponse: info => { seen.push(info); throw new Error('observer error'); },
    fetch: async () => json({ code: 'not_found', detail: 'private body', tx_id: 'original-tx' }, 'secret-header', 404),
  });
  await assert.rejects(client.apps.getApp({ slug: 'private-name' }, { onResponse: async info => {
    assert.equal(info, seen[0]);
    assert.equal(Reflect.set(info, 'status', 200), false);
    throw new Error('async observer error');
  } }), error => error instanceof ErrNotFound && error.txId === 'original-tx');
  await nextTurn();
  assert.equal(seen[0]!.requestId, null);
  assert.equal(JSON.stringify(seen).includes('secret'), false);
  const never = createClient('https://observer.test', { onResponse: () => new Promise<void>(() => {}), fetch: async () => json({ id: 'ok' }) });
  assert.equal((await never.apps.getApp({ slug: 'ok' })).id, 'ok');
});

test('no-response failures emit no diagnostics, and listeners and deadline timers retire on completion', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const controller = new AbortController();
  const reports: ResponseInfo[] = [];
  const signals: AbortSignal[] = [];
  const client = createClient('https://cleanup.test', { onResponse: info => { reports.push(info); }, fetch: async (url, init) => {
    signals.push(init!.signal!);
    if (String(url).endsWith('/network')) throw new TypeError('network failed');
    return json({ id: 'ok' });
  } });
  await client.apps.getApp({ slug: 'ok' }, { signal: controller.signal, timeoutMs: 100 });
  await assert.rejects(client.apps.getApp({ slug: 'network' }, { signal: controller.signal, timeoutMs: 100 }), /network failed/);
  assert.equal(reports.length, 1);
  assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
  t.mock.timers.tick(200);
  assert.ok(signals.every(signal => !signal.aborted));
});

test('late custom responses after cancellation are retired without late observer delivery', async () => {
  const started = deferred(), release = deferred();
  let cancelled = 0, observed = 0;
  const client = createClient('https://late.test', { onResponse: () => { observed++; }, fetch: async () => {
    started.resolve(); await release.promise;
    return new Response(new ReadableStream({ cancel() { cancelled++; } }));
  } });
  const controller = new AbortController();
  const reason = new Error('cancel before headers');
  const pending = client.apps.getApp({ slug: 'late' }, { signal: controller.signal });
  const rejected = assert.rejects(pending, error => error === reason);
  await started.promise; controller.abort(reason); await rejected;
  release.resolve(); await nextTurn();
  assert.equal(cancelled, 1); assert.equal(observed, 0);
});

test('the client observer also covers bound execution submission, SSE and receipt responses', async () => {
  const responses: ResponseInfo[] = [];
  let count = 0;
  const client = createPreviewClient('https://runs.test', { onResponse: info => { responses.push(info); }, fetch: async url => {
    const id = uuid(++count);
    return String(url).includes('/events') ? new Response('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n', {
      headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': id },
    }) : json({ id: 'run', status: 'succeeded' }, id);
  } });
  await client.runExecution({ runtime: 'node22', source: 'console.log(1)' });
  assert.deepEqual(responses.map(info => info.requestId), [uuid(1), uuid(2), uuid(3)]);
  assert.ok(responses.every(info => info.attempt === 1));
});

test('binary bodies, no-argument services, defaulted parameters and public/preview types stay usable', async () => {
  const client = createClient('https://types.test', { fetch: async url => String(url).includes('/billing/') ? new Response(new Uint8Array([0, 255, 7])) : json({ id: 'ok' }) });
  const download = await client.billing.exportFocusInvoices({ month: '2026-11' }, { timeoutMs: 1000 });
  assert.deepEqual(new Uint8Array(await download.arrayBuffer()), new Uint8Array([0, 255, 7]));
  await client.account.getAccount(undefined, { timeoutMs: 1000 });
  await client.account.getAccount();
  await client.apps.listApps();
  for (const timeoutMs of [-1, 1.5, NaN, Infinity, 2 ** 31]) await assert.rejects(client.apps.getApp({ slug: 'bad' }, { timeoutMs }), TypeError);
  if (false) {
    const options: RequestOptions = { timeoutMs: 100, retry: false, signal: AbortSignal.abort() };
    const result: CancelablePromise<AppResponse> = client.apps.getApp({ slug: 'typed' }, options);
    result.cancel();
    // @ts-expect-error required generated parameters remain required
    client.apps.getApp({}, options);
    // @ts-expect-error options stay outside the generated API parameter object
    client.apps.getApp({ slug: 'typed', timeoutMs: 100 });
    // @ts-expect-error preview namespaces remain outside the default factory
    client.flags;
    createPreviewClient('https://preview.test').runs.getExecution({ id: 'run' }, options);
  }
});
