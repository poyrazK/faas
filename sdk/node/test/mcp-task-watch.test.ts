import assert from 'node:assert/strict';
import test from 'node:test';
import { createMcpTaskTransport, watchMcpTask, watchMcpTasks, type McpTaskWatchConnection } from '../src/preview.js';

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

function polling(read: (id: string) => Promise<Record<string, unknown>>): McpTaskWatchConnection {
  return {
    getNegotiatedProtocolVersion: () => '2026-07-28',
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {} } }),
    async request(request, _, { signal }) {
      assert.equal(request.method, 'tasks/get');
      signal.throwIfAborted();
      return read(request.params.taskId);
    },
  };
}

type FixtureStream = {
  send(task: Record<string, unknown>, id?: string): void;
  drop(): void;
};

function streaming(
  read: (id: string) => Promise<Record<string, unknown>>,
  onStart: (stream: FixtureStream, ids: string[]) => void,
  honored: boolean | string[] = true,
): McpTaskWatchConnection {
  const transport = createMcpTaskTransport(FixtureTransport, endpoint, {
    fetch: async (_, init) => {
      const request = JSON.parse(String(init?.body));
      const ids: string[] = request.params.notifications.taskIds;
      assert.ok(Array.isArray(ids) && ids.length > 0 && ids.length <= 64);
      assert.deepEqual(request.params.notifications, { taskIds: ids });
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
            _meta: metadata, notifications: honored ? { taskIds: honored === true ? ids : honored } : {},
          });
          onStart({
            send(task, id = request.id) { frame('notifications/tasks', { ...task, _meta: { [subscriptionId]: id } }); },
            drop() { controller.close(); },
          }, ids);
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

test('a dropped shared stream reconciles all Tasks and reconnects only the remaining Task', async () => {
  const paused = 'paused task';
  const remaining = 'remaining task';
  const ids = [taskId, paused, remaining];
  const states = new Map(ids.map(id => [id, { ...initial, taskId: id }]));
  const firstStream = deferred<FixtureStream>();
  const secondStream = deferred<FixtureStream>();
  const initialReconciliation = deferred<void>();
  const trace: string[] = [];
  let subscriptions = 0;
  const client = streaming(async id => {
    trace.push(id);
    if (trace.length === 7) initialReconciliation.resolve();
    return states.get(id)!;
  }, (stream, requested) => {
    trace.push('subscribe');
    if (++subscriptions === 1) {
      assert.deepEqual(requested, ids);
      firstStream.resolve(stream);
    } else {
      assert.deepEqual(requested, [remaining]);
      secondStream.resolve(stream);
    }
  });
  const watch = watchMcpTasks(client, [...ids, taskId], { timeoutMs: 3_000 });
  async function status() {
    const { value } = await watch.next();
    assert.equal(value?.outcome, 'available');
    return [value.taskId, value.task.status];
  }

  try {
    for (const id of ids) assert.deepEqual(await status(), [id, 'working']);
    const completed = status();
    const live = await Promise.race([
      firstStream.promise, completed.then(() => assert.fail('Task changed before opening the shared stream')),
    ]);
    await initialReconciliation.promise;
    live.drop();
    states.set(taskId, { ...initial, status: 'completed' });
    states.set(paused, { ...initial, taskId: paused, status: 'input_required' });
    assert.deepEqual(await completed, [taskId, 'completed']);
    assert.deepEqual(await status(), [paused, 'input_required']);

    const finished = status();
    const reconnected = await Promise.race([
      secondStream.promise, finished.then(() => assert.fail('Task finished before reconnecting')),
    ]);
    const beforeReconnect = [...trace];
    states.set(remaining, { ...initial, taskId: remaining, status: 'cancelled' });
    reconnected.send({ taskId: remaining });
    assert.deepEqual(await finished, [remaining, 'cancelled']);
    assert.deepEqual(beforeReconnect, [...ids, 'subscribe', ...ids, ...ids, 'subscribe']);
    assert.deepEqual(await watch.next(), { done: true, value: undefined });
  } finally {
    await watch.return();
  }
});

test('partially honored filters use polling for the remaining IDs and isolate inaccessible Tasks', async () => {
  const inaccessible = 'inaccessible task';
  const polled = 'polled task';
  const ids = [taskId, inaccessible, polled];
  const reads = new Map<string, number>();
  let subscriptions = 0;
  const client = streaming(async id => {
    const count = (reads.get(id) ?? 0) + 1;
    reads.set(id, count);
    if (id === inaccessible && count === 2) throw { code: -32002, message: 'private server detail' };
    return { ...initial, taskId: id, status: count === 1 ? 'working' : 'completed' };
  }, (_, requested) => {
    assert.deepEqual(requested, ids);
    subscriptions++;
  }, [taskId, inaccessible, 'not requested']);
  const events = [];
  for await (const event of watchMcpTasks(client, ids, { timeoutMs: 3_000 })) events.push(event);
  assert.deepEqual(events.map(event => [event.taskId, event.outcome === 'available' ? event.task.status : event.error.code]), [
    [taskId, 'working'], [inaccessible, 'working'], [polled, 'working'],
    [taskId, 'completed'], [inaccessible, 'not_found'], [polled, 'completed'],
  ]);
  const denied = events[4]!;
  assert.equal(denied.outcome, 'unavailable');
  assert.equal(denied.error.message, 'MCP Task is unavailable to this caller');
  assert.equal(subscriptions, 1);
  assert.deepEqual([...reads.values()], [2, 2, 2]);
});

test('batches above the subscription limit poll with bounded concurrency before subscribing', async () => {
  const ids = Array.from({ length: 65 }, (_, i) => `task ${i}`);
  const reads = new Map<string, number>();
  let concurrent = 0;
  let maximum = 0;
  const filters: string[][] = [];
  const client = streaming(async id => {
    const count = (reads.get(id) ?? 0) + 1;
    reads.set(id, count);
    maximum = Math.max(maximum, ++concurrent);
    await new Promise<void>(resolve => setImmediate(resolve));
    concurrent--;
    const finished = count > 1 && (id === ids[0] || filters.length > 0);
    return { ...initial, taskId: id, status: finished ? 'completed' : 'working' };
  }, (_, requested) => { filters.push(requested); });
  const states = new Map<string, string[]>();
  for await (const event of watchMcpTasks(client, ids, { timeoutMs: 3_000 })) {
    assert.equal(event.outcome, 'available');
    states.set(event.taskId, [...(states.get(event.taskId) ?? []), event.task.status]);
  }
  assert.equal(maximum, 4);
  assert.deepEqual(filters, [ids.slice(1)]);
  assert.deepEqual([...states.keys()], ids);
  for (const id of ids) assert.deepEqual(states.get(id), ['working', 'completed']);
});

test('an empty batch makes no requests and invalid IDs fail before the batch starts', async () => {
  const client = polling(async () => assert.fail('unexpected Task read'));
  assert.deepEqual(await watchMcpTasks(client, []).next(), { done: true, value: undefined });
  assert.throws(() => watchMcpTasks(client, [taskId, '']), { name: 'TypeError' });
});
