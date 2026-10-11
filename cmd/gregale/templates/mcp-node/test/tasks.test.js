import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createApp } from '../app.js';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime, MCP_TASKS_EXTENSION_ID, mcpTaskHandlers } from '../tasks.js';

function owner(authInfo, authMode) {
  if (authMode === 'open') return 'public';
  return `${authInfo?.resource?.href || ''}:${authInfo?.extra?.subject || ''}:${authInfo?.clientId || ''}`;
}

function copy(task) {
  return task && {
    ...task,
    arguments: task.arguments && structuredClone(task.arguments),
    result: task.result && structuredClone(task.result),
    error: task.error && structuredClone(task.error),
    input_methods: [...(task.input_methods || [])],
    input_requests: task.input_state && Object.fromEntries(Object.entries(task.input_state.requests).filter(([key]) => !Object.hasOwn(task.input_state.responses, key))),
  };
}

class MemoryTaskStore {
  rows = new Map();
  initialized = false;
  async initialize() { this.initialized = true; }
  async create({ toolName, handlerVersion, args, authInfo, authMode, inputMethods = [] }) {
    const now = new Date();
    const row = {
      task_id: randomUUID(), owner: owner(authInfo, authMode), tool_name: toolName,
      handler_version: handlerVersion, arguments: structuredClone(args), status: 'queued',
      result: null, error: null, created_at: now, updated_at: now,
      expires_at: new Date(now.getTime() + 60_000), attempt_count: 0,
      lease_token: null, lease_expires_at: null, cancel_requested_at: null,
      input_methods: [...inputMethods], input_state: null, resume_pending: false,
    };
    this.rows.set(row.task_id, row);
    return copy(row);
  }
  async get({ taskID, authInfo, authMode }) {
    const row = this.rows.get(taskID);
    if (!row || row.owner !== owner(authInfo, authMode) || row.expires_at <= new Date()) return null;
    return copy(row);
  }
  async getMany({ taskIDs, authInfo, authMode }) {
    const tasks = [];
    for (const taskID of taskIDs) {
      const task = await this.get({ taskID, authInfo, authMode });
      if (task) tasks.push(task);
    }
    return tasks;
  }
  async requestCancel({ taskID, authInfo, authMode }) {
    const row = await this.get({ taskID, authInfo, authMode });
    if (!row) return false;
    const stored = this.rows.get(taskID);
    if (stored.status === 'queued' || stored.status === 'input_required') stored.status = 'cancelled';
    else if (stored.status === 'running') stored.cancel_requested_at = new Date();
    stored.updated_at = new Date();
    return true;
  }
  async claim(maxAttempts, leaseMs) {
    const now = new Date();
    for (const row of this.rows.values()) {
      if (!['queued', 'running'].includes(row.status) || row.expires_at <= now) continue;
      if (row.status === 'running' && row.lease_expires_at > now) continue;
      if (row.attempt_count >= maxAttempts) {
        row.status = row.cancel_requested_at ? 'cancelled' : 'failed';
        row.error = row.cancel_requested_at ? null : { code: -32603, message: 'Task could not be resumed' };
        row.lease_token = null;
        row.lease_expires_at = null;
        continue;
      }
      row.status = 'running';
      if (!row.resume_pending) row.attempt_count++;
      row.resume_pending = false;
      row.lease_token = randomUUID();
      row.lease_expires_at = new Date(now.getTime() + leaseMs);
      row.updated_at = now;
      return copy(row);
    }
    return null;
  }
  async heartbeat(taskID, leaseToken, leaseMs) {
    const row = this.rows.get(taskID);
    if (!row || row.lease_token !== leaseToken || row.status !== 'running') return { owned: false, cancelRequested: false };
    row.lease_expires_at = new Date(Date.now() + leaseMs);
    return { owned: true, cancelRequested: row.cancel_requested_at !== null };
  }
  async complete(taskID, leaseToken, result) {
    const row = this.rows.get(taskID);
    if (!row || row.lease_token !== leaseToken || row.status !== 'running') return null;
    row.status = row.cancel_requested_at ? 'cancelled' : 'completed';
    row.result = row.cancel_requested_at ? null : structuredClone(result);
    row.lease_token = null;
    row.lease_expires_at = null;
    row.updated_at = new Date();
    return row.status;
  }
  async fail(taskID, leaseToken, error) {
    const row = this.rows.get(taskID);
    if (!row || row.lease_token !== leaseToken || row.status !== 'running') return null;
    row.status = row.cancel_requested_at ? 'cancelled' : 'failed';
    row.error = row.cancel_requested_at ? null : structuredClone(error);
    row.lease_token = null;
    row.lease_expires_at = null;
    row.updated_at = new Date();
    return row.status;
  }
  async finishCancelled(taskID, leaseToken) {
    const row = this.rows.get(taskID);
    if (!row || row.lease_token !== leaseToken || row.status !== 'running') return null;
    row.status = 'cancelled';
    row.lease_token = null;
    row.lease_expires_at = null;
    row.updated_at = new Date();
    return row.status;
  }
  async requestInputs({ taskID, leaseToken, requests }) {
    const row = this.rows.get(taskID);
    if (!row || row.lease_token !== leaseToken || row.status !== 'running') return { ready: false, responses: {} };
    if (row.cancel_requested_at) return { ready: false, cancelled: true, responses: {} };
    row.input_state ??= { requests: {}, responses: {} };
    for (const [key, request] of Object.entries(requests)) {
      if (Object.hasOwn(row.input_state.requests, key)) assert.deepEqual(row.input_state.requests[key], request);
      else row.input_state.requests[key] = structuredClone(request);
    }
    const responses = Object.fromEntries(Object.entries(requests).filter(([key]) => Object.hasOwn(row.input_state.responses, key)).map(([key]) => [key, structuredClone(row.input_state.responses[key])]));
    if (Object.keys(responses).length !== Object.keys(requests).length) {
      row.status = 'input_required';
      row.lease_token = null;
      row.lease_expires_at = null;
      row.updated_at = new Date();
      return { ready: false, responses: {} };
    }
    return { ready: true, responses };
  }
  async updateInputs({ taskID, authInfo, authMode, inputResponses }) {
    const row = this.rows.get(taskID);
    if (!row || row.owner !== owner(authInfo, authMode) || row.expires_at <= new Date()) return false;
    if (row.status !== 'input_required' || !row.input_state) return true;
    for (const [key, response] of Object.entries(inputResponses)) {
      if (Object.hasOwn(row.input_state.requests, key) && !Object.hasOwn(row.input_state.responses, key)) row.input_state.responses[key] = structuredClone(response);
    }
    const pending = Object.keys(row.input_state.requests).some(key => !Object.hasOwn(row.input_state.responses, key));
    if (!pending) {
      row.status = 'queued';
      row.resume_pending = true;
    }
    row.updated_at = new Date();
    return true;
  }
  async cleanupExpired() {}
}

