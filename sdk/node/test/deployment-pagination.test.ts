import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once, getEventListeners } from 'node:events';
import { setImmediate as nextTurn } from 'node:timers/promises';
import {
  createClient, OpenAPI, PaginationError, ErrNotFound, ErrCapacity, type DeploymentPagination,
  type PaginationOptions, type DeploymentResponse, type DeploymentListResponse,
  type BuildResponse, type BuildListResponse, type ResponseInfo, type CancelablePromise,
} from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

const cursor = '2026-11-13T20:01:02.123456789Z|opaque/+%?';
const page = (ids: string[], next?: string | null) => Response.json({ items: ids.map(id => ({ id })), next_before: next });
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}
async function collect<T>(source: AsyncIterable<T>): Promise<T[]> {
  const result: T[] = [];
  for await (const value of source) result.push(value);
  return result;
}

const cases = [
  {
    name: 'app deployments', path: '/v1/apps/my%2Fapp/deployments',
    items: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateAppDeployments({ slug: 'my/app', limit: 2 }, options),
    pages: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateAppDeploymentPages({ slug: 'my/app', limit: 2 }, options),
  },
  {
    name: 'account deployments', path: '/v1/deployments',
    items: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateDeployments({ limit: 2 }, options),
    pages: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateDeploymentPages({ limit: 2 }, options),
  },
  {
    name: 'builds', path: '/v1/builds',
    items: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateBuilds({ app: 'my/app', status: 'running', limit: 2 }, options),
    pages: (client: { deployments: DeploymentPagination }, options?: PaginationOptions) => client.deployments.iterateBuildPages({ app: 'my/app', status: 'running', limit: 2 }, options),
  },
];

for (const [surface, factory] of [['public', createClient], ['preview', createPreviewClient]] as const) {
  for (const route of cases) {
    test(`${surface} ${route.name} item traversal is lazy and round-trips cursors and filters`, async () => {
      const urls: URL[] = [];
      const client = factory('https://pages.test/prefix', { fetch: async url => {
        const parsed = new URL(String(url)); urls.push(parsed);
        assert.equal(parsed.pathname, `/prefix${route.path}`);
        assert.equal(parsed.searchParams.get('limit'), '2');
        if (route.name === 'builds') {
          assert.equal(parsed.searchParams.get('app'), 'my/app');
          assert.equal(parsed.searchParams.get('status'), 'running');
        }
        return urls.length === 1 ? page(['a', 'b'], cursor) : page(['c']);
      } });
      const iterator = route.items(client);
      assert.equal(urls.length, 0);
      assert.equal((await iterator.next()).value?.id, 'a');
      assert.equal((await iterator.next()).value?.id, 'b');
      assert.equal(urls.length, 1);
      assert.equal((await iterator.next()).value?.id, 'c');
      assert.equal(urls.length, 2);
      assert.equal(urls[0]!.searchParams.has('before'), false);
      assert.equal(urls[1]!.searchParams.get('before'), cursor);
      assert.deepEqual(await iterator.next(), { done: true, value: undefined });
      assert.equal(urls.length, 2);
    });
  }
}

for (const route of cases) {
  test(`${route.name} page traversal includes empty pages and enforces exact page/item limits`, async () => {
    const limits: number[] = [];
    const client = createClient('https://limits.test', { fetch: async url => {
      const size = Number(new URL(String(url)).searchParams.get('limit')); limits.push(size);
      if (limits.length === 1) return page([], 'empty-page');
      return page(Array.from({ length: size }, (_, i) => String(i)), `next-${limits.length}`);
    } });
    const pages = await collect<DeploymentListResponse | BuildListResponse>(route.pages(client, { maxPages: 3, maxItems: 3 }));
    assert.deepEqual(pages.map(value => value.items.length), [0, 2, 1]);
    assert.deepEqual(limits, [2, 2, 1]);
    assert.equal(pages[2]!.next_before, 'next-3');
    assert.deepEqual(await collect<DeploymentResponse | BuildResponse>(route.items(client, { maxPages: 0 })), []);
    assert.deepEqual(await collect<DeploymentListResponse | BuildListResponse>(route.pages(client, { maxItems: 0 })), []);
    assert.equal(limits.length, 3);
  });
}

