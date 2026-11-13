import test, { type TestContext } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { createClient, FaaSClient, AppsService, OpenAPI, ErrCapacity, ErrNotFound, type AppResponse, type CancelablePromise } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

async function endpoint(t: TestContext, handle: (req: IncomingMessage, res: ServerResponse) => void | Promise<void>): Promise<string> {
  const server = createServer((req, res) => { Promise.resolve(handle(req, res)).catch(error => res.destroy(error as Error)); });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    const closed = once(server, 'close');
    server.close();
    server.closeAllConnections();
    await closed;
  });
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  return `http://127.0.0.1:${address.port}`;
}

function json(res: ServerResponse, body: unknown) {
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify(body));
}

const unavailable = () => Response.json({ code: 'capacity_unavailable', status: 503 }, { status: 503 });

test('48 concurrent calls keep endpoints and credentials private, including detached methods', { timeout: 10_000 }, async t => {
  const originalFetch = globalThis.fetch;
  const originalConfig = { ...OpenAPI };
  const seen: Array<{ owner: string; token: string | undefined; path: string | undefined }> = [];
  const allArrived = deferred();
  const makeEndpoint = (owner: string) => endpoint(t, async (req, res) => {
    seen.push({ owner, token: req.headers.authorization, path: req.url });
    if (seen.length === 48) allArrived.resolve();
    await allArrived.promise;
    json(res, { id: owner, slug: decodeURIComponent(req.url!.split('/').at(-1)!) });
  });
  const [urlA, urlB] = await Promise.all([makeEndpoint('a'), makeEndpoint('b')]);
  const a = createClient(`${urlA}/`, { token: 'token-a' });
  const b = createClient(urlB, { token: 'token-b' });
  const getA = a.apps.getApp;
  const getB = b.apps.getApp;
  const responses = await Promise.all(Array.from({ length: 24 }, (_, i) => Promise.all([
    getA({ slug: `a/${i}` }), getB({ slug: `b/${i}` }),
  ])));
  for (const [left, right] of responses) {
    assert.equal(left.id, 'a');
    assert.equal(right.id, 'b');
    assert.match(left.slug, /^a\//);
    assert.match(right.slug, /^b\//);
  }
  assert.equal(seen.length, 48);
  for (const request of seen) {
    assert.equal(request.token, `Bearer token-${request.owner}`);
    assert.match(request.path!, new RegExp(`/v1/apps/${request.owner}%2F\\d+$`));
  }
  assert.equal(globalThis.fetch, originalFetch);
  assert.deepEqual(OpenAPI, originalConfig);
});

test('clients coexist with legacy static callers and leave ordinary fetch alone', async t => {
  const originalConfig = { ...OpenAPI };
  const originalFetch = globalThis.fetch;
  const seen: Array<{ path: string | undefined; token: string | undefined; key: string | undefined }> = [];
  const url = await endpoint(t, (req, res) => {
    seen.push({ path: req.url, token: req.headers.authorization, key: req.headers['idempotency-key'] as string | undefined });
    json(res, { id: req.headers.authorization ?? 'anonymous' });
  });
  const isolated = createClient(url, { token: 'isolated' });
  isolated.setIdempotencyKey('isolated-key');
  await fetch(`${url}/ordinary`, { method: 'POST' });
  assert.equal(globalThis.fetch, originalFetch);
  const legacy = new FaaSClient(url, { token: 'legacy' });
  t.after(() => { legacy.uninstall(); globalThis.fetch = originalFetch; Object.assign(OpenAPI, originalConfig); });
  const legacyFetch = globalThis.fetch;
  legacy.setIdempotencyKey('legacy-key');
  const duringMigration = createClient(url, { token: 'new' });
  duringMigration.setToken('new-rotated');
  const detachedLegacy = AppsService.getApp;
  const [old, next, first] = await Promise.all([
    detachedLegacy({ slug: 'legacy' }),
    duringMigration.apps.createApp({ requestBody: { slug: 'new', type: 'app' } }),
    isolated.apps.createApp({ requestBody: { slug: 'isolated', type: 'app' } }),
  ]);
  assert.equal(old.id, 'Bearer legacy');
  assert.equal(next.id, 'Bearer new-rotated');
  assert.equal(first.id, 'Bearer isolated');
  assert.equal(globalThis.fetch, legacyFetch);
  assert.equal(OpenAPI.TOKEN, 'legacy');
  assert.deepEqual(seen[0], { path: '/ordinary', token: undefined, key: undefined });
  const newer = seen.find(request => request.token === 'Bearer new-rotated')!;
  assert.ok(newer.key && newer.key !== 'legacy-key');
  assert.equal(seen.find(request => request.token === 'Bearer isolated')!.key, 'isolated-key');
  legacy.uninstall();
  assert.equal((await duringMigration.apps.getApp({ slug: 'after-uninstall' })).id, 'Bearer new-rotated');
});

test('in-flight calls retain token, base URL, key and retry policy while future calls use updates', async () => {
  const started = deferred();
  const release = deferred();
  const calls: Array<{ url: string; token: string | null; key: string | null }> = [];
  const logs: unknown[][] = [];
  const retry = { maxAttempts: 2, backoffMs: 1 };
  const client = createClient('https://first.test/prefix/', {
    token: 'first', retry,
    logger: { debug: (...args) => logs.push(args), info() {}, warn() {}, error() {} },
    fetch: async (url, init) => {
      const headers = new Headers(init?.headers);
      calls.push({ url: String(url), token: headers.get('Authorization'), key: headers.get('Idempotency-Key') });
      if (calls.length === 1) { started.resolve(); await release.promise; return unavailable(); }
      return Response.json({ id: 'ok' });
    },
  });
  client.setIdempotencyKey('first-key');
  const pending = client.apps.createApp({ requestBody: { slug: 'first', type: 'app' } });
  await started.promise;
  client.setToken('second');
  client.setBaseURL('https://second.test/');
  client.setIdempotencyKey('second-key');
  retry.maxAttempts = 1; // Options supplied by another caller cannot change this client's policy.
  release.resolve();
  await pending;
  await client.apps.createApp({ requestBody: { slug: 'second', type: 'app' } });
  assert.deepEqual(calls, [
    { url: 'https://first.test/prefix/v1/apps', token: 'Bearer first', key: 'first-key' },
    { url: 'https://first.test/prefix/v1/apps', token: 'Bearer first', key: 'first-key' },
    { url: 'https://second.test/v1/apps', token: 'Bearer second', key: 'second-key' },
  ]);
  assert.equal(logs.filter(args => args[0] === 'faas http request').length, 3);
  client.setToken(undefined);
  client.setIdempotencyKey(undefined);
  await client.apps.createApp({ requestBody: { slug: 'anonymous', type: 'app' }, idempotencyKey: 'explicit' });
  assert.equal(calls.at(-1)!.token, null);
  assert.equal(calls.at(-1)!.key, 'explicit');
});

test('concurrent clients own retry budgets, loggers, and per-call mutation keys', async () => {
  const keys: string[] = [];
  const aLogs: unknown[][] = [];
  const bLogs: unknown[][] = [];
  let bAttempts = 0;
  const logger = (target: unknown[][]) => ({ debug: (...args: unknown[]) => target.push(args), info() {}, warn() {}, error() {} });
  const a = createClient('https://a.test', {
    retry: { maxAttempts: 2, backoffMs: 1 }, logger: logger(aLogs),
    fetch: async (_url, init) => {
      keys.push(new Headers(init?.headers).get('Idempotency-Key')!);
      return keys.length % 2 ? unavailable() : Response.json({ id: 'a' });
    },
  });
  const b = createClient('https://b.test', {
    retry: { maxAttempts: 1, backoffMs: 1 }, logger: logger(bLogs),
    fetch: async () => { bAttempts++; return unavailable(); },
  });
  const [success, failure] = await Promise.allSettled([
    a.apps.createApp({ requestBody: { slug: 'a', type: 'app' } }),
    b.apps.createApp({ requestBody: { slug: 'b', type: 'app' } }),
  ]);
  assert.equal(success.status, 'fulfilled');
  assert.equal(failure.status, 'rejected');
  if (failure.status === 'rejected') assert.ok(failure.reason instanceof ErrCapacity);
  assert.equal(bAttempts, 1);
  await a.apps.createApp({ requestBody: { slug: 'another', type: 'app' } });
  assert.equal(keys[0], keys[1]);
  assert.equal(keys[2], keys[3]);
  assert.notEqual(keys[0], keys[2]);
  assert.match(keys[0]!, /^[0-9a-f-]{36}$/);
  assert.equal(aLogs.filter(args => args[0] === 'faas http request').length, 4);
  assert.equal(bLogs.filter(args => args[0] === 'faas http request').length, 1);
});

test('cancelling a request does not cancel another client, and timeout budgets are independent', { timeout: 5000 }, async t => {
  const slowStarted = deferred();
  const slowAborted = deferred();
  const url = await endpoint(t, (req, res) => {
    if (req.url?.endsWith('/cancel')) {
      res.on('close', slowAborted.resolve);
      slowStarted.resolve();
    } else if (req.url?.endsWith('/timeout')) {
      // Leave this response open for the client's deadline.
    } else json(res, { id: 'healthy' });
  });
  const a = createClient(url, { timeoutMs: 0 });
  const b = createClient(url, { timeoutMs: 500 });
  const pending = a.apps.getApp({ slug: 'cancel' });
  const rejected = assert.rejects(pending, { name: 'CancelError' });
  await slowStarted.promise;
  pending.cancel();
  await rejected;
  await slowAborted.promise;
  const short = createClient(url, { timeoutMs: 15 });
  const [timedOut, healthy] = await Promise.allSettled([
    short.apps.getApp({ slug: 'timeout' }), b.apps.getApp({ slug: 'healthy' }),
  ]);
  assert.equal(timedOut.status, 'rejected');
  if (timedOut.status === 'rejected') assert.equal(timedOut.reason.name, 'TimeoutError');
  assert.equal(healthy.status, 'fulfilled');
});

test('typed errors and binary responses use the isolated transport', async () => {
  const client = createClient('https://binary.test', {
    fetch: async url => String(url).includes('/billing/')
      ? new Response(new Uint8Array([0, 255, 7]), { headers: { 'Content-Type': 'application/zip' } })
      : Response.json({ status: 404, code: 'not_found', tx_id: 'isolated-tx' }, { status: 404 }),
  });
  const download = await client.billing.exportFocusInvoices({ month: '2026-11' });
  assert.ok(download instanceof Blob);
  assert.deepEqual(new Uint8Array(await download.arrayBuffer()), new Uint8Array([0, 255, 7]));
  await assert.rejects(client.apps.getApp({ slug: 'missing' }), error => {
    assert.ok(error instanceof ErrNotFound);
    assert.equal(error.txId, 'isolated-tx');
    return true;
  });
});

test('public and preview client surfaces preserve generated signatures and capability boundaries', () => {
  const client = createClient('https://public.test');
  const preview = createPreviewClient('https://preview.test');
  assert.equal('flags' in client, false);
  assert.equal(typeof preview.flags, 'object');
  assert.ok(Object.isFrozen(client) && Object.isFrozen(client.apps));
  // Compile-time checks exercise the published signatures without making requests.
  if (false) {
    const result: CancelablePromise<AppResponse> = client.apps.getApp({ slug: 'typed' });
    void result;
    // @ts-expect-error slug remains required
    client.apps.getApp({});
    // @ts-expect-error preview-only namespace is excluded from the default entry
    client.flags;
    // @ts-expect-error generated static names are accessed through namespaces
    client.AppsService;
    // @ts-expect-error clients expose no global installation lifecycle
    client.uninstall();
  }
});

test('invalid endpoint updates fail before replacing the current configuration', async () => {
  const seen: string[] = [];
  const client = createClient('https://valid.test/', { fetch: async url => { seen.push(String(url)); return Response.json({}); } });
  for (const url of ['file:///tmp/api', 'https://user:secret@api.test', 'https://api.test?token=x', 'https://api.test#fragment', 'https://api.test?', 'https://api.test#']) {
    assert.throws(() => createClient(url), TypeError);
    assert.throws(() => client.setBaseURL(url), TypeError);
  }
  await client.apps.getApp({ slug: 'still-valid' });
  assert.deepEqual(seen, ['https://valid.test/v1/apps/still-valid']);
});
