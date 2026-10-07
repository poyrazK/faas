import test from 'node:test';
import assert from 'node:assert/strict';
import { fork } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Pool } from 'pg';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime } from '../tasks.js';

const databaseURL = process.env.MCP_TASKS_TEST_DATABASE_URL;
const postgresOnly = { skip: databaseURL ? false : 'set MCP_TASKS_TEST_DATABASE_URL to run PostgreSQL integration tests' };

if (process.env.MCP_TASKS_REQUIRE_POSTGRES === '1' && !databaseURL) {
  throw new Error('MCP_TASKS_TEST_DATABASE_URL is required for the PostgreSQL integration job');
}

function principal(subject) {
  return {
    resource: new URL('https://mcp.example/mcp'),
    extra: { subject },
    clientId: 'integration-test-client',
  };
}

async function harness(t, { legacySchema = false } = {}) {
  const adminPool = new Pool({ connectionString: databaseURL, max: 1, connectionTimeoutMillis: 5_000, statement_timeout: 10_000 });
  const schema = `gregale_mcp_test_${randomUUID().replaceAll('-', '')}`;
  let schemaCreated = false;
  let pool;
  t.after(async () => {
    try {
      if (pool) await pool.end();
    } finally {
      try {
        if (schemaCreated) await adminPool.query(`DROP SCHEMA ${schema} CASCADE`);
      } finally {
        await adminPool.end();
      }
    }
  });

  await adminPool.query(`CREATE SCHEMA ${schema}`);
  schemaCreated = true;

  pool = new Pool({
    connectionString: databaseURL,
    max: 16,
    connectionTimeoutMillis: 5_000,
    statement_timeout: 10_000,
    options: `-c search_path=${schema}`,
  });

  if (legacySchema) {
    await pool.query(`
      CREATE TABLE gregale_mcp_tasks (
        namespace text NOT NULL,
        task_id uuid NOT NULL,
        owner_hash bytea NOT NULL,
        tool_name text NOT NULL,
        handler_version text NOT NULL,
        arguments_encrypted bytea NOT NULL,
        status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'cancelled', 'failed')),
        result_encrypted bytea,
        error_encrypted bytea,
        created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
        updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
        expires_at timestamptz NOT NULL,
        attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
        lease_token uuid,
        lease_expires_at timestamptz,
        cancel_requested_at timestamptz,
        PRIMARY KEY (namespace, task_id),
        CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
        CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
      )
    `);
  }

  const namespace = `test-${randomUUID()}`;
  const ownerKey = randomBytes(48).toString('base64url');
  const store = createPostgresMcpTaskStore({
    pool,
    namespace,
    ownerKey,
    ttlMs: 60_000,
  });
  await store.initialize();
  return { adminPool, namespace, ownerKey, pool, schema, store };
}

function create(store, subject, args = { report: 'weekly' }) {
  return store.create({
    toolName: 'build_report',
    handlerVersion: '1',
    args,
    authInfo: principal(subject),
    authMode: 'external-oauth',
  });
}

function get(store, taskID, subject) {
  return store.get({ taskID, authInfo: principal(subject), authMode: 'external-oauth' });
}

async function eventually(fn, message) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const value = await fn();
    if (value) return value;
    await delay(20);
  }
  assert.fail(message);
}

function waitForWorkerMessage(worker, expectedType, timeoutMs = 10_000) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => finish(new Error(`worker did not send ${expectedType}`)), timeoutMs);
    function cleanup() {
      clearTimeout(timeout);
      worker.off('message', onMessage);
      worker.off('error', onError);
      worker.off('exit', onExit);
    }
    function finish(error, message) {
      cleanup();
      if (error) reject(error);
      else resolve(message);
    }
    function onMessage(message) {
      if (message?.type === 'fatal') finish(new Error(message.message || 'worker failed during startup'));
      else if (message?.type === expectedType) finish(null, message);
    }
    function onError(error) { finish(error); }
    function onExit(code, signal) { finish(new Error(`worker exited before ${expectedType} (code=${code}, signal=${signal})`)); }
    worker.on('message', onMessage);
    worker.once('error', onError);
    worker.once('exit', onExit);
  });
}

async function stopChild(worker) {
  if (worker.exitCode !== null || worker.signalCode !== null) return;
  const exited = once(worker, 'exit');
  worker.kill('SIGKILL');
  await Promise.race([
    exited,
    delay(5_000).then(() => { throw new Error('worker process did not exit after SIGKILL'); }),
  ]);
}