test('empty nonterminal pages advance item traversal, and every empty terminal cursor ends it', async () => {
  for (const terminal of [undefined, null, '']) {
    let calls = 0;
    const client = createClient('https://empty.test', { fetch: async () => ++calls === 1 ? page([], cursor) : page(['last'], terminal) });
    assert.deepEqual((await collect(client.deployments.iterateBuilds())).map(value => value.id), ['last']);
    assert.equal(calls, 2);
  }
});

test('breaking or returning between buffered items prevents subsequent page fetches', async () => {
  let calls = 0;
  const client = createClient('https://break.test', { fetch: async () => { calls++; return page(['a', 'b'], cursor); } });
  const iterate = client.deployments.iterateDeployments;
  for await (const item of iterate()) { assert.equal(item.id, 'a'); break; }
  const iterator = client.deployments.iterateDeploymentPages();
  await iterator.return();
  assert.deepEqual(await iterator.next(), { done: true, value: undefined });
  assert.equal(calls, 1);
});

test('client configuration, filters and retry policies are captured before lazy consumption', async () => {
  const seen: Array<{ url: string; token: string | null }> = [];
  const client = createClient('https://before.test', { token: 'before', retry: { maxAttempts: 1, backoffMs: 1 }, fetch: async (url, init) => {
    seen.push({ url: String(url), token: new Headers(init?.headers).get('Authorization') });
    return seen.length === 1 ? page(['first'], cursor) : page(['next']);
  } });
  const params = { app: 'original', status: 'running' as const, before: 'seed', limit: 1 };
  const options = { retry: { maxAttempts: 1, backoffMs: 1 }, maxPages: 2 };
  const iterator = client.deployments.iterateBuilds(params, options);
  params.app = 'changed'; params.before = 'changed'; options.maxPages = 0; options.retry.maxAttempts = 9;
  client.setToken('after'); client.setBaseURL('https://after.test');
  assert.deepEqual((await collect(iterator)).map(value => value.id), ['first', 'next']);
  assert.ok(seen.every(value => value.url.startsWith('https://before.test/') && value.token === 'Bearer before'));
  assert.ok(seen.every(value => new URL(value.url).searchParams.get('app') === 'original'));
  assert.equal(new URL(seen[0]!.url).searchParams.get('before'), 'seed');
  assert.equal(new URL(seen[1]!.url).searchParams.get('before'), cursor);
  await collect(client.deployments.iterateBuilds({}, { maxPages: 1 }));
  assert.ok(seen[2]!.url.startsWith('https://after.test/')); assert.equal(seen[2]!.token, 'Bearer after');
});

