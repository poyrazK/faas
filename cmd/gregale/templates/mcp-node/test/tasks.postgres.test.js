import test from 'node:test';
import assert from 'node:assert/strict';
import { fork } from 'node:child_process';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Pool } from 'pg';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime, RetryableMcpTaskError } from '../tasks.js';

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

async function harness(t, { legacySchema = false, maxRunning = 64, maxRunningPerOwner = 64 } = {}) {
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
    maxRunning, maxRunningPerOwner,
  });
  await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
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
  const fairness = await pool.query('SELECT 1 FROM gregale_mcp_task_fairness WHERE namespace = $1', [namespace]);
  assert.equal(fairness.rowCount, 0, 'idle owner cursors are removed after their tasks expire');
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

test('PostgreSQL rotates claims across owners and preserves FIFO within each owner', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const aliceTasks = [];
  const bobTasks = [];
  for (let index = 0; index < 6; index++) {
    aliceTasks.push(await create(store, 'alice', { owner: 'alice', index }));
    bobTasks.push(await create(store, 'bob', { owner: 'bob', index }));
  }

  const ids = new Map([
    ...aliceTasks.map(task => [task.task_id, 'alice']),
    ...bobTasks.map(task => [task.task_id, 'bob']),
  ]);
  const claimedByOwner = { alice: [], bob: [] };
  const claimOrder = [];
  for (let index = 0; index < 8; index++) {
    const task = await store.claim(3, 30_000);
    assert.ok(task, `claim ${index + 1} should find queued work`);
    const owner = ids.get(task.task_id);
    assert.ok(owner, `claim returned an unknown task ${task.task_id}`);
    claimOrder.push(owner);
    claimedByOwner[owner].push(task.task_id);
  }

  assert.ok(claimOrder.every((owner, index) => index === 0 || owner !== claimOrder[index - 1]),
    `active owners should take turns; claim order was ${claimOrder.join(', ')}`);
  assert.deepEqual(claimedByOwner.alice, aliceTasks.slice(0, 4).map(task => task.task_id));
  assert.deepEqual(claimedByOwner.bob, bobTasks.slice(0, 4).map(task => task.task_id));

  const cursors = await pool.query(
    'SELECT owner_hash FROM gregale_mcp_task_fairness WHERE namespace = $1',
    [namespace],
  );
  assert.equal(cursors.rowCount, 2, 'the database keeps one fairness cursor per active owner');
});

test('PostgreSQL concurrent claims skip a locked owner and serve another owner', postgresOnly, async t => {
  const { store } = await harness(t);
  const alice = await create(store, 'alice');
  const bob = await create(store, 'bob');
  const claimed = await Promise.all([
    store.claim(3, 30_000),
    store.claim(3, 30_000),
  ]);

  assert.ok(claimed.every(Boolean));
  assert.deepEqual(new Set(claimed.map(task => task.task_id)), new Set([alice.task_id, bob.task_id]));
});

test('PostgreSQL retries a briefly locked owner cursor instead of idling a worker', postgresOnly, async t => {
  const { pool, namespace, ownerKey, store } = await harness(t);
  const record = await create(store, 'alice');
  let signalBusy;
  const sawBusy = new Promise(resolve => { signalBusy = resolve; });
  const observedPool = {
    async query(sql, params) {
      const result = await pool.query(sql, params);
      if (String(sql).includes('WITH chosen_owner') && result.rows?.[0]?.busy) signalBusy();
      return result;
    },
    async connect(...args) {
      const client = await pool.connect(...args);
      const originalQuery = client.query.bind(client);
      return { async query(sql, params) {
        const result = await originalQuery(sql, params);
        if (String(sql).includes('WITH chosen_owner') && result.rows?.[0]?.busy) signalBusy();
        return result;
      }, release: (...args) => client.release(...args) };
    },
  };
  const retryingStore = createPostgresMcpTaskStore({ pool: observedPool, namespace, ownerKey, ttlMs: 60_000 });
  await retryingStore.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

  const lockClient = await pool.connect();
  let transactionOpen = false;
  try {
    await lockClient.query('BEGIN');
    transactionOpen = true;
    await lockClient.query(
      'SELECT owner_hash FROM gregale_mcp_task_fairness WHERE namespace = $1 FOR UPDATE',
      [namespace],
    );
    const claim = retryingStore.claim(3, 30_000);
    await Promise.race([
      sawBusy,
      delay(5_000).then(() => { throw new Error('claim did not report a temporarily locked owner cursor'); }),
    ]);
    await lockClient.query('COMMIT');
    transactionOpen = false;
    assert.equal((await claim).task_id, record.task_id);
  } finally {
    if (transactionOpen) await lockClient.query('ROLLBACK').catch(() => {});
    lockClient.release();
  }
});