test('PostgreSQL persists encrypted task inputs, results, and failures with caller/app isolation and expiry cleanup', postgresOnly, async t => {
  const { namespace, pool, store } = await harness(t);
  const alice = principal('alice');
  const record = await create(store, 'alice', { secret: 'private task input' });

  const raw = await pool.query(
    'SELECT owner_hash, arguments_encrypted, result_encrypted FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
    [namespace, record.task_id],
  );
  assert.equal(raw.rowCount, 1);
  assert.ok(Buffer.isBuffer(raw.rows[0].owner_hash));
  assert.ok(Buffer.isBuffer(raw.rows[0].arguments_encrypted));
  assert.ok(!raw.rows[0].arguments_encrypted.includes(Buffer.from('private task input')));
  assert.equal(raw.rows[0].result_encrypted, null);

  assert.equal(await get(store, record.task_id, 'bob'), null, 'another caller cannot read the task');
  const otherAppStore = createPostgresMcpTaskStore({
    pool,
    namespace: `${namespace}-other-app`,
    ownerKey: 'x'.repeat(48),
    ttlMs: 60_000,
  });
  assert.equal(await otherAppStore.get({ taskID: record.task_id, authInfo: alice, authMode: 'external-oauth' }), null);
  assert.equal(await otherAppStore.claim(3, 30_000), null, 'another app namespace cannot claim the task');

  const claim = await store.claim(3, 30_000);
  assert.equal(claim.task_id, record.task_id);
  assert.deepEqual(claim.arguments, { secret: 'private task input' });
  const result = { content: [{ type: 'text', text: 'private task output' }] };
  assert.equal(await store.complete(record.task_id, claim.lease_token, result), 'completed');
  const completedRaw = await pool.query(
    'SELECT result_encrypted FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
    [namespace, record.task_id],
  );
  assert.ok(Buffer.isBuffer(completedRaw.rows[0].result_encrypted));
  assert.ok(!completedRaw.rows[0].result_encrypted.includes(Buffer.from('private task output')));
  assert.deepEqual((await get(store, record.task_id, 'alice')).result, result);

  const failedRecord = await create(store, 'alice', { report: 'failure path' });
  const failedLease = await store.claim(3, 30_000);
  assert.equal(failedLease.task_id, failedRecord.task_id);
  const privateError = { code: -32603, message: 'private upstream credential' };
  assert.equal(await store.fail(failedRecord.task_id, failedLease.lease_token, privateError), 'failed');
  const failedRaw = await pool.query(
    'SELECT error_encrypted FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
    [namespace, failedRecord.task_id],
  );
  assert.ok(Buffer.isBuffer(failedRaw.rows[0].error_encrypted));
  assert.ok(!failedRaw.rows[0].error_encrypted.includes(Buffer.from(privateError.message)));
  assert.deepEqual((await get(store, failedRecord.task_id, 'alice')).error, privateError);

  await pool.query(
    "UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1",
    [namespace],
  );
  assert.equal(await get(store, record.task_id, 'alice'), null, 'expired task handles are hidden');
  assert.equal(await get(store, failedRecord.task_id, 'alice'), null, 'expired failure payloads are hidden');
  await store.cleanupExpired();
  const remaining = await pool.query('SELECT 1 FROM gregale_mcp_tasks WHERE namespace = $1', [namespace]);
  assert.equal(remaining.rowCount, 0, 'expired task payloads are removed');
});

test('PostgreSQL SKIP LOCKED lets concurrent workers claim each queued task once', postgresOnly, async t => {
  const { store } = await harness(t);
  const tasks = await Promise.all(Array.from({ length: 24 }, (_, index) => create(store, 'alice', { index })));
  const claimed = [];

  await Promise.all(Array.from({ length: 8 }, async () => {
    while (true) {
      const task = await store.claim(3, 30_000);
      if (!task) return;
      claimed.push(task);
    }
  }));

  assert.equal(claimed.length, tasks.length);
  assert.equal(new Set(claimed.map(task => task.task_id)).size, tasks.length, 'a task is never leased to two workers');
  assert.deepEqual(new Set(claimed.map(task => task.task_id)), new Set(tasks.map(task => task.task_id)));
  assert.ok(claimed.every(task => task.status === 'running' && task.attempt_count === 1));
});