test('invalid budgets and page sizes reject before HTTP', async () => {
  let calls = 0;
  const client = createClient('https://invalid.test', { fetch: async () => { calls++; return page([]); } });
  for (const key of ['maxPages', 'maxItems'] as const) {
    for (const value of [-1, 0.1, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      await assert.rejects(client.deployments.iterateBuilds({}, { [key]: value }).next(), TypeError);
    }
  }
  for (const value of [-1, 0.5, NaN, Infinity, 2 ** 31]) await assert.rejects(client.deployments.iterateBuilds({}, { timeoutMs: value }).next(), TypeError);
  for (const value of [0, 201, 1.5, NaN]) await assert.rejects(client.deployments.iterateBuilds({ limit: value }).next(), TypeError);
  assert.equal(calls, 0);
});

test('concurrent retry overrides stay local, are copied, and count HTTP retries within one page', async () => {
  const counts = new Map<string, number>();
  const attempts: number[] = [];
  const client = createClient('https://policies.test', { retry: { maxAttempts: 3, backoffMs: 1 }, fetch: async url => {
    const app = new URL(String(url)).searchParams.get('app')!;
    const count = (counts.get(app) ?? 0) + 1; counts.set(app, count);
    return count < (app === 'default' ? 3 : 2) ? Response.json({ code: 'capacity_unavailable' }, { status: 503 }) : page([app], cursor);
  } });
  const options = { maxPages: 1, retry: { maxAttempts: 2, backoffMs: 1 }, onResponse: (info: ResponseInfo) => { attempts.push(info.attempt); } };
  const two = client.deployments.iterateBuilds({ app: 'two' }, options);
  options.retry.maxAttempts = 1;
  const [success, failure, defaults] = await Promise.allSettled([
    collect(two), collect(client.deployments.iterateBuilds({ app: 'one' }, { retry: false })),
    collect(client.deployments.iterateBuilds({ app: 'default' }, { maxPages: 1 })),
  ]);
  assert.equal(success.status, 'fulfilled'); assert.equal(defaults.status, 'fulfilled');
  assert.equal(failure.status, 'rejected'); if (failure.status === 'rejected') assert.ok(failure.reason instanceof ErrCapacity);
  assert.deepEqual(Object.fromEntries(counts), { two: 2, one: 1, default: 3 });
  assert.deepEqual(attempts, [1, 2]);
});

test('a traversal deadline leaves a concurrent traversal with timeout disabled running', { timeout: 5000 }, async () => {
  const release = deferred();
  let longFinished = false;
  const client = createClient('https://independent.test', { timeoutMs: 10, fetch: async url => {
    if (new URL(String(url)).searchParams.get('app') === 'short') return new Promise<Response>(() => {});
    await release.promise; return page(['long']);
  } });
  const short = collect(client.deployments.iterateBuilds({ app: 'short' }));
  const long = collect(client.deployments.iterateBuilds({ app: 'long' }, { timeoutMs: 0 })).then(items => { longFinished = true; return items; });
  await assert.rejects(short, { name: 'TimeoutError' });
  assert.equal(longFinished, false); release.resolve();
  assert.equal((await long)[0]!.id, 'long');
});

test('malformed and oversized pages fail without truncating away a resumable boundary', async () => {
  for (const body of [null, {}, { items: {} }, { items: [], next_before: 42 }, { items: [{ id: 'a' }, { id: 'b' }], next_before: cursor }]) {
    const client = createClient('https://bad-page.test', { fetch: async () => Response.json(body) });
    await assert.rejects(client.deployments.iterateDeploymentPages({ limit: 1 }).next(), error => error instanceof PaginationError && error.code === 'invalid_page');
  }
});

test('initial cursor repetition and longer cursor cycles fail without leaking cursors', async () => {
  const client = createClient('https://cycle.test', { fetch: async () => page(['repeat'], cursor) });
  await assert.rejects(client.deployments.iterateDeployments({ before: cursor }).next(), error =>
    error instanceof PaginationError && error.code === 'repeated_cursor' && !error.message.includes(cursor));
  let calls = 0;
  const cycle = createClient('https://cycle.test', { fetch: async () => page([String(++calls)], calls % 2 ? 'one' : 'two') });
  const iterator = cycle.deployments.iterateBuilds();
  assert.equal((await iterator.next()).value?.id, '1');
  assert.equal((await iterator.next()).value?.id, '2');
  await assert.rejects(iterator.next(), error => error instanceof PaginationError && error.code === 'repeated_cursor');
  assert.equal(calls, 3);
});

test('caller abort works before HTTP and between buffered items with the original reason', async () => {
  let calls = 0;
  const reason = new Error('caller stopped');
  const client = createClient('https://abort.test', { fetch: async () => { calls++; return page(['a', 'b'], cursor); } });
  await assert.rejects(client.deployments.iterateBuilds({}, { signal: AbortSignal.abort(reason) }).next(), error => error === reason);
  assert.equal(calls, 0);
  const controller = new AbortController();
  const iterator = client.deployments.iterateDeployments({}, { signal: controller.signal });
  await iterator.next(); controller.abort(reason);
  await assert.rejects(iterator.next(), error => error === reason);
  assert.equal(calls, 1);
  assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
});

for (const action of ['return', 'throw', 'abort'] as const) {
  test(`${action} interrupts a pending next() instead of waiting for stalled headers`, { timeout: 5000 }, async () => {
    const started = deferred();
    let aborted = 0;
    const client = createClient('https://pending.test', { fetch: async (_url, init) => {
      started.resolve();
      return new Promise<Response>((_resolve, reject) => init!.signal!.addEventListener('abort', () => { aborted++; reject(init!.signal!.reason); }, { once: true }));
    } });
    const controller = new AbortController(), reason = new Error('stop pending');
    const iterator = client.deployments.iterateBuilds({}, { signal: controller.signal });
    const pending = assert.rejects(iterator.next(), error => action === 'return' ? error instanceof DOMException && error.name === 'AbortError' : error === reason);
    await started.promise;
    if (action === 'return') assert.deepEqual(await iterator.return(), { done: true, value: undefined });
    else if (action === 'throw') await assert.rejects(iterator.throw(reason), error => error === reason);
    else controller.abort(reason);
    await pending;
    assert.equal(aborted, 1);
    assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
  });
}

test('deadline expires while reading a blocked custom response body', { timeout: 5000 }, async t => {
  t.mock.method(console, 'error', () => {});
  let cancelled = 0;
  const client = createClient('https://body.test', { fetch: async () => new Response(new ReadableStream({
    start(controller) { controller.enqueue(new TextEncoder().encode('{"items":[')); },
    cancel() { cancelled++; },
  }), { headers: { 'Content-Type': 'application/json' } }) });
  await assert.rejects(client.deployments.iterateBuilds({}, { timeoutMs: 20 }).next(), { name: 'TimeoutError' });
  await nextTurn(); assert.equal(cancelled, 1);
});

test('one traversal deadline includes earlier pages and Retry-After waits', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let clock = 1000, calls = 0;
  t.mock.method(performance, 'now', () => clock);
  const responded = deferred();
  const reports: ResponseInfo[] = [];
  const client = createClient('https://deadline.test', { retry: { maxAttempts: 3, backoffMs: 1 }, fetch: async () => {
    calls++;
    return calls === 1 ? page(['first'], cursor) : new Response('{}', { status: 503, headers: { 'Retry-After': '60' } });
  } });
  const iterator = client.deployments.iterateDeploymentPages({}, { timeoutMs: 100, onResponse: info => { reports.push(info); if (calls === 2) responded.resolve(); } });
  clock = 10_000; // Time before first next() consumes no budget.
  await iterator.next(); clock += 80;
  const rejected = assert.rejects(iterator.next(), { name: 'TimeoutError' });
  await responded.promise; await nextTurn();
  t.mock.timers.tick(19); await nextTurn(); assert.equal(calls, 2);
  t.mock.timers.tick(1); await rejected;
  assert.equal(calls, 2); assert.deepEqual(reports.map(info => info.attempt), [1, 1]);
});

