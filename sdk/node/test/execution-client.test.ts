import test, { type TestContext } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { createClient as createPublicClient, OpenAPI } from '../src/index.js';
import { createClient, FaaSClient, runExecution, decodeExecutionArtifact, ErrUnauthorized, type CreateExecutionRequest, type ExecutionEvent, type ExecutionResponse } from '../src/preview.js';

const body: CreateExecutionRequest = { runtime: 'node22', source: "console.log('hello')" };
const fast = { retryInitialMs: 0, retryMaxMs: 0 };
const receipt = (id = 'run/1', status = 'queued') => ({ id, status });
const frame = (id: number, event: string, data: object) => `id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
const sse = (text: string) => new Response(text, { headers: { 'Content-Type': 'text/event-stream' } });
const terminal = () => sse(frame(3, 'terminal', { status: 'succeeded' }));

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

async function endpoint(t: TestContext, handle: (req: IncomingMessage, res: ServerResponse) => Promise<void> | void) {
  const server = createServer((req, res) => {
    void Promise.resolve().then(() => handle(req, res)).catch(error => res.destroy(error as Error));
  });
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

function json(res: ServerResponse, value: unknown, status = 200) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(value));
}

async function collect(events: AsyncIterable<ExecutionEvent>) {
  const result: ExecutionEvent[] = [];
  for await (const event of events) result.push(event);
  return result;
}

test('two clients submit, reconnect and collect receipts independently alongside legacy callers', { timeout: 10_000 }, async t => {
  const originalFetch = globalThis.fetch;
  const originalConfig = { ...OpenAPI };
  const legacyCalls: string[] = [];
  const legacy = new FaaSClient('https://legacy.test', {
    token: 'legacy-token',
    fetch: async (url, init) => {
      legacyCalls.push(String(url));
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer legacy-token');
      return String(url).includes('/events') ? terminal() : Response.json(receipt('legacy', init?.method === 'POST' ? 'queued' : 'succeeded'));
    },
  });
  t.after(() => { legacy.uninstall(); globalThis.fetch = originalFetch; Object.assign(OpenAPI, originalConfig); });
  const legacyFetch = globalThis.fetch;
  const bothStreaming = deferred();
  let streamsStarted = 0;
  const seen: Array<{ owner: string; path: string; method: string; token: string | undefined; key: unknown; accept: unknown }> = [];
  const makeEndpoint = (owner: string) => endpoint(t, async (req, res) => {
    const path = req.url!;
    seen.push({ owner, path, method: req.method!, token: req.headers.authorization, key: req.headers['idempotency-key'], accept: req.headers.accept });
    if (req.method === 'POST') {
      for await (const _chunk of req) { /* drain the upload */ }
      json(res, receipt(`run/${owner}`), 202);
    } else if (path.includes('/events')) {
      res.setHeader('Content-Type', 'text/event-stream');
      if (!path.includes('after=')) {
        if (++streamsStarted === 2) bothStreaming.resolve();
        await bothStreaming.promise;
        res.end(frame(1, 'status', { status: 'running' }) + frame(2, 'stdout', { chunk: owner }));
      } else {
        // Some transports replay an overlapping batch after reconnecting.
        res.end(frame(2, 'stdout', { chunk: 'duplicate' }) + frame(3, 'terminal', { status: 'succeeded' }));
      }
    } else json(res, { ...receipt(`run/${owner}`, 'succeeded'), artifacts: [{ name: 'result.txt', content: 'aGk=', size_bytes: 2, sha256: 'sha256:8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4' }] });
  });
  const [aURL, bURL] = await Promise.all([makeEndpoint('a'), makeEndpoint('b')]);
  const a = createClient(`${aURL}/prefix-a/`, { token: 'a-token' });
  const b = createClient(`${bURL}/prefix-b`, { token: 'b-token' });
  b.setIdempotencyKey('b-default');
  const aEvents: ExecutionEvent[] = [];
  const bEvents: ExecutionEvent[] = [];
  const [aResult, bResult, legacyResult] = await Promise.all([
    a.runExecution(body, { ...fast, idempotencyKey: 'a-explicit', onEvent: e => { aEvents.push(e); } }),
    b.runExecution(body, { ...fast, onEvent: e => { bEvents.push(e); } }),
    runExecution(body, fast),
  ]);
  assert.equal(aResult.id, 'run/a');
  assert.equal(bResult.id, 'run/b');
  assert.equal(legacyResult.id, 'legacy');
  assert.deepEqual(decodeExecutionArtifact(aResult.artifacts![0]!), Buffer.from('hi'));
  for (const [owner, events] of [['a', aEvents], ['b', bEvents]] as const) {
    assert.deepEqual(events.map(e => e.id), [1, 2, 3]);
    assert.equal(events[1]!.data.chunk, owner);
    const requests = seen.filter(request => request.owner === owner);
    assert.equal(requests.length, 4);
    assert.equal(requests.filter(request => request.method === 'POST').length, 1);
    assert.ok(requests.every(request => request.token === `Bearer ${owner}-token` && request.path.startsWith(`/prefix-${owner}/v1/executions`)));
    assert.equal(requests[0]!.key, owner === 'a' ? 'a-explicit' : 'b-default');
    assert.equal(requests[2]!.path, `/prefix-${owner}/v1/executions/run%2F${owner}/events?after=2&limit=100`);
    assert.ok(requests.filter(request => request.path.includes('/events')).every(request => request.accept === 'text/event-stream' && request.key === undefined));
  }
  assert.equal(legacyCalls.length, 3);
  assert.equal(globalThis.fetch, legacyFetch);
  assert.equal(OpenAPI.TOKEN, 'legacy-token');
});

test('one workflow snapshot survives rotation, including a detached lazy iterator and final read', async () => {
  const calls: Array<{ url: string; token: string | null; key: string | null }> = [];
  let streamCalls = 0;
  const client = createClient('https://first.test/prefix', {
    token: 'first',
    fetch: async (url, init) => {
      const headers = new Headers(init?.headers);
      calls.push({ url: String(url), token: headers.get('Authorization'), key: headers.get('Idempotency-Key') });
      if (String(url).includes('/events')) return ++streamCalls === 1 ? sse(frame(1, 'status', { status: 'running' })) : terminal();
      return Response.json(receipt());
    },
  });
  client.setIdempotencyKey('first-key');
  const run = client.runExecution;
  await run(body, {
    ...fast,
    onEvent(event) {
      if (event.id === 1) {
        client.setToken('second');
        client.setBaseURL('https://second.test');
        client.setIdempotencyKey('second-key');
      }
    },
  });
  assert.equal(calls.length, 4);
  assert.ok(calls.every(call => call.url.startsWith('https://first.test/prefix/') && call.token === 'Bearer first'));
  assert.equal(calls[0]!.key, 'first-key');
  const watch = client.watchExecution;
  const iterator = watch('next', fast);
  client.setToken('third');
  client.setBaseURL('https://third.test');
  await collect(iterator);
  assert.equal(calls.at(-1)!.url, 'https://second.test/v1/executions/next/events?limit=100');
  assert.equal(calls.at(-1)!.token, 'Bearer second');
});

test('a saved execution ID and cursor resume observation without submission', async () => {
  const calls: string[] = [];
  const client = createClient('https://resume.test', { fetch: async (url, init) => {
    calls.push(`${init?.method} ${url}`);
    return sse(frame(1, 'status', { status: 'running' }) + frame(2, 'stdout', { chunk: 'already processed' }) + frame(3, 'terminal', { status: 'succeeded' }));
  } });
  const events = await collect(client.watchExecution('saved/id', { ...fast, after: 2, limit: 50 }));
  assert.deepEqual(events.map(event => event.id), [3]);
  assert.deepEqual(calls, ['GET https://resume.test/v1/executions/saved%2Fid/events?after=2&limit=50']);
});

for (const phase of ['submission', 'stream', 'receipt'] as const) {
  test(`cancelling ${phase} stops only that caller's HTTP request`, { timeout: 10_000 }, async t => {
    const started = deferred();
    const disconnected = deferred();
    const counts = { a: 0, b: 0 };
    const url = await endpoint(t, async (req, res) => {
      const owner = req.headers.authorization === 'Bearer a' ? 'a' : 'b';
      if (req.method === 'POST') {
        counts[owner]++;
        for await (const _chunk of req) { /* drain the upload */ }
      }
      const isStream = req.url!.includes('/events');
      const isReceipt = req.method === 'GET' && !isStream;
      const held = owner === 'a' && ((phase === 'submission' && req.method === 'POST') || (phase === 'stream' && isStream) || (phase === 'receipt' && isReceipt));
      if (held) {
        res.on('close', disconnected.resolve);
        if (isStream) { res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.write(frame(1, 'status', { status: 'running' })); }
        started.resolve();
        return;
      }
      if (isStream) { res.writeHead(200, { 'Content-Type': 'text/event-stream' }); res.end(frame(3, 'terminal', { status: 'succeeded' })); }
      else json(res, receipt(owner, isReceipt ? 'succeeded' : 'queued'), req.method === 'POST' ? 202 : 200);
    });
    const controller = new AbortController();
    const a = createClient(url, { token: 'a' });
    const b = createClient(url, { token: 'b' });
    const reason = new Error(`stop ${phase}`);
    const pendingA = a.runExecution(body, { ...fast, signal: controller.signal });
    const rejected = assert.rejects(pendingA, error => error === reason);
    const pendingB = b.runExecution(body, fast);
    await started.promise;
    controller.abort(reason);
    await rejected;
    await disconnected.promise;
    assert.equal((await pendingB).id, 'b');
    assert.deepEqual(counts, { a: 1, b: 1 });
  });
}

