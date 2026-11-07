import assert from 'node:assert/strict';
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import { setImmediate as tick } from 'node:timers/promises';
import test from 'node:test';
import { createApp } from '../app.js';

const protocol = '2026-07-28';
const taskID = '11111111-1111-4111-8111-111111111111';
const taskURI = `task://tasks/${taskID}`;

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

async function firstMessage(response) {
  const reader = response.body.getReader();
  let text = '';
  const decoder = new TextDecoder();
  while (true) {
    const { value, done } = await reader.read();
    if (done) return text ? JSON.parse(text) : undefined;
    text += decoder.decode(value, { stream: true });
    if (response.headers.get('content-type')?.includes('application/json')) continue;
    const frame = text.split('\n').find(line => line.startsWith('data: '));
    if (frame && text.includes('\n\n')) return JSON.parse(frame.slice(6));
  }
}

async function startServer(t, { taskRuntime, resourceUpdateAdapter } = {}) {
  const api = createApp({
    version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: false,
    allowed_origins: [], tasks: { enabled: !!taskRuntime },
    auth: { mode: 'open', tool_scopes: { greet: [], build_report: [] },
      resource_scopes: { 'task://tasks/{taskId}': [], 'greeting://welcome': [], 'second://item': [] }, prompt_scopes: {} },
  }, { taskRuntime, resourceUpdateAdapter, log: () => {} });
  const server = api.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const controllers = [];
  t.after(async () => {
    for (const abort of controllers) abort.abort();
    api.closeTaskSubscriptions();
    await api.handler.close();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  });
  let requestID = 0;
  return {
    api,
    server,
    nextResponseClose() {
      const closed = deferred();
      server.once('request', (_req, res) => res.once('close', closed.resolve));
      return closed.promise;
    },
    send(notifications = {}) {
      const abort = new AbortController();
      controllers.push(abort);
      const promise = fetch(`http://127.0.0.1:${server.address().port}/mcp`, {
        method: 'POST', signal: abort.signal,
        headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
          'MCP-Protocol-Version': protocol, 'Mcp-Method': 'subscriptions/listen' },
        body: JSON.stringify({ jsonrpc: '2.0', id: ++requestID, method: 'subscriptions/listen', params: {
          notifications: { toolsListChanged: true, resourceSubscriptions: [], ...notifications },
          _meta: { 'io.modelcontextprotocol/protocolVersion': protocol,
            'io.modelcontextprotocol/clientInfo': { name: 'setup-test', version: '1' },
            'io.modelcontextprotocol/clientCapabilities': { extensions: { 'io.modelcontextprotocol/tasks': {} } } },
        } }),
      });
      return { abort, promise };
    },
  };
}

async function assertFullCapacity(app) {
  const messages = await Promise.all(Array.from({ length: 128 }, async () => firstMessage(await app.send().promise)));
  assert.equal(messages.filter(message => message?.method === 'notifications/subscriptions/acknowledged').length, 128);
  const excess = await firstMessage(await app.send().promise);
  assert.equal(excess.error?.message, 'Subscription limit reached');
}

test('a reader that stops consuming is disconnected and releases stream capacity', { timeout: 15000 }, async t => {
  const app = await startServer(t);
  const closed = app.nextResponseClose();
  let response;
  app.server.once('request', (_req, res) => { response = res; });
  const { promise } = app.send({ resourceSubscriptions: ['greeting://welcome'] });
  assert.equal((await firstMessage(await promise)).method, 'notifications/subscriptions/acknowledged');
  for (let i = 0; i < 10000; i++) app.api.notifyResourceUpdated('greeting://welcome');
  await closed;
  assert.equal(response.destroyed, true);
  assert.ok(response.writableLength <= 1 << 20);
  await assertFullCapacity(app);
});

test('event bursts during asynchronous setup are bounded and late adapters are disposed', { timeout: 15000 }, async t => {
  let disposed = 0;
  const app = await startServer(t, { resourceUpdateAdapter: {
    publish() {},
    async subscribe(uri, listener) {
      for (let i = 0; i < 10000; i++) listener(uri);
      return () => { disposed++; };
    },
  } });
  const closed = app.nextResponseClose();
  await assert.rejects(app.send({ resourceSubscriptions: ['greeting://welcome'] }).promise);
  await closed;
  assert.equal(disposed, 1);
  await assertFullCapacity(app);
});

test('pending task lookups reserve subscription capacity atomically', { timeout: 15000 }, async t => {
  const release = deferred();
  t.after(release.resolve);
  const staged = deferred();
  let lookups = 0, rejected = 0;
  const stage = () => { if (lookups + rejected === 160) staged.resolve(); };
  const app = await startServer(t, { taskRuntime: {
    async get() { lookups++; stage(); await release.promise; throw new Error('Task not found'); },
    async subscribe() { assert.fail('an inaccessible task must not be subscribed'); },
  } });
  const pending = Array.from({ length: 160 }, async () => {
    const message = await firstMessage(await app.send({ resourceSubscriptions: [taskURI] }).promise);
    if (message?.error) { rejected++; stage(); }
    return message;
  });
  await staged.promise;
  release.resolve();
  const messages = await Promise.all(pending);
  assert.equal(lookups, 128);
  assert.equal(rejected, 32);
  assert.equal(messages.filter(message => message?.method === 'notifications/subscriptions/acknowledged').length, 128);
});