test('PostgreSQL initialization backfills fairness cursors for existing queued tasks', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const record = await create(store, 'alice');
  await pool.query('DELETE FROM gregale_mcp_task_fairness WHERE namespace = $1', [namespace]);

  await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

  const cursor = await pool.query(
    'SELECT 1 FROM gregale_mcp_task_fairness WHERE namespace = $1',
    [namespace],
  );
  assert.equal(cursor.rowCount, 1, 'existing queue owners receive a fairness cursor during migration');
  assert.equal((await store.claim(3, 30_000)).task_id, record.task_id);
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
  await otherStore.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

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

test('PostgreSQL resumes input on the final attempt without granting another retry', postgresOnly, async t => {
  const { namespace, pool, store } = await harness(t);
  const record = await create(store, 'alice');
  let lease;
  for (let attempt = 1; attempt <= 3; attempt++) {
    lease = await store.claim(3, 30_000);
    assert.equal(lease.attempt_count, attempt);
    if (attempt < 3) await pool.query(
      "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
      [namespace, record.task_id],
    );
  }
  await store.requestInputs({ taskID: record.task_id, leaseToken: lease.lease_token,
    requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } },
  });
  await store.updateInputs({ taskID: record.task_id, authInfo: principal('alice'), authMode: 'external-oauth',
    inputResponses: { approval: { action: 'accept', content: {} } },
  });
  const resumed = await store.claim(3, 30_000);
  assert.equal(resumed.task_id, record.task_id);
  assert.equal(resumed.attempt_count, 3);
  await pool.query(
    "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
    [namespace, record.task_id],
  );
  assert.equal(await store.claim(3, 30_000), null);
  assert.equal((await get(store, record.task_id, 'alice')).status, 'failed');
});

test('PostgreSQL atomically budgets queue admission across replicas and owners', postgresOnly, async t => {
  const { pool, namespace, ownerKey } = await harness(t);
  const stores = Array.from({ length: 2 }, () => createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000, maxOutstanding: 3, maxOutstandingPerOwner: 2 }));
  const attempts = await Promise.allSettled(Array.from({ length: 8 }, (_, i) => create(stores[i % 2], 'alice')));
  assert.equal(attempts.filter(result => result.status === 'fulfilled').length, 2);
  assert.ok(attempts.filter(result => result.status === 'rejected').every(result => result.reason.code === 'MCP_TASK_CAPACITY'));
  const bob = await create(stores[0], 'bob');
  await assert.rejects(create(stores[1], 'carol'), { code: 'MCP_TASK_CAPACITY' });
  await stores[0].requestCancel({ taskID: bob.task_id, authInfo: principal('bob'), authMode: 'external-oauth' });
  assert.ok(await create(stores[1], 'carol'));
});

test('PostgreSQL leaves unsupported handler versions queued for compatible workers', postgresOnly, async t => {
  const { store } = await harness(t);
  const old = await create(store, 'alice');
  const current = await store.create({ toolName: 'build_report', handlerVersion: '2', args: {}, authInfo: principal('alice'), authMode: 'external-oauth' });
  const newWorker = await store.claim(3, 30_000, [{ name: 'build_report', version: '2' }]);
  assert.equal(newWorker.task_id, current.task_id);
  assert.equal((await get(store, old.task_id, 'alice')).attempt_count, 0);
  assert.equal(await store.claim(3, 30_000, [{ name: 'build_report', version: '2' }]), null);
  assert.equal((await store.claim(3, 30_000, [{ name: 'build_report', version: '1' }])).task_id, old.task_id);
});


