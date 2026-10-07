import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { createApp } from '../app.js';

function taskSnapshot(taskID, status, result) {
  return {
    resultType: 'complete', taskId: taskID, status,
    createdAt: '2026-10-01T00:00:00.000Z', lastUpdatedAt: '2026-10-01T00:00:01.000Z',
    ttlMs: 60_000, pollIntervalMs: 1_000,
    ...(result === undefined ? {} : { result }),
  };
}

function createFakeTaskRuntime(taskID) {
  let current = taskSnapshot(taskID, 'working');
  const watchers = new Set();
  return {
    async get(requestedID, _authInfo, _authMode, canAccessTool) {
      if (requestedID !== taskID || !canAccessTool('build_report')) throw new Error('Task not found');
      return structuredClone(current);
    },
    async subscribe(taskIDs, _authInfo, _authMode, canAccessTool, onTask) {
      const accepted = taskIDs.filter(id => id === taskID && canAccessTool('build_report'));
      const watcher = { taskIDs: new Set(accepted), onTask };
      watchers.add(watcher);
      return {
        taskIds: accepted,
        tasks: accepted.map(() => structuredClone(current)),
        close() { watchers.delete(watcher); },
      };
    },
    update(snapshot) {
      current = structuredClone(snapshot);
      for (const watcher of watchers) {
        if (watcher.taskIDs.has(taskID)) watcher.onTask(structuredClone(current));
      }
    },
  };
}

async function startServer(t, taskRuntime) {
  const { app, closeTaskSubscriptions } = createApp({
    version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: false,
    allowed_origins: [], tasks: { enabled: true },
    auth: {
      mode: 'open',
      tool_scopes: { build_report: [] },
      resource_scopes: { 'task://tasks/{taskId}': [] },
      prompt_scopes: {},
    },
  }, { taskRuntime, log: () => {} });
  const server = app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    closeTaskSubscriptions();
    server.closeAllConnections?.();
    await new Promise(resolve => server.close(resolve));
  });
  return `http://127.0.0.1:${server.address().port}/mcp`;
}

function streamMessages(response) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  return {
    async next() {
      while (true) {
        const boundary = buffer.indexOf('\n\n');
        if (boundary >= 0) {
          const frame = buffer.slice(0, boundary);
          buffer = buffer.slice(boundary + 2);
          const data = frame.split('\n').find(line => line.startsWith('data: '));
          if (data) return JSON.parse(data.slice(6));
          continue;
        }
        const { done, value } = await reader.read();
        if (done) throw new Error('MCP SSE stream ended before the expected message');
        buffer += decoder.decode(value, { stream: true });
      }
    },
    cancel() { return reader.cancel(); },
  };
}

function protocolHeaders(method, name) {
  return {
    'Content-Type': 'application/json',
    Accept: 'application/json, text/event-stream',
    'MCP-Protocol-Version': '2026-07-28',
    'Mcp-Method': method,
    ...(name ? { 'Mcp-Name': name } : {}),
  };
}

function modernEnvelope() {
  return {
    'io.modelcontextprotocol/protocolVersion': '2026-07-28',
    'io.modelcontextprotocol/clientInfo': { name: 'resource-test', version: '1' },
    'io.modelcontextprotocol/clientCapabilities': {},
  };
}

test('task resources acknowledge authorized URIs and publish changed task content', async t => {
  const taskID = randomUUID();
  const uri = `task://tasks/${taskID}`;
  const runtime = createFakeTaskRuntime(taskID);
  const endpoint = await startServer(t, runtime);
  const abort = new AbortController();
  const response = await fetch(endpoint, {
    method: 'POST', signal: abort.signal, headers: protocolHeaders('subscriptions/listen'),
    body: JSON.stringify({
      jsonrpc: '2.0', id: 11, method: 'subscriptions/listen',
      params: { notifications: { resourceSubscriptions: [uri] }, _meta: modernEnvelope() },
    }),
  });
  assert.equal(response.status, 200);
  const messages = streamMessages(response);
  const acknowledgement = await messages.next();
  assert.equal(acknowledgement.method, 'notifications/subscriptions/acknowledged');
  assert.deepEqual(acknowledgement.params.notifications.resourceSubscriptions, [uri]);

  runtime.update(taskSnapshot(taskID, 'completed', { content: [{ type: 'text', text: 'Report ready.' }] }));
  const update = await messages.next();
  assert.equal(update.method, 'notifications/resources/updated');
  assert.equal(update.params.uri, uri);

  const read = await fetch(endpoint, {
    method: 'POST', headers: protocolHeaders('resources/read', uri),
    body: JSON.stringify({ jsonrpc: '2.0', id: 12, method: 'resources/read', params: { uri, _meta: modernEnvelope() } }),
  });
  assert.equal(read.status, 200);
  const readMessages = streamMessages(read);
  const resource = await readMessages.next();
  const task = JSON.parse(resource.result.contents[0].text);
  assert.equal(task.status, 'completed');
  assert.equal(task.result.content[0].text, 'Report ready.');
  await messages.cancel();
  abort.abort();
});

test('task resource subscriptions do not acknowledge unknown task URIs', async t => {
  const runtime = createFakeTaskRuntime(randomUUID());
  const endpoint = await startServer(t, runtime);
  const abort = new AbortController();
  const response = await fetch(endpoint, {
    method: 'POST', signal: abort.signal, headers: protocolHeaders('subscriptions/listen'),
    body: JSON.stringify({
      jsonrpc: '2.0', id: 21, method: 'subscriptions/listen',
      params: { notifications: { resourceSubscriptions: [`task://tasks/${randomUUID()}`] }, _meta: modernEnvelope() },
    }),
  });
  assert.equal(response.status, 200);
  const messages = streamMessages(response);
  const acknowledgement = await messages.next();
  assert.equal(acknowledgement.method, 'notifications/subscriptions/acknowledged');
  assert.equal(Object.hasOwn(acknowledgement.params.notifications, 'resourceSubscriptions'), false);
  const completion = await messages.next();
  assert.equal(completion.id, 21);
  assert.equal(completion.result.resultType, 'complete');
  await messages.cancel();
  abort.abort();
});