for (const found of [false, true]) {
  test(`disconnect during task lookup releases capacity (task ${found ? 'found' : 'absent'})`, { timeout: 15000 }, async t => {
    const entered = deferred(), release = deferred();
    t.after(release.resolve);
    let subscriptions = 0;
    const app = await startServer(t, { taskRuntime: {
      async get() { entered.resolve(); await release.promise; if (!found) throw new Error('Task not found'); return {}; },
      async subscribe() { subscriptions++; return { taskIds: [taskID], tasks: [], close() {} }; },
    } });
    const closed = app.nextResponseClose();
    const request = app.send({ resourceSubscriptions: [taskURI] });
    await entered.promise;
    request.abort.abort();
    await assert.rejects(request.promise, { name: 'AbortError' });
    await closed;
    await assertFullCapacity(app);
    app.api.closeTaskSubscriptions();
    release.resolve();
    await tick();
    assert.equal(subscriptions, 0, 'cancelled setup must not start a task subscription');
    await assertFullCapacity(app);
  });
}

for (const drain of [false, true]) {
  test(`late async resource registration is disposed after ${drain ? 'drain' : 'disconnect'}`, { timeout: 15000 }, async t => {
    const entered = deferred(), release = deferred();
    t.after(release.resolve);
    let active = 0, disposed = 0, calls = 0;
    const app = await startServer(t, { resourceUpdateAdapter: {
      publish() {},
      async subscribe() {
        calls++; active++; entered.resolve(); await release.promise;
        return () => { disposed++; active--; };
      },
    } });
    const closed = app.nextResponseClose();
    const request = app.send({ resourceSubscriptions: ['greeting://welcome'] });
    await entered.promise;
    if (drain) {
      app.api.closeTaskSubscriptions();
      await request.promise;
    } else {
      request.abort.abort();
      await assert.rejects(request.promise, { name: 'AbortError' });
    }
    await closed;
    release.resolve();
    await tick();
    assert.equal(active, 0);
    assert.equal(disposed, 1);
    assert.equal(calls, 1);
    app.api.closeTaskSubscriptions();
    assert.equal(disposed, 1, 'cleanup must be idempotent');
    await assertFullCapacity(app);
  });
}

test('late async task subscription is closed exactly once after disconnect', { timeout: 15000 }, async t => {
  const entered = deferred(), release = deferred();
  t.after(release.resolve);
  let disposed = 0;
  const app = await startServer(t, { taskRuntime: {
    async get() { return {}; },
    async subscribe() { entered.resolve(); await release.promise; return { taskIds: [taskID], tasks: [], close() { disposed++; } }; },
  } });
  const closed = app.nextResponseClose();
  const request = app.send({ taskIds: [taskID] });
  await entered.promise;
  request.abort.abort();
  await assert.rejects(request.promise, { name: 'AbortError' });
  await closed;
  release.resolve();
  await tick();
  assert.equal(disposed, 1);
  app.api.closeTaskSubscriptions();
  assert.equal(disposed, 1);
  await assertFullCapacity(app);
});

test('post-lookup filter validation failure releases the reserved slot', { timeout: 15000 }, async t => {
  const app = await startServer(t, { taskRuntime: {
    async get() { return {}; }, async subscribe() { assert.fail('oversized union must be rejected before subscribing'); },
  } });
  const response = await app.send({ taskIds: Array.from({ length: 64 }, () => randomUUID()), resourceSubscriptions: [taskURI] }).promise;
  assert.equal(response.status, 400);
  assert.equal((await response.json()).error.code, -32602);
  await assertFullCapacity(app);
});

for (const failure of ['setup', 'disposal', 'async disposal']) {
  test(`${failure} failure still releases subscription capacity`, { timeout: 15000 }, async t => {
    let disposed = 0;
    const app = await startServer(t, { resourceUpdateAdapter: {
      publish() {},
      subscribe(uri) {
        if (uri === 'second://item') throw new Error('adapter unavailable');
        return () => {
          disposed++;
          if (failure === 'async disposal') return Promise.reject(new Error('unsubscribe unavailable'));
          throw new Error('unsubscribe unavailable');
        };
      },
    } });
    app.api.registerResource('second', 'second://item', {}, async () => ({ contents: [] }));
    const response = await app.send({ resourceSubscriptions: failure === 'setup'
      ? ['greeting://welcome', 'second://item'] : ['greeting://welcome'] }).promise;
    if (failure === 'setup') assert.equal((await response.json()).error.code, -32603);
    else {
      await firstMessage(response);
      app.api.closeTaskSubscriptions();
    }
    await tick();
    assert.equal(disposed, 1);
    await assertFullCapacity(app);
  });
}