async function eventually(fn, message) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const value = await fn();
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  assert.fail(message);
}

test('durable task handles are stored before return and resolve to tool results', async () => {
  const store = new MemoryTaskStore();
  const runtime = createMcpTaskRuntime({ store, handlers: mcpTaskHandlers, pollIntervalMs: 500 });
  await runtime.start();
  try {
    const handle = await runtime.create('build_report', { report: 'weekly', steps: 1 }, {}, 'open');
    assert.equal(handle.resultType, 'task');
    assert.equal(handle.status, 'working');
    assert.ok(store.rows.has(handle.taskId), 'task record must exist before its handle is returned');
    const task = await eventually(() => runtime.get(handle.taskId, {}, 'open').then(value => value.status === 'completed' ? value : null), 'task did not complete');
    assert.equal(task.result.content[0].text, 'Report weekly is ready after 1 steps.');
    assert.equal(task.resultType, 'complete');
  } finally {
    await runtime.stop();
  }
});

test('a web-only process can enqueue work for a separate task worker', async () => {
  const store = new MemoryTaskStore();
  let executions = 0;
  const handlers = { work: { version: '1', async execute() { executions++; return { content: [] }; } } };
  const webRuntime = createMcpTaskRuntime({ store, handlers, workerEnabled: false });
  const workerRuntime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500, keepAlive: true });
  await webRuntime.start();
  try {
    const handle = await webRuntime.create('work', {}, {}, 'open');
    assert.equal((await webRuntime.get(handle.taskId, {}, 'open')).status, 'working');
    assert.equal(executions, 0, 'the HTTP process must leave execution to the worker role');

    await workerRuntime.start();
    const task = await eventually(() => webRuntime.get(handle.taskId, {}, 'open').then(value => value.status === 'completed' ? value : null), 'separate worker did not complete the task');
    assert.equal(task.resultType, 'complete');
    assert.equal(executions, 1);
  } finally {
    await workerRuntime.stop();
    await webRuntime.stop();
  }
});

