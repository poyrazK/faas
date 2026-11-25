import assert from 'node:assert/strict';
import test from 'node:test';
import { createMcpTaskTransport, watchMcpTask, type McpTaskWatchConnection } from '../src/preview.js';

const taskId = 'known task';
const endpoint = 'https://mcp.example/mcp';
const subscriptionId = 'io.modelcontextprotocol/subscriptionId';
const initial = { taskId, status: 'working', createdAt: '2026-11-24T00:00:00Z',
  lastUpdatedAt: '2026-11-24T00:00:00Z', ttlMs: 120_000, pollIntervalMs: 0 };
const encoder = new TextEncoder();

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

class FixtureTransport {
  constructor(readonly url: URL, readonly options?: { fetch?: typeof fetch }) {}
}

function polling(read: () => Promise<Record<string, unknown>>): McpTaskWatchConnection {
  return {
    getNegotiatedProtocolVersion: () => '2026-07-28',
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {} } }),
    async request(request, _, { signal }) {
      assert.deepEqual(request, { method: 'tasks/get', params: { taskId } });
      signal.throwIfAborted();
      return read();
    },
  };
}

type FixtureStream = {
  send(task: Record<string, unknown>, id?: string): void;
  drop(): void;
};

function streaming(
  read: () => Promise<Record<string, unknown>>,
  onStart: (stream: FixtureStream) => void,
  honored = true,
): McpTaskWatchConnection {
  const transport = createMcpTaskTransport(FixtureTransport, endpoint, {
    fetch: async (_, init) => {
      const request = JSON.parse(String(init?.body));
      assert.deepEqual(request.params.notifications, { taskIds: [taskId] });
      assert.deepEqual(request.params._meta['io.modelcontextprotocol/clientCapabilities'].extensions, {
        'io.modelcontextprotocol/tasks': {},
      });
      assert.equal(init?.redirect, 'manual');
      const metadata = { [subscriptionId]: request.id };
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          const frame = (method: string, params: object) => {
            controller.enqueue(encoder.encode(`event: message\r\ndata: ${JSON.stringify({ jsonrpc: '2.0', method, params })}\r\n\r\n`));
          };
          frame('notifications/subscriptions/acknowledged', {
            _meta: metadata, notifications: honored ? { taskIds: [taskId] } : {},
          });
          onStart({
            send(task, id = request.id) { frame('notifications/tasks', { ...task, _meta: { [subscriptionId]: id } }); },
            drop() { controller.close(); },
          });
        },
      });
      return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } });
    },
  });
  let next = 0;
  return {
    ...polling(read), transport,
    async listen(filter, { signal }) {
      const response = await transport.options!.fetch!(transport.url, {
        method: 'POST', signal, headers: { 'Mcp-Method': 'subscriptions/listen' },
        body: JSON.stringify({ jsonrpc: '2.0', id: `listen:${++next}`, method: 'subscriptions/listen',
          params: { notifications: filter, _meta: { 'io.modelcontextprotocol/clientCapabilities': {} } } }),
      });
      const reader = response.body!.getReader();
      await reader.read(); // The official client opens the handle after the first acknowledgement.
      const close = async () => { await reader.cancel(); };
      signal.addEventListener('abort', () => { void close(); }, { once: true });
      const closed = (async () => {
        while (!(await reader.read()).done) { /* Drain the stream as the official client does. */ }
        return signal.aborted ? 'local' as const : 'remote' as const;
      })();
      return { close, closed };
    },
  };
}

test('Task watch falls back to polling and emits each authorized snapshot once', async () => {
  let reads = 0;
  const client = polling(async () => {
    reads++;
    return reads < 3 ? initial : { ...initial, status: 'completed', result: { private: true } };
  });
  const watch = watchMcpTask(client, taskId, { timeoutMs: 3_000 });
  const events = [];
  for await (const event of watch) events.push(event);
  assert.deepEqual(events.map(event => event.status), ['working', 'completed']);
  assert.equal(Object.hasOwn(events[1]!, 'result'), false);
  assert.equal(reads, 3);
});