test('PostgreSQL recovers an expired lease and rejects writes from the former worker', postgresOnly, async t => {
  const { namespace, pool, store } = await harness(t);
  const record = await create(store, 'alice');
  const firstLease = await store.claim(3, 30_000);
  assert.equal(firstLease.task_id, record.task_id);

  await pool.query(
    "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
    [namespace, record.task_id],
  );
  const recoveredLease = await store.claim(3, 30_000);
  assert.equal(recoveredLease.task_id, record.task_id);
  assert.equal(recoveredLease.attempt_count, 2);
  assert.notEqual(recoveredLease.lease_token, firstLease.lease_token);

  assert.deepEqual(await store.heartbeat(record.task_id, firstLease.lease_token, 30_000), { owned: false, cancelRequested: false });
  assert.equal(await store.complete(record.task_id, firstLease.lease_token, { content: [] }), null);
  assert.equal(await store.complete(record.task_id, recoveredLease.lease_token, { content: [{ type: 'text', text: 'recovered' }] }), 'completed');
  assert.equal((await get(store, record.task_id, 'alice')).status, 'completed');
});

test('PostgreSQL runtime recovers a task after its worker process crashes', postgresOnly, async t => {
  const { namespace, ownerKey, pool, schema, store } = await harness(t);
  const workerPath = join(dirname(fileURLToPath(import.meta.url)), 'tasks.worker-fixture.js');
  const worker = fork(workerPath, [], {
    stdio: ['ignore', 'ignore', 'inherit', 'ipc'],
    env: {
      ...process.env,
      MCP_TASKS_TEST_DATABASE_URL: databaseURL,
      MCP_TASKS_TEST_NAMESPACE: namespace,
      MCP_TASKS_TEST_OWNER_KEY: ownerKey,
      MCP_TASKS_TEST_SCHEMA: schema,
    },
  });
  let replacementRuntime;
  try {
    await waitForWorkerMessage(worker, 'ready');
    const record = await create(store, 'alice', { report: 'recovered after restart' });
    const started = await waitForWorkerMessage(worker, 'started');
    assert.equal(started.taskId, record.task_id);

    const beforeCrash = await pool.query(
      'SELECT status, attempt_count FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
      [namespace, record.task_id],
    );
    assert.deepEqual(beforeCrash.rows[0], { status: 'running', attempt_count: 1 });

    const childExit = once(worker, 'exit');
    worker.kill('SIGKILL'); // Simulate abrupt process loss while the handler is still running.
    const [, signal] = await Promise.race([
      childExit,
      delay(5_000).then(() => { throw new Error('crashed worker process did not exit'); }),
    ]);
    assert.equal(signal, 'SIGKILL');

    // Move the dead worker's lease past its deadline instead of sleeping for the
    // production 30-second lease duration. The store-level case tests the same SQL.
    await pool.query(
      "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
      [namespace, record.task_id],
    );

    let executions = 0;
    replacementRuntime = createMcpTaskRuntime({
      store,
      pollIntervalMs: 500,
      handlers: {
        build_report: {
          version: '1',
          async execute(args) {
            executions++;
            return { content: [{ type: 'text', text: `Recovered ${args.report}.` }] };
          },
        },
      },
    });
    await replacementRuntime.start();
    const completed = await eventually(
      () => replacementRuntime.get(record.task_id, principal('alice'), 'external-oauth')
        .then(task => task.status === 'completed' ? task : null),
      'replacement runtime did not complete the recovered task',
    );
    assert.equal(completed.result.content[0].text, 'Recovered recovered after restart.');
    assert.equal(executions, 1, 'the replacement process runs the recovered handler once');

    const persisted = await get(store, record.task_id, 'alice');
    assert.equal(persisted.status, 'completed');
    assert.equal(persisted.attempt_count, 2);
  } finally {
    if (replacementRuntime) await replacementRuntime.stop();
    await stopChild(worker);
  }
});