test('running limits are atomic across replicas and recover from completion and lease expiry', postgresOnly, async t => {
  const { pool, namespace, ownerKey } = await harness(t);
  const options = { pool, namespace, ownerKey, ttlMs: 60_000, maxRunning: 3, maxRunningPerOwner: 1 };
  const replicas = Array.from({ length: 12 }, () => createPostgresMcpTaskStore(options));
  for (let owner = 0; owner < 4; owner++) {
    await create(replicas[0], `owner-${owner}`);
    await create(replicas[0], `owner-${owner}`);
  }
  const claims = (await Promise.all(replicas.map(store => store.claim(3, 60_000)))).filter(Boolean);
  assert.equal(claims.length, 3);
  assert.equal(new Set(claims.map(task => task.task_id)).size, 3);
  const owners = await pool.query("SELECT owner_hash, COUNT(*)::int AS count FROM gregale_mcp_tasks WHERE status = 'running' GROUP BY owner_hash");
  assert.ok(owners.rows.every(row => row.count === 1));
  const metrics = await replicas[0].queueMetrics();
  assert.equal(metrics.runningTasks, 3);
  assert.equal(metrics.capacityWaitingTasks, 5);
  await replicas[0].complete(claims[0].task_id, claims[0].lease_token, { ok: true });
  assert.ok(await replicas[1].claim(3, 60_000));
  await pool.query("UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE task_id = $1", [claims[1].task_id]);
  assert.deepEqual(await replicas[0].heartbeat(claims[1].task_id, claims[1].lease_token, 60_000), { owned: false, cancelRequested: false });
  assert.ok(await replicas[2].claim(3, 60_000));
  assert.equal((await replicas[0].queueMetrics()).runningTasks, 3);
});


test('owner saturation leaves capacity for other owners and cancellation releases it', postgresOnly, async t => {
  const { pool, namespace, ownerKey } = await harness(t);
  const store = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000, maxRunning: 3, maxRunningPerOwner: 1 });
  assert.deepEqual(await store.queueMetrics(), { outstandingTasks: 0, oldestAgeSeconds: 0, runningTasks: 0, capacityWaitingTasks: 0, failedTasks: 0, retryWaitingTasks: 0, activeWorkers: 0, drainingWorkers: 0, unsupportedHandlerTasks: 0 });
  await create(store, 'alice');
  await create(store, 'alice');
  const alice = await store.claim(3, 60_000);
  assert.equal(await store.claim(3, 60_000), null);
  assert.equal((await store.queueMetrics()).capacityWaitingTasks, 1);
  const bob = await create(store, 'bob');
  const bobLease = await store.claim(3, 60_000);
  assert.equal(bobLease.task_id, bob.task_id);
  await store.requestCancel({ taskID: alice.task_id, authInfo: principal('alice'), authMode: 'external-oauth' });
  assert.equal(await store.claim(3, 60_000), null, 'cooperative cancellation keeps its live slot');
  await store.finishCancelled(alice.task_id, alice.lease_token);
  assert.ok(await store.claim(3, 60_000));
  await store.requestInputs({ taskID: bob.task_id, leaseToken: bobLease.lease_token, requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } } });
  assert.equal((await store.queueMetrics()).runningTasks, 1, 'input pauses release their running slot');
});

test('heartbeat renewal waits for the capacity lock and rechecks lease expiration', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  await create(store, 'alice');
  const lease = await store.claim(3, 60_000);
  const lock = await pool.connect();
  let renewal;
  try {
    await lock.query('BEGIN');
    await lock.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`gregale_mcp_tasks:execution:${namespace}`]);
    renewal = store.heartbeat(lease.task_id, lease.lease_token, 60_000);
    await eventually(async () => {
      const result = await pool.query("SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND NOT granted AND objid = (hashtextextended($1, 0) & 4294967295)::oid AND classid = ((hashtextextended($1, 0) >> 32) & 4294967295)::oid", [`gregale_mcp_tasks:execution:${namespace}`]);
      return result.rows.length > 0;
    }, 'heartbeat should wait for the namespace capacity lock');
    await lock.query("UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1", [namespace]);
    await lock.query('COMMIT');
    assert.deepEqual(await renewal, { owned: false, cancelRequested: false });
  } finally {
    await lock.query('ROLLBACK').catch(() => {});
    lock.release();
    await renewal;
  }
});