test('consumer pauses count toward the deadline without keeping idle timers or listeners', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let clock = 1000, calls = 0;
  t.mock.method(performance, 'now', () => clock);
  const controller = new AbortController(), signals: AbortSignal[] = [];
  const client = createClient('https://pause.test', { fetch: async (_url, init) => { calls++; signals.push(init!.signal!); return page(['a', 'b'], cursor); } });
  const iterator = client.deployments.iterateBuilds({}, { signal: controller.signal, timeoutMs: 100 });
  await iterator.next();
  assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
  clock += 101; t.mock.timers.tick(101);
  assert.ok(signals.every(signal => !signal.aborted));
  await assert.rejects(iterator.next(), { name: 'TimeoutError' });
  assert.equal(calls, 1);
  const unlimited = client.deployments.iterateBuilds({}, { timeoutMs: 0, maxPages: 1 });
  await unlimited.next(); clock += 1_000_000;
  assert.equal((await unlimited.next()).value?.id, 'b');
  assert.equal((await unlimited.next()).done, true);
});

test('HTTP errors retain their type and never restart pagination from the beginning', async () => {
  let calls = 0;
  const client = createClient('https://errors.test', { fetch: async url => {
    if (++calls === 1) return page(['first'], cursor);
    assert.equal(new URL(String(url)).searchParams.get('before'), cursor);
    return Response.json({ code: 'not_found', tx_id: 'original-tx' }, { status: 404 });
  } });
  const iterator = client.deployments.iterateBuilds();
  await iterator.next();
  await assert.rejects(iterator.next(), error => error instanceof ErrNotFound && error.txId === 'original-tx');
  assert.equal(calls, 2);
  assert.equal((await iterator.next()).done, true);
});