test('an abort wakes a pending custom SSE body read without another connection', { timeout: 5000 }, async () => {
  const started = deferred();
  let cancelled = 0;
  let calls = 0;
  const controller = new AbortController();
  const client = createClient('https://custom.test', { fetch: async () => {
    calls++;
    const response = new Response(new ReadableStream({ cancel() { cancelled++; } }));
    started.resolve();
    return response;
  } });
  const pending = collect(client.watchExecution('run', { ...fast, signal: controller.signal }));
  const reason = new Error('stop custom stream');
  const rejected = assert.rejects(pending, error => error === reason);
  await started.promise;
  await nextTurn();
  controller.abort(reason);
  await rejected;
  assert.equal(cancelled, 1);
  assert.equal(calls, 1);
});

test('aborting reconnect backoff does not reconnect or submit work', { timeout: 5000 }, async () => {
  let calls = 0;
  const client = createClient('https://backoff.test', { fetch: async () => { calls++; return sse(frame(1, 'status', { status: 'running' })); } });
  const controller = new AbortController();
  const iterator = client.watchExecution('run', { signal: controller.signal, retryInitialMs: 60_000 });
  assert.equal((await iterator.next()).value?.id, 1);
  const pending = iterator.next();
  const reason = new Error('stop backoff');
  const rejected = assert.rejects(pending, error => error === reason);
  await nextTurn();
  controller.abort(reason);
  await rejected;
  assert.equal(calls, 1);
});