test('PostgreSQL persists retry delays, releases capacity and bounds retries by attempts and TTL', postgresOnly, async t => {
  const { store, pool, namespace, ownerKey } = await harness(t);
  const record = await create(store, 'alice');
  const lease = await store.claim(3, 60_000);
  assert.equal(await store.fail(lease.task_id, lease.lease_token, { message: 'sanitized' }, { retryable: true, maxAttempts: 2, retryDelayMs: 30_000 }), 'queued');
  const restarted = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
  await restarted.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  assert.equal(await restarted.claim(3, 60_000), null, 'persisted retry delay survives store initialization');
  const metrics = await store.queueMetrics();
  assert.equal(metrics.runningTasks, 0);
  assert.equal(metrics.retryWaitingTasks, 1);
  assert.equal(metrics.failedTasks, 0);
  assert.equal(await store.fail(lease.task_id, lease.lease_token, {}, { retryable: true }), null, 'old lease cannot change retry state');
  await pool.query("UPDATE gregale_mcp_tasks SET next_attempt_at = clock_timestamp() - interval '1 second' WHERE namespace = $1", [namespace]);
  const retry = await store.claim(3, 60_000);
  assert.equal(retry.task_id, record.task_id);
  assert.equal(retry.attempt_count, 2);
  assert.equal(await store.fail(retry.task_id, retry.lease_token, {}, { retryable: true, maxAttempts: 2 }), 'failed');
  assert.equal((await store.queueMetrics()).failedTasks, 1);
  await create(store, 'bob');
  const short = await store.claim(3, 60_000);
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() + interval '1 second' WHERE task_id = $1", [short.task_id]);
  assert.equal(await store.fail(short.task_id, short.lease_token, {}, { retryable: true, retryDelayMs: 2000 }), 'failed', 'a retry cannot outlive TTL');
  await create(store, 'charlie');
  const cancelled = await store.claim(3, 60_000);
  await store.requestCancel({ taskID: cancelled.task_id, authInfo: principal('charlie'), authMode: 'external-oauth' });
  assert.equal(await store.fail(cancelled.task_id, cancelled.lease_token, {}, { retryable: true }), 'cancelled');
  await create(store, 'expired-owner');
  const expired = await store.claim(3, 60_000);
  await pool.query("UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE task_id = $1", [expired.task_id]);
  assert.equal(await store.fail(expired.task_id, expired.lease_token, {}, { retryable: true }), null, 'expired leases cannot schedule retries');
});

test('runtime retries only explicit transient errors with bounded jitter and sanitized errors', postgresOnly, async t => {
  const { store, pool, namespace } = await harness(t);
  let executions = 0;
  let permanentExecutions = 0;
  const runtime = createMcpTaskRuntime({ store, pollIntervalMs: 500, maxAttempts: 2, retryBaseDelayMs: 100, retryMaxDelayMs: 100, handlers: {
    build_report: { version: '1', async execute(args) {
      if (args.permanent) { permanentExecutions++; throw new Error('private permanent error'); }
      if (++executions === 1) throw new RetryableMcpTaskError('private provider detail');
      return { ok: true };
    } },
  } });
  await runtime.start();
  t.after(() => runtime.stop());
  const record = await create(store, 'alice');
  await eventually(async () => (await get(store, record.task_id, 'alice'))?.status === 'completed', 'retry did not complete');
  assert.equal(executions, 2);
  assert.equal((await get(store, record.task_id, 'alice')).attempt_count, 2);
  const data = await pool.query('SELECT error_encrypted, next_attempt_at FROM gregale_mcp_tasks WHERE namespace = $1', [namespace]);
  assert.equal(data.rows[0].error_encrypted, null);
  assert.equal(data.rows[0].next_attempt_at, null);
  const permanent = await create(store, 'bob', { permanent: true });
  await eventually(async () => (await get(store, permanent.task_id, 'bob'))?.status === 'failed', 'permanent failure was not recorded');
  await delay(600);
  assert.equal(permanentExecutions, 1, 'ordinary errors are never retried');
  assert.deepEqual((await get(store, permanent.task_id, 'bob')).error, { code: -32603, message: 'Task execution failed' });
});


test('payload rotation preserves ownership, input state and mixed-key results, and guards key removal', postgresOnly, async t => {
  const { store, pool, namespace, ownerKey } = await harness(t);
  const legacy = await create(store, 'alice');
  const first = await store.claim(3, 60000);
  await store.complete(first.task_id, first.lease_token, { legacy: true });
  const a = 'a'.repeat(48), b = 'b'.repeat(48);
  const options = { pool, namespace, ownerKey, ttlMs: 60000 };
  const old = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'a', keys: { a } } });
  await old.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  const task = await create(old, 'alice');
  const running = await old.claim(3, 60000);
  await old.requestInputs({ taskID: task.task_id, leaseToken: running.lease_token, requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } } });
  const rotated = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'b', keys: { a, b } } });
  await rotated.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  assert.deepEqual((await get(rotated, legacy.task_id, 'alice')).result, { legacy: true });
  assert.equal(await get(rotated, task.task_id, 'bob'), null);
  await rotated.updateInputs({ taskID: task.task_id, authInfo: principal('alice'), authMode: 'external-oauth', inputResponses: { approval: { action: 'accept', content: {} } } });
  const resumed = await rotated.claim(3, 60000);
  assert.equal(resumed.task_id, task.task_id);
  assert.deepEqual(resumed.arguments, { report: 'weekly' });
  await rotated.complete(resumed.task_id, resumed.lease_token, { rotated: true });
  assert.deepEqual((await get(rotated, task.task_id, 'alice')).result, { rotated: true });
  const withoutOld = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'b', keys: { b } } });
  await assert.rejects(withoutOld.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /unavailable/);
  await assert.rejects(createPostgresMcpTaskStore(options).initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /unavailable/, 'falling back to legacy writes cannot omit live versioned keys');
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE task_id = $1", [task.task_id]);
  await withoutOld.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  const changedSecret = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'a', keys: { a: b } } });
  await assert.rejects(changedSecret.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /must remain stable/);
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1", [namespace]);
  const changedOwner = createPostgresMcpTaskStore({ ...options, ownerKey: 'new-owner'.repeat(8), encryptionKeys: { activeKeyId: 'b', keys: { b } } });
  await assert.rejects(changedOwner.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /must remain stable/);
});