test('concurrent HTTP traversals keep accounts, cursors, policies and response observers separate', { timeout: 10_000 }, async t => {
  const originalFetch = globalThis.fetch, originalConfig = { ...OpenAPI };
  const requests: Array<{ token: string; app: string; before: string | null }> = [];
  const server = createServer(async (req, res) => {
    await nextTurn();
    const url = new URL(req.url!, 'http://local'), app = url.searchParams.get('app')!;
    const token = req.headers.authorization!;
    requests.push({ token, app, before: url.searchParams.get('before') });
    const n = Number(app.slice(1));
    res.writeHead(200, { 'Content-Type': 'application/json', 'X-Request-ID': `11111111-1111-4111-8111-${String(n).padStart(12, '0')}` });
    res.end(JSON.stringify({ items: [{ id: `${app}-${url.searchParams.has('before') ? 2 : 1}` }], next_before: url.searchParams.has('before') ? '' : `${app}/opaque+%` }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(async () => { const closed = once(server, 'close'); server.close(); server.closeAllConnections(); await closed; });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  const base = `http://127.0.0.1:${address.port}`;
  const clients = [createClient(base, { token: 'a' }), createClient(base, { token: 'b' })];
  await Promise.all(Array.from({ length: 20 }, async (_, i) => {
    const responses: ResponseInfo[] = [], app = `t${i}`;
    const iterator = clients[i % 2]!.deployments.iterateBuilds({ app }, { maxPages: i % 2 + 1, retry: false, onResponse: info => { responses.push(info); } });
    const items = await collect(iterator);
    assert.deepEqual(items.map(item => item.id), i % 2 ? [`${app}-1`, `${app}-2`] : [`${app}-1`]);
    assert.equal(responses.length, items.length);
    assert.ok(responses.every(info => info.requestId?.endsWith(String(i).padStart(12, '0'))));
  }));
  assert.equal(requests.length, 30);
  assert.ok(requests.every(value => value.token === `Bearer ${Number(value.app.slice(1)) % 2 ? 'b' : 'a'}` && (!value.before || value.before === `${value.app}/opaque+%`)));
  assert.equal(globalThis.fetch, originalFetch); assert.deepEqual(OpenAPI, originalConfig);
});

test('typed helpers preserve generated return types and legacy list signatures', () => {
  const client = createClient('https://types.test');
  assert.ok(Object.isFrozen(client.deployments));
  if (false) {
    const a: AsyncGenerator<DeploymentResponse> = client.deployments.iterateAppDeployments({ slug: 'api' });
    const b: AsyncGenerator<DeploymentListResponse> = client.deployments.iterateDeploymentPages();
    const c: AsyncGenerator<BuildResponse> = client.deployments.iterateBuilds({ status: 'running' });
    const d: AsyncGenerator<BuildListResponse> = createPreviewClient('https://types.test').deployments.iterateBuildPages();
    const legacy: CancelablePromise<DeploymentListResponse> = client.deployments.listAppDeployments({ slug: 'api' });
    void a; void b; void c; void d; void legacy;
    // @ts-expect-error app slug remains required
    client.deployments.iterateAppDeployments({});
    // @ts-expect-error build status remains generated
    client.deployments.iterateBuilds({ status: 'invented' });
    // @ts-expect-error pagination limits stay outside generated API parameters
    client.deployments.getBuilds({ maxPages: 2 });
    // @ts-expect-error preview namespaces stay excluded
    client.flags;
  }
});