test('task cancellation is cooperative and task IDs are scoped to the authenticated caller', async () => {
  let executions = 0;
  const handlers = { work: { version: '1', execute: async () => { executions++; return { content: [] }; } } };
  const store = new MemoryTaskStore();
  const runtime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  const alice = { resource: new URL('https://mcp.example/mcp'), extra: { subject: 'alice' }, clientId: 'client-a' };
  const bob = { ...alice, extra: { subject: 'bob' } };
  const handle = await runtime.create('work', {}, alice, 'external-oauth');
  await assert.rejects(runtime.get(handle.taskId, bob, 'external-oauth'), /Task not found/);
  await assert.rejects(runtime.cancel(handle.taskId, bob, 'external-oauth'), /Task not found/);
  const denied = () => false;
  const allowed = toolName => toolName === 'work';
  await assert.rejects(runtime.get(handle.taskId, alice, 'external-oauth', denied), /Task not found/);
  await assert.rejects(runtime.cancel(handle.taskId, alice, 'external-oauth', denied), /Task not found/);
  assert.deepEqual(await runtime.cancel(handle.taskId, alice, 'external-oauth', allowed), { resultType: 'complete' });
  await runtime.start();
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(executions, 0, 'a task cancelled while queued must never start');
  assert.equal((await runtime.get(handle.taskId, alice, 'external-oauth')).status, 'cancelled');
  await runtime.stop();
});

test('task status subscriptions only acknowledge tasks visible to the caller and tool policy', async () => {
  const handlers = { work: { version: '1', execute: async () => ({ content: [] }) } };
  const store = new MemoryTaskStore();
  const runtime = createMcpTaskRuntime({ store, handlers, workerEnabled: false });
  const alice = { resource: new URL('https://mcp.example/mcp'), extra: { subject: 'alice' }, clientId: 'client-a' };
  const bob = { ...alice, extra: { subject: 'bob' } };
  await runtime.start();
  try {
    const aliceTask = await runtime.create('work', {}, alice, 'external-oauth');
    const bobTask = await runtime.create('work', {}, bob, 'external-oauth');
    const subscription = await runtime.subscribe(
      [aliceTask.taskId, bobTask.taskId], alice, 'external-oauth', name => name === 'work', () => {},
    );
    try {
      assert.deepEqual(subscription.taskIds, [aliceTask.taskId]);
      assert.deepEqual(subscription.tasks.map(task => task.taskId), [aliceTask.taskId]);
      assert.ok(!JSON.stringify(subscription.tasks).includes(bobTask.taskId));
    } finally {
      subscription.close();
    }
  } finally {
    await runtime.stop();
  }
});