test('worker inventory expires, respects namespace boundaries and distinguishes absent workers from unsupported handlers', postgresOnly, async t => {
  const { store, pool, namespace } = await harness(t);
  await create(store, 'alice');
  const incompatible = randomUUID(), compatible = randomUUID();
  await store.workerHeartbeat(incompatible, [{ name: 'build_report', version: '2' }]);
  await pool.query("INSERT INTO gregale_mcp_task_workers (namespace, worker_id, handlers, heartbeat_at, expires_at) VALUES ($1, $2::uuid, $3::jsonb, clock_timestamp(), clock_timestamp() + interval '90 seconds')", [`${namespace}-other`, randomUUID(), JSON.stringify([{ name: 'build_report', version: '1' }])]);
  assert.equal((await store.queueMetrics()).activeWorkers, 1);
  assert.equal((await store.queueMetrics()).unsupportedHandlerTasks, 1);
  await store.workerHeartbeat(compatible, [{ name: 'build_report', version: '1' }]);
  assert.equal((await store.queueMetrics()).activeWorkers, 2);
  assert.equal((await store.queueMetrics()).unsupportedHandlerTasks, 0);
  await pool.query("UPDATE gregale_mcp_task_workers SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND worker_id = $2", [namespace, compatible]);
  assert.equal((await store.queueMetrics()).unsupportedHandlerTasks, 1);
  await store.workerStopped(incompatible);
  const absent = await store.queueMetrics();
  assert.equal(absent.activeWorkers, 0);
  assert.equal(absent.unsupportedHandlerTasks, 0);
  const runtime = createMcpTaskRuntime({ store, handlers: { build_report: { version: '1', async execute() { return {}; } } } });
  await runtime.start();
  try { assert.equal((await store.queueMetrics()).activeWorkers, 1); } finally { await runtime.stop(); }
  assert.equal((await store.queueMetrics()).activeWorkers, 0, 'shutdown withdraws the worker registration');
});

test('candidate compatibility includes delayed retries, paused input and both lease states', postgresOnly, async t => {
  const { pool, namespace, store, ownerKey } = await harness(t);
  const { checkMcpTaskCompatibility } = await import('../task-compatibility.js');
  const tasks = await Promise.all(Array.from({ length: 7 }, () => create(store, 'alice')));
  const ids = tasks.map(task => task.task_id);
  await pool.query("UPDATE gregale_mcp_tasks SET next_attempt_at = clock_timestamp() + interval '30 seconds' WHERE namespace = $1 AND task_id = $2", [namespace, ids[1]]);
  await pool.query("UPDATE gregale_mcp_tasks SET status = 'input_required', input_state_encrypted = arguments_encrypted WHERE namespace = $1 AND task_id = $2", [namespace, ids[2]]);
  for (const [id, interval] of [[ids[3], '30 seconds'], [ids[4], '-1 second']]) {
    await pool.query("UPDATE gregale_mcp_tasks SET status = 'running', lease_token = $3, lease_expires_at = clock_timestamp() + $4::interval WHERE namespace = $1 AND task_id = $2", [namespace, id, randomUUID(), interval]);
  }
  await pool.query("UPDATE gregale_mcp_tasks SET status = 'completed' WHERE namespace = $1 AND task_id = $2", [namespace, ids[5]]);
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2", [namespace, ids[6]]);
  const other = createPostgresMcpTaskStore({ pool, namespace: namespace + '-other', ownerKey, ttlMs: 60_000 });
  await other.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  await create(other, 'alice');
  const handlers = { build_report: { version: '2', async execute() {} } };
  const blocked = await checkMcpTaskCompatibility({ pool, namespace, handlers });
  assert.equal(blocked.ok, false);
  assert.equal(blocked.gaps.length, 1);
  assert.deepEqual({ ...blocked.gaps[0], latestExpiry: undefined }, { tool: 'build_report', version: '1', taskCount: 5, queuedCount: 2, runningCount: 2, inputRequiredCount: 1, latestExpiry: undefined });
  assert.ok(Date.parse(blocked.gaps[0].latestExpiry) > Date.now());
  handlers.build_report.previousVersions = { '1': async () => {} };
  assert.deepEqual(await checkMcpTaskCompatibility({ pool, namespace, handlers }), { ok: true, gaps: [] });
  delete handlers.build_report.previousVersions;
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1", [namespace]);
  assert.equal((await checkMcpTaskCompatibility({ pool, namespace, handlers })).ok, true);
});