test('a live notification wakes the watch and the authorized read determines the state', async () => {
  let current: Record<string, unknown> = initial;
  let reads = 0;
  const stream = deferred<FixtureStream>();
  const acknowledged = deferred<void>();
  const client = streaming(async () => {
    if (++reads === 2) acknowledged.resolve();
    return current;
  }, stream.resolve);
  const watch = watchMcpTask(client, taskId, { timeoutMs: 3_000 });
  try {
    assert.equal((await watch.next()).value?.status, 'working');
    const next = watch.next();
    const live = await stream.promise;
    await acknowledged.promise;
    live.send({ ...initial, status: 'completed' }, 'other subscription');
    live.send({ ...initial, taskId: 'other task', status: 'completed' });
    await new Promise<void>(resolve => setImmediate(resolve));
    assert.equal(reads, 2);

    // The confirmed read supplies the state even if the signal carries a different state.
    current = { ...initial, status: 'input_required', inputRequests: { private: true } };
    live.send({ ...initial, status: 'completed', result: { private: true } });
    const event = (await next).value!;
    assert.equal(event.status, 'input_required');
    assert.equal(event.nextAction, 'provide_input');
    assert.equal(Object.hasOwn(event, 'inputRequests'), false);
    assert.deepEqual(await watch.next(), { value: undefined, done: true });
    assert.equal(reads, 3);
  } finally {
    await watch.return();
  }
});

test('a dropped stream is reconciled before reconnecting', async () => {
  const trace: string[] = [];
  const stream = deferred<FixtureStream>();
  const opened = deferred<void>();
  let reads = 0;
  let subscriptions = 0;
  const client = streaming(async () => {
    trace.push('read');
    if (++reads === 2) opened.resolve();
    return subscriptions < 2 ? initial : { ...initial, status: 'completed' };
  }, value => {
    trace.push('subscribe');
    if (++subscriptions === 1) stream.resolve(value);
  });
  const watch = watchMcpTask(client, taskId, { timeoutMs: 3_000 });
  try {
    assert.equal((await watch.next()).value?.status, 'working');
    const finished = watch.next();
    const live = await stream.promise;
    await opened.promise;
    live.drop();
    assert.equal((await finished).value?.status, 'completed');
    assert.deepEqual(await watch.next(), { value: undefined, done: true });
    assert.deepEqual(trace, ['read', 'subscribe', 'read', 'read', 'subscribe', 'read']);
  } finally {
    await watch.return();
  }
});

test('unhonored Task filters fall back to polling', async () => {
  let reads = 0;
  let subscriptions = 0;
  const client = streaming(async () => ++reads === 1 ? initial : { ...initial, status: 'completed' },
    () => { subscriptions++; }, false);
  const events = [];
  for await (const event of watchMcpTask(client, taskId, { timeoutMs: 3_000 })) events.push(event.status);
  assert.deepEqual(events, ['working', 'completed']);
  assert.equal(subscriptions, 1);
});

test('return cancels an outstanding Task status read', async () => {
  const opened = deferred<void>();
  const client = {
    ...polling(async () => initial),
    request: async (...[, , options]: Parameters<McpTaskWatchConnection['request']>) => new Promise<never>((_, reject) => {
      options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true });
      opened.resolve();
    }),
  };
  const watch = watchMcpTask(client, taskId, { timeoutMs: 3_000 });
  const pending = assert.rejects(watch.next(), { name: 'AbortError' });
  await opened.promise;
  assert.deepEqual(await watch.return(), { value: undefined, done: true });
  await pending;
});

test('a quiet subscription still reconciles the Task', async () => {
  let reads = 0;
  let subscriptions = 0;
  const client = streaming(async () => ++reads < 3 ? initial : { ...initial, status: 'completed' },
    () => { subscriptions++; });
  const states = [];
  for await (const task of watchMcpTask(client, taskId, { timeoutMs: 3_000, reconcileIntervalMs: 250 })) {
    states.push(task.status);
  }
  assert.deepEqual(states, ['working', 'completed']);
  assert.equal(reads, 3);
  assert.equal(subscriptions, 1);
});