test('input-required tasks can be cancelled and task updates keep caller isolation', async () => {
  let executions = 0;
  const handlers = {
    work: {
      version: '1',
      async execute(_args, { requestInput }) {
        executions++;
        await requestInput('approval', { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } });
        return { content: [] };
      },
    },
  };
  const store = new MemoryTaskStore();
  const runtime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  const alice = { resource: new URL('https://mcp.example/mcp'), extra: { subject: 'alice' }, clientId: 'client-a' };
  const bob = { ...alice, extra: { subject: 'bob' } };
  await runtime.start();
  try {
    const handle = await runtime.create('work', {}, alice, 'external-oauth', { elicitation: { form: {} } });
    const waiting = await eventually(() => runtime.get(handle.taskId, alice, 'external-oauth').then(task => task.status === 'input_required' ? task : null), 'task did not request input');
    assert.deepEqual(waiting.inputRequests, { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } });
    await assert.rejects(runtime.update(handle.taskId, { approval: { action: 'accept' } }, bob, 'external-oauth'), /Task not found/);
    assert.deepEqual(await runtime.cancel(handle.taskId, alice, 'external-oauth'), { resultType: 'complete' });
    assert.equal((await runtime.get(handle.taskId, alice, 'external-oauth')).status, 'cancelled');
    assert.equal(executions, 1, 'a cancelled input wait is not re-enqueued');
  } finally {
    await runtime.stop();
  }
});

test('task failures do not expose exception text in persisted results', async () => {
  const store = new MemoryTaskStore();
  const handlers = { work: { version: '1', execute: async () => { throw new Error('private database password'); } } };
  const runtime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  await runtime.start();
  try {
    const handle = await runtime.create('work', {}, {}, 'open');
    const task = await eventually(() => runtime.get(handle.taskId, {}, 'open').then(value => value.status === 'failed' ? value : null), 'task did not fail');
    assert.deepEqual(task.error, { code: -32603, message: 'Task execution failed' });
    assert.ok(!JSON.stringify(task).includes('private database password'));
  } finally {
    await runtime.stop();
  }
});