test('draining registrations are separate from available handler coverage', postgresOnly, async t => {
  const { store } = await harness(t);
  await create(store, 'alice');
  const id = randomUUID();
  await store.workerHeartbeat(id, [{ name: 'build_report', version: '1' }]);
  await store.workerDraining(id);
  const metrics = await store.queueMetrics();
  assert.equal(metrics.activeWorkers, 0);
  assert.equal(metrics.drainingWorkers, 1);
  await store.workerStopped(id);
  assert.equal((await store.queueMetrics()).drainingWorkers, 0);
});

test('Task doctor and compatibility command return safe deployment reports', postgresOnly, async t => {
  const { pool, schema, namespace, ownerKey, store } = await harness(t);
  const { mkdtemp, copyFile, writeFile, rm } = await import('node:fs/promises');
  const { execFile } = await import('node:child_process');
  const { promisify } = await import('node:util');
  const root = dirname(dirname(fileURLToPath(import.meta.url)));
  const fixture = await mkdtemp(join(root, 'doctor-fixture-'));
  t.after(() => rm(fixture, { recursive: true, force: true }));
  for (const file of ['task-doctor.js', 'tasks-compatibility.js', 'task-compatibility.js', 'tasks.js', 'task-runtime.js', 'task-store.js', 'task-crypto.js', 'task-admission.js', 'tasks-admission.js', 'task-metrics.js', 'task-limits.json']) await copyFile(join(root, file), join(fixture, file));
  await writeFile(join(fixture, 'package.json'), JSON.stringify({ type: 'module' }));
  await writeFile(join(fixture, 'gregale-mcp.json'), JSON.stringify({ tasks: { enabled: true, database_url_env: 'DOCTOR_DATABASE', owner_key_env: 'DOCTOR_OWNER' } }));
  const url = new URL(databaseURL);
  url.searchParams.set('options', `-c search_path=${schema}`);
  const env = { ...process.env, DOCTOR_DATABASE: url.href, DOCTOR_OWNER: ownerKey, MCP_TASK_NAMESPACE: namespace };
  const run = async (script, overrides = {}, args = []) => {
    try { const result = await promisify(execFile)(process.execPath, [script, ...args], { cwd: fixture, env: { ...env, ...overrides }, timeout: 15000 }); return { code: 0, report: JSON.parse(result.stdout) }; }
    catch (error) { return { code: error.code, report: JSON.parse(error.stdout) }; }
  };
  await store.workerHeartbeat(randomUUID(), [{ name: 'build_report', version: '1' }]);
  const ready = await run('task-doctor.js');
  assert.equal(ready.code, 0, JSON.stringify(ready.report));
  assert.equal(ready.report.ok, true);
  const safe = await run('task-doctor.js', { DOCTOR_OWNER: 'secret-that-must-not-appear' });
  assert.equal(safe.code, 1);
  assert.equal(JSON.stringify(safe.report).includes('secret-that-must-not-appear'), false);
  const task = await create(store, 'alice');
  await pool.query("UPDATE gregale_mcp_tasks SET handler_version = 'retired', next_attempt_at = clock_timestamp() + interval '30 seconds' WHERE namespace = $1 AND task_id = $2", [namespace, task.task_id]);
  const blocked = await run('tasks-compatibility.js', { DOCTOR_OWNER: '' });
  assert.equal(blocked.code, 1);
  assert.equal(blocked.report.gaps[0].version, 'retired');
  assert.equal(blocked.report.gaps[0].taskCount, 1);
  const doctor = await run('task-doctor.js');
  assert.equal(doctor.code, 1);
  assert.ok(doctor.report.checks.some(check => check.name === 'retained_handler_coverage' && check.status === 'failed'));
  assert.equal(doctor.report.handlerCompatibility.gaps[0].taskCount, 1);
  const disabled = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['disable', 'build_report', '1']);
  assert.equal(disabled.code, 0);
  assert.equal(disabled.report.versions.find(entry => entry.version === '1').state, 'draining');
  const fenced = await run('task-doctor.js');
  assert.equal(fenced.code, 1);
  assert.ok(fenced.report.checks.some(check => check.name === 'task_admission' && check.status === 'failed'));
  const retired = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['retire', 'build_report', '1']);
  assert.equal(retired.code, 0);
  assert.equal(retired.report.versions.find(entry => entry.version === '1').state, 'retired');
  const audit = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['audit']);
  assert.equal(audit.code, 0);
  assert.ok(audit.report.events.some(event => event.handler_version === '1' && event.retired_at));
});