test('early iterator exit and callback failure cancel their response bodies', async () => {
  for (const mode of ['break', 'callback'] as const) {
    let cancelled = 0;
    let reads = 0;
    const client = createClient('https://cleanup.test', { fetch: async (url, init) => {
      if (init?.method === 'POST') return Response.json(receipt());
      if (!String(url).includes('/events')) { reads++; return Response.json(receipt()); }
      return new Response(new ReadableStream({
        start(controller) { controller.enqueue(new TextEncoder().encode(frame(1, 'status', { status: 'running' }))); },
        cancel() { cancelled++; },
      }));
    } });
    if (mode === 'break') {
      for await (const _event of client.watchExecution('run', fast)) break;
    } else {
      const reason = new Error('callback failed');
      await assert.rejects(client.runExecution(body, { ...fast, onEvent() { throw reason; } }), error => error === reason);
    }
    assert.equal(cancelled, 1);
    assert.equal(reads, 0);
  }
});

test('a transient error after connection resumes the cursor without repeating submission', async () => {
  const calls: string[] = [];
  let streams = 0;
  const client = createClient('https://retry.test', { fetch: async (url, init) => {
    calls.push(`${init?.method} ${url}`);
    if (String(url).includes('/events')) {
      streams++;
      if (streams === 1) return sse(frame(1, 'status', { status: 'running' }));
      if (streams === 2) return Response.json({ code: 'capacity_unavailable' }, { status: 503 });
      return terminal();
    }
    return Response.json(receipt());
  } });
  await client.runExecution(body, fast);
  assert.equal(calls.filter(call => call.startsWith('POST')).length, 1);
  assert.equal(calls.filter(call => call.includes('events?after=1')).length, 2);
  assert.equal(calls.at(-1), 'GET https://retry.test/v1/executions/run%2F1');
});

test('authorization and malformed event failures fail without reconnecting or final reads', async () => {
  for (const mode of ['authorization', 'malformed'] as const) {
    let streams = 0;
    let reads = 0;
    const client = createClient('https://errors.test', { fetch: async (url, init) => {
      if (init?.method === 'POST') return Response.json(receipt());
      if (!String(url).includes('/events')) { reads++; return Response.json(receipt()); }
      streams++;
      return mode === 'authorization' ? Response.json({ code: 'unauthorized' }, { status: 401 }) : sse('id: 1\nevent: status\ndata: invalid-json\n\n');
    } });
    await assert.rejects(client.runExecution(body, fast), error => mode === 'authorization' ? error instanceof ErrUnauthorized : /invalid JSON/.test(String(error)));
    assert.equal(streams, 1);
    assert.equal(reads, 0);
  }
});

test('pre-aborted workflows make no requests and helpers remain preview-only', async () => {
  let calls = 0;
  const client = createClient('https://abort.test', { fetch: async () => { calls++; return terminal(); } });
  const reason = new Error('already aborted');
  const signal = AbortSignal.abort(reason);
  await assert.rejects(client.runExecution(body, { signal }), error => error === reason);
  await assert.rejects(collect(client.watchExecution('run', { signal })), error => error === reason);
  assert.equal(calls, 0);
  assert.equal('runExecution' in createPublicClient('https://public.test'), false);
  assert.equal('watchExecution' in createPublicClient('https://public.test'), false);
  if (false) {
    const result: Promise<ExecutionResponse> = client.runExecution(body);
    const events: AsyncGenerator<ExecutionEvent, void, void> = client.watchExecution('run');
    void result; void events;
    // @ts-expect-error execution helpers are preview-only
    createPublicClient('https://public.test').runExecution(body);
    // @ts-expect-error generated execution inputs remain checked
    client.runExecution({ runtime: 'unsupported' });
  }
});