test('PostgreSQL store encrypts task arguments and scopes records to the app and caller', async () => {
  const calls = [];
  const rows = new Map();
  const pool = { async query(sql, params) {
    calls.push({ sql, params });
    if (sql.includes('COUNT(*)::int AS total')) return { rows: [{ total: rows.size, owned: [...rows.values()].filter(row => row.owner_hash.equals(params[1])).length }] };
    if (sql.includes('INSERT INTO gregale_mcp_tasks')) {
      const row = {
        task_id: params[1], tool_name: params[3], handler_version: params[4], status: 'queued',
        owner_hash: Buffer.from(params[2]), arguments_encrypted: Buffer.from(params[5]),
        input_methods: params[6], input_state_encrypted: null,
        created_at: new Date('2026-10-06T10:00:00Z'), updated_at: new Date('2026-10-06T10:00:00Z'),
        expires_at: new Date('2026-10-06T11:00:00Z'), attempt_count: 0,
        lease_token: null, lease_expires_at: null, cancel_requested_at: null,
      };
      rows.set(row.task_id, row);
      return { rows: [row] };
    }
    if (sql.includes('WITH candidate AS')) {
      const row = [...rows.values()].find(task => task.status === 'queued');
      if (!row) return { rows: [] };
      row.status = 'running';
      row.attempt_count++;
      row.lease_token = params[2];
      row.lease_expires_at = new Date('2026-10-06T10:01:00Z');
      return { rows: [row] };
    }
    if (sql.includes("SET status = CASE WHEN cancel_requested_at IS NULL THEN 'completed'")) {
      const row = rows.get(params[1]);
      row.status = 'completed';
      row.result_encrypted = Buffer.from(params[3]);
      return { rows: [{ status: 'completed' }] };
    }
    if (sql.includes('SELECT task_id::text AS task_id')) {
      const row = rows.get(params[1]);
      return { rows: row && row.owner_hash.equals(params[2]) ? [row] : [] };
    }
    return { rows: [] };
  } };
  const store = createPostgresMcpTaskStore({ pool, namespace: 'app-123', ownerKey: 'x'.repeat(48), ttlMs: 3_600_000 });
  await store.initialize();
  const authInfo = { resource: new URL('https://mcp.example/mcp'), extra: { subject: 'alice' }, clientId: 'client-a' };
  const record = await store.create({ toolName: 'build_report', handlerVersion: '1', args: { secret: 'private report input' }, authInfo, authMode: 'external-oauth' });
  const insert = calls.find(call => call.sql.includes('INSERT INTO gregale_mcp_tasks'));
  assert.ok(insert);
  assert.equal(insert.params[0], 'app-123');
  assert.ok(Buffer.isBuffer(insert.params[2]), 'caller identity is stored as a keyed hash');
  assert.ok(Buffer.isBuffer(insert.params[5]), 'task arguments are encrypted before storage');
  assert.ok(!insert.params[5].includes(Buffer.from('private report input')));
  assert.equal(record.task_id, insert.params[1]);
  const aliceOwnerHash = Buffer.from(insert.params[2]);
  await store.create({ toolName: 'build_report', handlerVersion: '1', args: { secret: 'private report input' }, authInfo: { ...authInfo, extra: { subject: 'bob' } }, authMode: 'external-oauth' });
  const bobInsert = calls.filter(call => call.sql.includes('INSERT INTO gregale_mcp_tasks'))[1];
  assert.ok(!aliceOwnerHash.equals(bobInsert.params[2]), 'different authenticated principals have different owner hashes');

  const claim = await store.claim(3, 30_000);
  assert.equal(claim.task_id, record.task_id);
  assert.deepEqual(claim.arguments, { secret: 'private report input' }, 'task arguments decrypt only after a worker claims the row');
  const result = { content: [{ type: 'text', text: 'private report output' }] };
  await store.complete(record.task_id, claim.lease_token, result);
  const completion = calls.find(call => call.sql.includes("SET status = CASE WHEN cancel_requested_at IS NULL THEN 'completed'"));
  assert.ok(Buffer.isBuffer(completion.params[3]), 'task results are encrypted before storage');
  assert.ok(!completion.params[3].includes(Buffer.from('private report output')));
  const recovered = await store.get({ taskID: record.task_id, authInfo, authMode: 'external-oauth' });
  assert.deepEqual(recovered.result, result, 'task results decrypt for the same authenticated owner');
  assert.equal(await store.get({ taskID: record.task_id, authInfo: { ...authInfo, extra: { subject: 'bob' } }, authMode: 'external-oauth' }), null);
  assert.throws(() => createPostgresMcpTaskStore({ pool, namespace: 'app-123', ownerKey: 'short', ttlMs: 60_000 }), /32 bytes/);
});

test('queue metrics count only unexpired queued and running rows in the task namespace', async () => {
  let captured;
  const pool = { async query(sql, params) {
    captured = { sql, params };
    return { rows: [{ outstanding_tasks: '4', oldest_age_seconds: '18.25' }] };
  } };
  const store = createPostgresMcpTaskStore({ pool, namespace: 'mcp-worker-prod', ownerKey: 'x'.repeat(48), ttlMs: 60_000 });
  assert.deepEqual(await store.queueMetrics(), { outstandingTasks: 4, oldestAgeSeconds: 18.25 });
  assert.deepEqual(captured.params, ['mcp-worker-prod']);
  assert.match(captured.sql, /status IN \('queued', 'running'\)/);
  assert.match(captured.sql, /expires_at > clock_timestamp\(\)/);
  assert.match(captured.sql, /MIN\(created_at\)/);
});