test('admission disable fences old producers, survives restart and requires draining before retirement', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const controller = createMcpTaskAdmissionController({ pool, namespace });
  const task = await create(store, 'alice');
  await assert.rejects(controller.change('retire', 'build_report', '1'), /Disable admission/);
  const disabled = await controller.change('disable', 'build_report', '1');
  const state = disabled.versions.find(version => version.tool === 'build_report' && version.version === '1');
  assert.equal(state.state, 'draining');
  assert.equal(state.retainedTasks, 1);
  assert.equal(state.canRetire, false);
  await assert.rejects(create(store, 'alice'), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
  // Legacy writers have no application-side policy check; the trigger fences
  // even a plain INSERT, with no dependence on a newly deployed runtime.
  const legacyInsert = () => pool.query(`INSERT INTO gregale_mcp_tasks (namespace, task_id, owner_hash, tool_name, handler_version, arguments_encrypted, status, expires_at)
    VALUES ($1, $2, $3, 'build_report', '1', $4, 'queued', clock_timestamp() + interval '1 minute')`, [namespace, randomUUID(), randomBytes(32), Buffer.from('legacy-payload')]);
  await assert.rejects(legacyInsert(), error => error.constraint === 'gregale_mcp_task_admission');
  await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  await assert.rejects(create(store, 'alice'), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
  await assert.rejects(controller.change('retire', 'build_report', '1'), /Retained Tasks/);
  const leased = await store.claim(3, 30000, [{ name: 'build_report', version: '1' }]);
  assert.equal(leased.task_id, task.task_id, 'disable leaves existing Tasks claimable');
  await store.complete(leased.task_id, leased.lease_token, { content: [] });
  const retired = await controller.change('retire', 'build_report', '1');
  assert.equal(retired.versions.find(version => version.version === '1').state, 'retired');
  await controller.change('disable', 'build_report', '1');
  assert.equal((await controller.status()).versions.find(version => version.version === '1').state, 'retired', 'idempotent disable preserves retirement');
  await controller.change('allow', 'build_report', '1');
  assert.ok(await create(store, 'alice'), 'explicit allow supports operator rollback');
  const events = (await controller.audit()).events.filter(event => event.handler_version === '1');
  assert.equal(events.length, 4, 'register, disable, retire and allow are audited once each');
  assert.ok(events.every(event => typeof event.database_role === 'string' && !Object.hasOwn(event, 'arguments')));
  const other = createMcpTaskAdmissionController({ pool, namespace: namespace + '-other' });
  assert.deepEqual(await other.audit(), { events: [] });
  await assert.rejects(store.create({ toolName: 'build_report', handlerVersion: 'unknown', args: {}, authMode: 'open' }), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
});

test('disable waits for admitted transactions and rejects inserts waiting behind it', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const controller = createMcpTaskAdmissionController({ pool, namespace });
  const admitted = await pool.connect();
  const insertSQL = `INSERT INTO gregale_mcp_tasks (namespace, task_id, owner_hash, tool_name, handler_version, arguments_encrypted, status, expires_at)
    VALUES ($1, $2, $3, 'build_report', '1', $4, 'queued', clock_timestamp() + interval '1 minute')`;
  const params = () => [namespace, randomUUID(), randomBytes(32), Buffer.from('legacy-payload')];
  try {
    await admitted.query('BEGIN');
    await admitted.query(insertSQL, params());
    let disabled = false;
    const disabling = controller.change('disable', 'build_report', '1').then(() => { disabled = true; });
    await delay(50);
    assert.equal(disabled, false, 'disable cannot return while an allowed insert is uncommitted');
    await admitted.query('COMMIT');
    await disabling;
    await assert.rejects(pool.query(insertSQL, params()), error => error.constraint === 'gregale_mcp_task_admission');
  } finally { await admitted.query('ROLLBACK').catch(() => {}); admitted.release(); }
  // Exercise a producer that began while disable was still uncommitted.
  await controller.change('allow', 'build_report', '1');
  const disabling = await pool.connect();
  try {
    await disabling.query('BEGIN');
    await disabling.query('UPDATE gregale_mcp_task_admission SET enabled = false WHERE namespace = $1 AND tool_name = $2 AND handler_version = $3', [namespace, 'build_report', '1']);
    const insertion = pool.query(insertSQL, params());
    const rejected = assert.rejects(insertion, error => error.constraint === 'gregale_mcp_task_admission');
    await delay(50);
    await disabling.query('COMMIT');
    await rejected;
  } finally { await disabling.query('ROLLBACK').catch(() => {}); disabling.release(); }
  // Retire considers every retained status, not only currently eligible work.
  await assert.rejects(controller.change('retire', 'build_report', '1'), /Retained Tasks/);
});

test('first admission migration seeds retained versions without reopening disabled entries', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const controller = createMcpTaskAdmissionController({ pool, namespace });
  const legacyTask = await create(store, 'alice');
  await pool.query("UPDATE gregale_mcp_tasks SET handler_version = 'legacy' WHERE namespace = $1 AND task_id = $2", [namespace, legacyTask.task_id]);
  await controller.change('disable', 'build_report', '1');
  await pool.query(`DROP TRIGGER gregale_mcp_tasks_admission_${createHash('md5').update(namespace).digest('hex')} ON gregale_mcp_tasks`);
  await pool.query('DELETE FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1', [namespace]);
  await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  const status = await controller.status();
  assert.equal(status.versions.find(entry => entry.version === 'legacy').state, 'allowed');
  assert.equal(status.versions.find(entry => entry.version === '1').state, 'draining');
  await assert.rejects(create(store, 'alice'), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
  assert.ok(await store.create({ toolName: 'build_report', handlerVersion: 'legacy', args: {}, authMode: 'open' }));
  await assert.rejects(store.create({ toolName: 'build_report', handlerVersion: 'unknown', args: {}, authMode: 'open' }), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
});

test('namespace activation preserves other queues and fences transactions with older snapshots', postgresOnly, async t => {
  const { pool, namespace, ownerKey, store } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const otherNamespace = namespace + "-'unupgraded";
  const other = createPostgresMcpTaskStore({ pool, namespace: otherNamespace, ownerKey, ttlMs: 60_000 });
  assert.ok(await create(other, 'alice'), 'an unupgraded namespace keeps its previous admission behavior');
  const controller = createMcpTaskAdmissionController({ pool, namespace: otherNamespace });
  assert.equal((await controller.status()).enforced, false);
  await assert.rejects(controller.change('disable', 'build_report', '1'), /Initialize admission/);
  const stale = await pool.connect();
  try {
    await stale.query('BEGIN ISOLATION LEVEL REPEATABLE READ');
    assert.equal((await stale.query('SELECT namespace FROM gregale_mcp_task_admission_namespaces WHERE namespace = $1', [otherNamespace])).rows.length, 0);
    await other.initialize({ admissionHandlers: [{ name: 'build_report', version: '2' }] });
    assert.equal((await controller.status()).enforced, true);
    await controller.change('disable', 'build_report', '1');
    await assert.rejects(stale.query(`INSERT INTO gregale_mcp_tasks (namespace, task_id, owner_hash, tool_name, handler_version, arguments_encrypted, status, expires_at)
      VALUES ($1, $2, $3, 'build_report', '1', $4, 'queued', clock_timestamp() + interval '1 minute')`, [otherNamespace, randomUUID(), randomBytes(32), Buffer.from('legacy')]), error => error.constraint === 'gregale_mcp_task_admission' || error.code === '40001');
  } finally { await stale.query('ROLLBACK').catch(() => {}); stale.release(); }
  await assert.rejects(create(other, 'alice'), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
  assert.ok(await create(store, 'alice'), 'disabling another namespace does not affect this queue');
});

test('admission status and administration fail closed when the enforcement trigger is disabled', postgresOnly, async t => {
  const { pool, namespace } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const controller = createMcpTaskAdmissionController({ pool, namespace });
  const trigger = `gregale_mcp_tasks_admission_${createHash('md5').update(namespace).digest('hex')}`;
  await pool.query(`ALTER TABLE gregale_mcp_tasks DISABLE TRIGGER ${trigger}`);
  assert.equal((await controller.status()).enforced, false);
  await assert.rejects(controller.change('disable', 'build_report', '1'), /Initialize admission/);
  await pool.query(`ALTER TABLE gregale_mcp_tasks ENABLE TRIGGER ${trigger}`);
  assert.equal((await controller.status()).enforced, true);
});
