import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import * as z from 'zod/v4';
import { createApp } from '../app.js';
import { createMcpTaskRuntime } from '../tasks.js';
import { snapshotTaskOutputSchema, validateTaskOutput } from '../task-output.js';

const outputSchema = { type: 'object', required: ['amount'], additionalProperties: false,
  $defs: { amount: { type: 'integer', minimum: 1 } }, properties: { amount: { $ref: '#/$defs/amount' } } };

test('task output snapshots support local references and never mutate results', () => {
  const original = structuredClone(outputSchema);
  const schema = snapshotTaskOutputSchema(original);
  original.required.length = 0;
  assert.throws(() => validateTaskOutput({ content: [], structuredContent: {} }, schema));
  const value = { content: [], structuredContent: { amount: 2 } };
  const before = structuredClone(value);
  validateTaskOutput(value, schema);
  assert.deepEqual(value, before);
  for (const amount of ['secret-output', -1, true]) {
    assert.throws(() => validateTaskOutput({ content: [], structuredContent: { amount } }, schema), /does not match outputSchema/);
  }
  assert.throws(() => validateTaskOutput({ content: [] }, schema));
  validateTaskOutput({ content: [], isError: true }, schema);
  validateTaskOutput({ content: [] }, null);
  validateTaskOutput({ legacy: true }, null);
  for (const result of [{}, { content: null }, { content: [], resultType: 'task' }, { content: [], resultType: 'input_required' }]) {
    assert.throws(() => validateTaskOutput(result, schema), /Invalid MCP Task tool result/);
  }
});

test('unresolved output schemas fail closed and reused schema IDs stay isolated', () => {
  for (const schema of [null, [], { type: 42 }, { $ref: 'https://provider.example/schema' }, { $schema: 'https://unsupported.example/schema' }]) {
    assert.throws(() => snapshotTaskOutputSchema(schema), /Invalid MCP Task output schema/);
  }
  const first = snapshotTaskOutputSchema({ ...outputSchema, $id: 'https://contract.example/output' });
  const next = snapshotTaskOutputSchema({ $id: first.$id, type: 'object', required: ['label'], properties: { label: { type: 'string' } } });
  validateTaskOutput({ content: [], structuredContent: { amount: 2 } }, first);
  assert.throws(() => validateTaskOutput({ content: [], structuredContent: { amount: 2 } }, next));
});

function storeFixture() {
  let persisted = 0;
  return { get persisted() { return persisted; }, async initialize() {}, async claim() { return null; },
    async create() { persisted++; const now = new Date(); return { task_id: randomUUID(), created_at: now, updated_at: now, expires_at: new Date(+now + 60_000) }; } };
}

test('async task validation settles before persistence and rejects without an unhandled promise', async () => {
  const store = storeFixture();
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const runtime = createMcpTaskRuntime({ store, workerEnabled: false, handlers: {
    lookup: { version: '1', async validate() { await gate; throw new Error('invalid arguments'); }, async execute() { return { content: [] }; } },
  } });
  await runtime.start();
  const pending = runtime.create('lookup', {}, undefined, 'open');
  const rejected = assert.rejects(pending, /invalid arguments/);
  await Promise.resolve();
  assert.equal(store.persisted, 0);
  release();
  await rejected;
  assert.equal(store.persisted, 0);
  await runtime.stop();
});

test('async validation cannot admit new work after drain begins', async () => {
  const store = storeFixture();
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const runtime = createMcpTaskRuntime({ store, workerEnabled: false, handlers: {
    lookup: { version: '1', async validate() { await gate; }, async execute() { return { content: [] }; } },
  } });
  await runtime.start();
  const pending = runtime.create('lookup', {}, undefined, 'open');
  const rejected = assert.rejects(pending, /draining/);
  await runtime.stop();
  release();
  await rejected;
  assert.equal(store.persisted, 0);
});

test('output-schema tools return task handles while discovery and synchronous validation remain intact', async t => {
  let creates = 0;
  let saved;
  const runtime = { canCreate: name => name === 'lookup', async create(_name, _args, _caller, _mode, _capabilities, schema) {
    creates++;
    saved = snapshotTaskOutputSchema(schema);
    return { resultType: 'task', taskId: randomUUID(), status: 'working', createdAt: new Date().toISOString(), lastUpdatedAt: new Date().toISOString(), ttlMs: 60_000, pollIntervalMs: 1000 };
  } };
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true,
    allowed_origins: [], tasks: { enabled: true }, auth: { mode: 'open' } }, { taskRuntime: runtime, log() {} });
  api.registerTool('lookup', { inputSchema: z.object({}), outputSchema: z.object({ amount: z.number().int() }) },
    async () => ({ content: [], structuredContent: { amount: 'invalid' } }));
  const server = api.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => { api.closeTaskSubscriptions(); await api.handler.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  async function rpc(method, version, tasks = false) {
    const response = await fetch(`http://127.0.0.1:${server.address().port}/mcp`, {
      method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
        'MCP-Protocol-Version': version, 'Mcp-Method': method, ...(method === 'tools/call' ? { 'Mcp-Name': 'lookup' } : {}) },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params: { name: 'lookup', arguments: {}, _meta: version === '2026-07-28' ? {
        'io.modelcontextprotocol/protocolVersion': version,
        'io.modelcontextprotocol/clientInfo': { name: 'output-test', version: '1' },
        'io.modelcontextprotocol/clientCapabilities': tasks ? { extensions: { 'io.modelcontextprotocol/tasks': {} } } : {},
      } : {} } }),
    });
    const text = await response.text();
    assert.equal(response.status, 200, text);
    return JSON.parse(text.startsWith('{') ? text : text.split('\n').find(line => line.startsWith('data: ')).slice(6)).result;
  }
  for (const version of ['2026-07-28', '2025-11-25']) {
    const inventory = await rpc('tools/list', version, true);
    assert.equal(inventory.tools.find(tool => tool.name === 'lookup').outputSchema.properties.amount.type, 'integer');
    assert.equal((await rpc('tools/call', version)).isError, true);
  }
  const handle = await rpc('tools/call', '2026-07-28', true);
  assert.equal(handle.resultType, 'task');
  assert.ok(handle.taskId);
  assert.equal(handle.isError, undefined);
  assert.equal(creates, 1);
  assert.equal(saved.properties.amount.type, 'integer');
});