test('MCP Tasks are advertised and the modern wire methods round-trip through the starter SDK', { timeout: 10_000 }, async t => {
  const root = dirname(dirname(fileURLToPath(import.meta.url)));
  const config = JSON.parse(await readFile(join(root, 'gregale-mcp.json'), 'utf8'));
  config.tasks.enabled = true;
  const store = new MemoryTaskStore();
  const taskHandlers = {
    build_report: {
      version: 'input-test-v1',
      async execute({ report, steps }, { signal, requestInputs }) {
        let argsContext = '';
        if (report === 'approval') {
          const responses = await requestInputs({
            approval: {
              method: 'elicitation/create',
              params: { mode: 'form', message: 'Approve this report?', requestedSchema: { type: 'object', properties: { approved: { type: 'boolean' } }, required: ['approved'] } },
            },
            context: {
              method: 'elicitation/create',
              params: { mode: 'form', message: 'Add a report note?', requestedSchema: { type: 'object', properties: { note: { type: 'string' } } } },
            },
          });
          const { approval, context } = responses;
          if (approval.action !== 'accept' || approval.content?.approved !== true) return { content: [{ type: 'text', text: 'Report was not approved.' }] };
          argsContext = context.content?.note || '';
        }
        const result = await mcpTaskHandlers.build_report.execute({ report, steps }, { signal });
        if (report === 'approval') result.content[0].text += ` Note: ${argsContext}`;
        return result;
      },
    },
  };
  const taskRuntime = createMcpTaskRuntime({ store, handlers: taskHandlers, pollIntervalMs: 500, workerEnabled: false });
  const taskWorkerRuntime = createMcpTaskRuntime({ store, handlers: taskHandlers, pollIntervalMs: 500, keepAlive: true });
  await taskRuntime.start();
  await taskWorkerRuntime.start();
  const { app, handler, closeTaskSubscriptions } = createApp(config, { taskRuntime, log: () => {} });
  const listener = app.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  t.after(async () => {
    closeTaskSubscriptions();
    await taskRuntime.stop();
    await taskWorkerRuntime.stop();
    await handler.close();
    await new Promise(resolve => listener.close(resolve));
  });
  const endpoint = `http://127.0.0.1:${listener.address().port}/mcp`;
  const capabilities = { extensions: { [MCP_TASKS_EXTENSION_ID]: {} } };
  const elicitationCapabilities = { ...capabilities, elicitation: { form: {} } };
  const meta = {
    'io.modelcontextprotocol/protocolVersion': '2026-07-28',
    'io.modelcontextprotocol/clientInfo': { name: 'gregale-task-test', version: '1' },
    'io.modelcontextprotocol/clientCapabilities': capabilities,
  };
  async function request(method, params, name, clientCapabilities = capabilities) {
    const response = await fetch(endpoint, { method: 'POST', headers: {
      'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
      'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': method,
      ...(name ? { 'Mcp-Name': name } : {}),
    }, body: JSON.stringify({ jsonrpc: '2.0', id: randomUUID(), method, params: { ...params, _meta: { ...meta, 'io.modelcontextprotocol/clientCapabilities': clientCapabilities } } }) });
    const text = await response.text();
    const data = text.split('\n').find(line => line.startsWith('data: '));
    assert.ok(data, text);
    return { response, result: JSON.parse(data.slice(6)).result, text };
  }
  async function listen(taskIDs, signal, clientCapabilities = capabilities, resourceSubscriptions = []) {
    return fetch(endpoint, { method: 'POST', signal, headers: {
      'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
      'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'subscriptions/listen',
    }, body: JSON.stringify({ jsonrpc: '2.0', id: `listen-${randomUUID()}`, method: 'subscriptions/listen', params: {
      notifications: { toolsListChanged: true, taskIds: taskIDs, ...(resourceSubscriptions.length ? { resourceSubscriptions } : {}) },
      _meta: { ...meta, 'io.modelcontextprotocol/clientCapabilities': clientCapabilities },
    } }) });
  }
  const discovery = await request('server/discover', {});
  assert.deepEqual(discovery.result.capabilities.extensions[MCP_TASKS_EXTENSION_ID], {});
  assert.equal(discovery.result.capabilities.resources.subscribe, true);
  const taskResourceTemplate = await request('resources/templates/list', {});
  assert.ok(taskResourceTemplate.result.resourceTemplates.some(resource => resource.uriTemplate === 'task://tasks/{taskId}'));
  const call = await request('tools/call', { name: 'build_report', arguments: { report: 'daily', steps: 3 } }, 'build_report');
  assert.equal(call.response.status, 200, call.text);
  assert.equal(call.result.resultType, 'task');
  assert.equal(call.result.status, 'working');
  const taskID = call.result.taskId;
  const first = await request('tasks/get', { taskId: taskID }, taskID);
  assert.equal(first.response.status, 200, first.text);
  assert.equal(first.result.resultType, 'complete');
  const complete = await eventually(async () => {
    const current = await request('tasks/get', { taskId: taskID }, taskID);
    return current.result.status === 'completed' ? current.result : null;
  }, 'MCP task did not complete');
  assert.equal(complete.result.content[0].text, 'Report daily is ready after 3 steps.');
  const cancel = await request('tasks/cancel', { taskId: taskID }, taskID);
  assert.equal(cancel.response.status, 200, cancel.text);
  assert.equal(cancel.result.resultType, 'complete');

  const approval = await request('tools/call', { name: 'build_report', arguments: { report: 'approval', steps: 1 } }, 'build_report', elicitationCapabilities);
  assert.equal(approval.result.resultType, 'task');
  const approvalTaskID = approval.result.taskId;
  const inputTask = await eventually(async () => {
    const current = await request('tasks/get', { taskId: approvalTaskID }, approvalTaskID);
    return current.result.status === 'input_required' ? current.result : null;
  }, 'MCP task did not pause for client input');
  assert.equal(inputTask.inputRequests.approval.method, 'elicitation/create');
  assert.equal(inputTask.inputRequests.context.method, 'elicitation/create');

  const missingTaskCapability = await listen([approvalTaskID], undefined, {});
  assert.equal(missingTaskCapability.status, 400);
  const missingCapabilityError = await missingTaskCapability.json();
  assert.equal(missingCapabilityError.error.code, -32021);
  assert.deepEqual(missingCapabilityError.error.data.requiredCapabilities.extensions[MCP_TASKS_EXTENSION_ID], {});

  const taskResourceURI = `task://tasks/${approvalTaskID}`;
  const listenController = new AbortController();
  const listenResponse = await listen([approvalTaskID], listenController.signal, capabilities, [taskResourceURI]);
  assert.equal(listenResponse.status, 200);
  assert.match(listenResponse.headers.get('content-type'), /text\/event-stream/);
  const reader = listenResponse.body.getReader();
  const decoder = new TextDecoder();
  let eventBuffer = '';
  async function nextSseMessage() {
    while (true) {
      const boundary = eventBuffer.indexOf('\n\n');
      if (boundary >= 0) {
        const frame = eventBuffer.slice(0, boundary);
        eventBuffer = eventBuffer.slice(boundary + 2);
        const data = frame.split('\n').find(line => line.startsWith('data: '));
        if (data) return JSON.parse(data.slice(6));
        continue;
      }
      const chunk = await reader.read();
      assert.equal(chunk.done, false, 'task subscription remains open until the client closes it');
      eventBuffer += decoder.decode(chunk.value, { stream: true });
    }
  }
  const acknowledged = await nextSseMessage();
  assert.equal(acknowledged.method, 'notifications/subscriptions/acknowledged');
  assert.deepEqual(acknowledged.params.notifications.taskIds, [approvalTaskID]);
  assert.deepEqual(acknowledged.params.notifications.resourceSubscriptions, [taskResourceURI]);
  assert.equal(acknowledged.params.notifications.toolsListChanged, true);
  const initialStatus = await nextSseMessage();
  assert.equal(initialStatus.method, 'notifications/tasks');
  assert.equal(initialStatus.params.status, 'input_required');
  assert.deepEqual(Object.keys(initialStatus.params.inputRequests), ['approval', 'context']);
  assert.equal(initialStatus.params._meta['io.modelcontextprotocol/subscriptionId'], acknowledged.params._meta['io.modelcontextprotocol/subscriptionId']);
  handler.notify.toolsChanged();
  const toolsChanged = await nextSseMessage();
  assert.equal(toolsChanged.method, 'notifications/tools/list_changed');
  assert.equal(toolsChanged.params._meta['io.modelcontextprotocol/subscriptionId'], acknowledged.params._meta['io.modelcontextprotocol/subscriptionId']);

  const missingResponses = await request('tasks/update', { taskId: approvalTaskID }, approvalTaskID);
  assert.ok(missingResponses.text.includes('Task input responses must be an object'), missingResponses.text);
  const ignored = await request('tasks/update', { taskId: approvalTaskID, inputResponses: { unknown: { action: 'accept' } } }, approvalTaskID);
  assert.equal(ignored.response.status, 200, ignored.text);
  assert.ok(ignored.result, ignored.text);
  assert.equal(ignored.result.resultType, 'complete');
  const stillWaiting = await request('tasks/get', { taskId: approvalTaskID }, approvalTaskID);
  assert.equal(stillWaiting.result.status, 'input_required', 'unknown response keys are ignored');
  const partial = await request('tasks/update', { taskId: approvalTaskID, inputResponses: { approval: { action: 'accept', content: { approved: true } } } }, approvalTaskID);
  assert.equal(partial.result.resultType, 'complete');
  const partialTask = await request('tasks/get', { taskId: approvalTaskID }, approvalTaskID);
  assert.equal(partialTask.result.status, 'input_required', 'partial responses leave unanswered requests outstanding');
  assert.deepEqual(Object.keys(partialTask.result.inputRequests), ['context']);
  const resume = await request('tasks/update', { taskId: approvalTaskID, inputResponses: {
    context: { action: 'accept', content: { note: 'Reviewed' } },
    approval: { action: 'decline' },
  } }, approvalTaskID);
  assert.equal(resume.result.resultType, 'complete');
  const resumed = await eventually(async () => {
    const current = await request('tasks/get', { taskId: approvalTaskID }, approvalTaskID);
    return current.result.status === 'completed' ? current.result : null;
  }, 'MCP task did not resume after input was supplied');
  assert.equal(resumed.result.content[0].text, 'Report approval is ready after 1 steps. Note: Reviewed');
  let completionNotification;
  let resourceUpdateNotification;
  for (let attempt = 0; attempt < 20 && (!completionNotification || !resourceUpdateNotification); attempt++) {
    const message = await nextSseMessage();
    if (message.method === 'notifications/tasks' && message.params.status === 'completed') completionNotification = message;
    if (message.method === 'notifications/resources/updated' && message.params.uri === taskResourceURI) resourceUpdateNotification = message;
  }
  assert.deepEqual(completionNotification.params.result, resumed.result);
  assert.equal(resourceUpdateNotification.params.uri, taskResourceURI);
  const taskResource = await request('resources/read', { uri: taskResourceURI }, taskResourceURI);
  assert.equal(taskResource.response.status, 200, taskResource.text);
  assert.equal(JSON.parse(taskResource.result.contents[0].text).result.content[0].text, 'Report approval is ready after 1 steps. Note: Reviewed');
  listenController.abort();
  await reader.cancel().catch(() => {});

  const rowCount = store.rows.size;
  const synchronous = await request('tools/call', { name: 'build_report', arguments: { report: 'sync', steps: 1 } }, 'build_report', {});
  assert.equal(synchronous.result.resultType, 'complete', synchronous.text);
  assert.equal(synchronous.result.content[0].text, 'Report sync is ready after 1 steps.');
  assert.equal(store.rows.size, rowCount, 'clients without Tasks support do not create task records');
});