test('PostgreSQL persists task input and resumes the same handler after a worker restart', postgresOnly, async t => {
  const { namespace, pool, store } = await harness(t);
  const alice = principal('alice');
  let executions = 0;
  const handlers = {
    approval: {
      version: '1',
      async execute(_args, { requestInput }) {
        executions++;
        const response = await requestInput('approval', {
          method: 'elicitation/create',
          params: { mode: 'form', message: 'Approve the operation?' },
        });
        return { content: [{ type: 'text', text: `Approval: ${response.action}` }] };
      },
    },
  };
  const firstRuntime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  await firstRuntime.start();
  const handle = await firstRuntime.create('approval', {}, alice, 'external-oauth', { elicitation: { form: {} } });
  const waiting = await eventually(
    () => firstRuntime.get(handle.taskId, alice, 'external-oauth').then(task => task.status === 'input_required' ? task : null),
    'task did not persist its input request',
  );
  assert.deepEqual(waiting.inputRequests.approval, {
    method: 'elicitation/create',
    params: { mode: 'form', message: 'Approve the operation?' },
  });
  const raw = await pool.query(
    'SELECT status, attempt_count, input_state_encrypted FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
    [namespace, handle.taskId],
  );
  assert.equal(raw.rows[0].status, 'input_required');
  assert.equal(raw.rows[0].attempt_count, 1);
  assert.ok(Buffer.isBuffer(raw.rows[0].input_state_encrypted));
  assert.ok(!raw.rows[0].input_state_encrypted.includes(Buffer.from('Approve the operation?')));
  await firstRuntime.stop();

  assert.equal(await store.updateInputs({
    taskID: handle.taskId,
    authInfo: alice,
    authMode: 'external-oauth',
    inputResponses: { approval: { action: 'accept' }, unknown: { action: 'accept' } },
  }), true);
  const resumedRaw = await pool.query(
    'SELECT status, resume_pending, attempt_count FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
    [namespace, handle.taskId],
  );
  assert.deepEqual(resumedRaw.rows[0], { status: 'queued', resume_pending: true, attempt_count: 1 });

  const replacementRuntime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  await replacementRuntime.start();
  try {
    const completed = await eventually(
      () => replacementRuntime.get(handle.taskId, alice, 'external-oauth').then(task => task.status === 'completed' ? task : null),
      'replacement worker did not resume the task after input arrived',
    );
    assert.equal(completed.result.content[0].text, 'Approval: accept');
    assert.equal(executions, 2, 'the persistent handler replays once after the client response');
    assert.equal((await get(store, handle.taskId, 'alice')).attempt_count, 1, 'a normal input pause does not consume retry budget');
  } finally {
    await replacementRuntime.stop();
  }
});

test('PostgreSQL task notifications reach web subscriptions from a separate worker runtime', postgresOnly, async t => {
  const { namespace, ownerKey, pool, store } = await harness(t);
  const workerStore = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
  let releaseWorker;
  const workGate = new Promise(resolve => { releaseWorker = resolve; });
  const handlers = {
    build_report: {
      version: '1',
      async execute({ report }) {
        await workGate;
        return { content: [{ type: 'text', text: `Finished ${report}.` }] };
      },
    },
  };
  const webRuntime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 30_000, workerEnabled: false });
  const workerRuntime = createMcpTaskRuntime({ store: workerStore, handlers, pollIntervalMs: 500, keepAlive: true });
  await webRuntime.start();
  await workerRuntime.start();
  t.after(async () => {
    await workerRuntime.stop();
    await webRuntime.stop();
  });

  const alice = principal('alice');
  const handle = await webRuntime.create('build_report', { report: 'notifications' }, alice, 'external-oauth');
  let resolveCompleted;
  let rejectCompleted;
  const completion = new Promise((resolve, reject) => {
    resolveCompleted = resolve;
    rejectCompleted = reject;
  });
  const updates = [];
  const subscription = await webRuntime.subscribe(
    [handle.taskId], alice, 'external-oauth', () => true,
    task => {
      updates.push(task);
      if (task.status === 'completed') resolveCompleted(task);
    },
  );
  const timeout = setTimeout(() => rejectCompleted(new Error('web runtime did not receive the worker task notification')), 5_000);
  try {
    assert.equal(subscription.taskIds[0], handle.taskId);
    assert.equal(subscription.tasks[0].status, 'working');
    releaseWorker();
    const completed = await completion;
    assert.equal(completed.result.content[0].text, 'Finished notifications.');
    assert.ok(updates.some(task => task.status === 'completed'));
  } finally {
    releaseWorker();
    clearTimeout(timeout);
    subscription.close();
  }
});

