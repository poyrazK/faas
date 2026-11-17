// Expected-failing probes; PostgreSQL work stays inside a unique test schema.
import assert from 'node:assert/strict';
import test from 'node:test';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const starter = process.env.MCP_INVESTIGATION_NODE_STARTER;
if (!starter) throw new Error('Set MCP_INVESTIGATION_NODE_STARTER to a dependency-installed starter copy');
const fromStarter = file => import(pathToFileURL(resolve(starter, file)).href);
const { createMcpTaskRuntime } = await fromStarter('tasks.js');

function deferred() {
  let resolvePromise;
  const promise = new Promise(resolve => { resolvePromise = resolve; });
  return {promise, resolve: resolvePromise};
}

test('runtime startup finishes while a fast-task backlog remains busy', {timeout: 5000}, async t => {
  let accepting = true;
  let completed = 0;
  const store = {
    async initialize() {},
    async claim() {
      // Model ordinary database latency with a queue replenished by callers.
      await delay(10);
      if (!accepting) return null;
      return {task_id: randomUUID(), lease_token: randomUUID(), tool_name: 'fast',
        handler_version: '1', arguments: {}, attempt_count: 1};
    },
    async complete() { completed += 1; },
    async fail() { assert.fail('the valid fast handler must not fail'); },
  };
  const runtime = createMcpTaskRuntime({store, workerConcurrency: 2, pollIntervalMs: 500,
    handlers: {fast: {version: '1', validate() {}, execute: async () => ({content: []})}}});
  let outcome = 'still starting';
  const starting = runtime.start().then(() => { outcome = 'started'; });
  try {
    await delay(200);
    t.diagnostic(`startup=${outcome}; completed handlers before readiness=${completed}`);
    assert.ok(completed >= 3, 'fixture must sustain successful work');
    assert.equal(outcome, 'started', 'startup waits for queue drain rather than initialization');
  } finally {
    accepting = false;
    await runtime.stop();
    await starting;
  }
});

const databaseURL = process.env.MCP_TASKS_TEST_DATABASE_URL;
test('shutdown releases a task whose committed claim returns during drain', {
  timeout: 15000, skip: databaseURL ? false : 'set MCP_TASKS_TEST_DATABASE_URL to a disposable PostgreSQL database',
}, async t => {
  const {Pool} = createRequire(resolve(starter, 'package.json'))('pg');
  const {createPostgresMcpTaskStore} = await fromStarter('task-store.js');
  const schema = `investigation_mcp_${randomUUID().replaceAll('-', '')}`;
  const admin = new Pool({connectionString: databaseURL, max: 1, connectionTimeoutMillis: 3000});
  let pool;
  let runtime;
  let createdSchema = false;
  const claimCommitted = deferred();
  const allowClaimReturn = deferred();
  try {
    await admin.query(`CREATE SCHEMA ${schema}`);
    createdSchema = true;
    pool = new Pool({connectionString: databaseURL, max: 4, connectionTimeoutMillis: 3000,
      statement_timeout: 5000, options: `-c search_path=${schema}`});
    const namespace = `probe-${randomUUID()}`;
    const store = createPostgresMcpTaskStore({pool, namespace,
      ownerKey: '012345678901234567890123456789012345678901234567', ttlMs: 60000, maxAttempts: 1});
    await store.migrate({admissionHandlers: [{name: 'fast', version: '1'}]});
    let holdClaims = false;
    let executions = 0;
    const controlledStore = {...store, async claim(...args) {
      const task = await store.claim(...args);
      if (task && holdClaims) {
        claimCommitted.resolve(task);
        await allowClaimReturn.promise;
      }
      return task;
    }};
    runtime = createMcpTaskRuntime({store: controlledStore, pollIntervalMs: 500,
      maxAttempts: 1, handlers: {fast: {version: '1', validate() {}, execute: async () => {
        executions += 1;
        return {content: []};
      }}}});
    await runtime.start(); // Initially empty, so startup is not part of this race.
    holdClaims = true;
    const created = await runtime.create('fast', {}, undefined, 'open');
    const claimed = await Promise.race([claimCommitted.promise, delay(2000, null)]);
    assert.ok(claimed, 'fixture must commit a claim before shutdown');
    const stopping = runtime.stop();
    allowClaimReturn.resolve();
    const stopped = await stopping;
    await delay(30);
    const row = (await pool.query('SELECT status,attempt_count,resume_pending FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2',
      [namespace, created.taskId])).rows[0];
    // Advance only the fixture lease expiry so the native claim logic can
    // demonstrate what the unexecuted attempt costs at its normal expiry.
    if (row.status === 'running') {
      await pool.query("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE namespace=$1 AND task_id=$2 AND status='running'",
        [namespace, created.taskId]);
      await store.claim(1, 30000);
    }
    const afterExpiry = (await pool.query('SELECT status FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2',
      [namespace, created.taskId])).rows[0];
    t.diagnostic(`stop=${JSON.stringify(stopped)}; after shutdown=${JSON.stringify(row)}; after lease expiry=${afterExpiry.status}; handler executions=${executions}`);
    assert.equal(executions, 0);
    assert.equal(row.status, 'queued', 'graceful shutdown leaves a never-executed task leased and consumes its attempt');
    assert.equal(row.resume_pending, true);
    assert.equal(afterExpiry.status, 'queued');
  } finally {
    allowClaimReturn.resolve();
    await runtime?.stop();
    await pool?.end();
    if (createdSchema) await admin.query(`DROP SCHEMA ${schema} CASCADE`);
    await admin.end();
  }
});