test('PostgreSQL task notifications are isolated by app namespace', postgresOnly, async t => {
  const { namespace, ownerKey, pool, store } = await harness(t);
  const otherNamespace = `${namespace}-other-app`;
  const otherStore = createPostgresMcpTaskStore({ pool, namespace: otherNamespace, ownerKey, ttlMs: 60_000 });
  await otherStore.initialize();

  let ownNotifications = 0;
  let otherNotifications = 0;
  const unsubscribeOwn = await store.subscribe(() => { ownNotifications++; });
  const unsubscribeOther = await otherStore.subscribe(() => { otherNotifications++; });
  try {
    const alice = principal('alice');
    const ownTask = await create(store, 'alice', { report: 'private app status' });
    await eventually(() => ownNotifications > 0, 'own app did not receive its task notification');
    await delay(100);
    assert.equal(otherNotifications, 0, 'another app namespace cannot observe this task notification');

    await otherStore.create({
      toolName: 'build_report',
      handlerVersion: '1',
      args: { report: 'other app status' },
      authInfo: alice,
      authMode: 'external-oauth',
    });
    await eventually(() => otherNotifications > 0, 'other app did not receive its own task notification');
    assert.ok(ownTask.task_id);
  } finally {
    await unsubscribeOwn();
    await unsubscribeOther();
  }
});

test('PostgreSQL migrates an existing task table to support input-required state', postgresOnly, async t => {
  const { store } = await harness(t, { legacySchema: true });
  const authInfo = principal('alice');
  const record = await store.create({
    toolName: 'build_report',
    handlerVersion: '1',
    args: { report: 'upgrade' },
    authInfo,
    authMode: 'external-oauth',
    inputMethods: ['elicitation/create:form'],
  });
  const lease = await store.claim(3, 30_000);
  assert.equal(lease.task_id, record.task_id);
  assert.deepEqual(lease.input_methods, ['elicitation/create:form']);
  assert.deepEqual(await store.requestInputs({
    taskID: record.task_id,
    leaseToken: lease.lease_token,
    requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } },
  }), { ready: false, responses: {} });
  const waiting = await get(store, record.task_id, 'alice');
  assert.equal(waiting.status, 'input_required');
  assert.equal(waiting.input_requests.approval.method, 'elicitation/create');
  assert.equal(await store.updateInputs({
    taskID: record.task_id,
    authInfo,
    authMode: 'external-oauth',
    inputResponses: { approval: { action: 'accept' } },
  }), true);
  const resumedLease = await store.claim(3, 30_000);
  assert.equal(resumedLease.task_id, record.task_id);
  assert.equal(resumedLease.attempt_count, 1);
});

test('PostgreSQL cancellation prevents queued execution and wins against a running completion', postgresOnly, async t => {
  const { store } = await harness(t);
  const queued = await create(store, 'alice');
  assert.equal(await store.requestCancel({ taskID: queued.task_id, authInfo: principal('alice'), authMode: 'external-oauth' }), true);
  assert.equal((await get(store, queued.task_id, 'alice')).status, 'cancelled');
  assert.equal(await store.claim(3, 30_000), null, 'cancelled queued work is not claimable');

  const running = await create(store, 'alice', { report: 'in progress' });
  const lease = await store.claim(3, 30_000);
  assert.equal(lease.task_id, running.task_id);
  assert.equal(await store.requestCancel({ taskID: running.task_id, authInfo: principal('alice'), authMode: 'external-oauth' }), true);
  assert.deepEqual(await store.heartbeat(running.task_id, lease.lease_token, 30_000), { owned: true, cancelRequested: true });
  assert.equal(await store.complete(running.task_id, lease.lease_token, { content: [{ type: 'text', text: 'must not persist' }] }), 'cancelled');
  const final = await get(store, running.task_id, 'alice');
  assert.equal(final.status, 'cancelled');
  assert.equal(final.result, undefined);
});

test('PostgreSQL fails a task after its retry limit is exhausted', postgresOnly, async t => {
  const { namespace, pool, store } = await harness(t);
  const record = await create(store, 'alice');
  const first = await store.claim(2, 30_000);
  assert.equal(first.attempt_count, 1);

  await pool.query(
    "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
    [namespace, record.task_id],
  );
  const second = await store.claim(2, 30_000);
  assert.equal(second.attempt_count, 2);

  await pool.query(
    "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
    [namespace, record.task_id],
  );
  assert.equal(await store.claim(2, 30_000), null);
  const failed = await get(store, record.task_id, 'alice');
  assert.equal(failed.status, 'failed');
  assert.equal(failed.lease_token, null);
  assert.equal(failed.attempt_count, 2);
});
