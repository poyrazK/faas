import { fork } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Pool } from 'pg';
import { createPostgresMcpTaskStore } from '../task-store.js';

const RUNNER = 'gregale-mcp-tasks-restart-v1';
const CHECK = 'tasks_restart_replicas';
const CHECK_NAMES = [
  'initial_worker_crash',
  'two_replica_recovery',
  'stale_lease_fenced',
  'result_owner_isolation',
  'final_attempt_input_resume',
];

class QualificationFailure extends Error {
  constructor(stage) {
    super('qualification_failed');
    this.stage = stage;
  }
}

function targetFromArgs(argv) {
  if (argv.length !== 3 || argv[0] !== '--target' || argv[2] !== '--confirm-disposable-database') {
    throw new QualificationFailure('arguments');
  }
  const target = argv[1];
  if (!target?.trim() || target.length > 512 || [...target].some(char => char.charCodeAt(0) < 32)) {
    throw new QualificationFailure('arguments');
  }
  return target.trim();
}

function principal(subject) {
  return {
    resource: new URL('https://qualification.example/mcp'),
    extra: { subject },
    clientId: 'gregale-mcp-qualification',
  };
}

function createReceipt(target) {
  return {
    version: 1,
    runner: RUNNER,
    check: CHECK,
    status: 'failed',
    target,
    observed_at: new Date().toISOString(),
    checks: Object.fromEntries(CHECK_NAMES.map(name => [name, 'failed'])),
    observations: {
      replacement_replicas: 0,
      recovery_attempt: 0,
      recovered_handler_executions: 0,
      final_attempt: 0,
      resumed_attempt: 0,
    },
    failures: [],
  };
}

function workerProcess(workerID, config) {
  const workerPath = join(dirname(fileURLToPath(import.meta.url)), 'tasks-restart-worker.js');
  const child = fork(workerPath, [], {
    stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
    env: {
      PATH: process.env.PATH || process.execPath,
      MCP_TASKS_TEST_DATABASE_URL: config.databaseURL,
      MCP_TASKS_TEST_NAMESPACE: config.namespace,
      MCP_TASKS_TEST_OWNER_KEY: config.ownerKey,
      MCP_TASKS_TEST_SCHEMA: config.schema,
      MCP_TASKS_TEST_WORKER_ID: workerID,
    },
  });
  const messages = [];
  const waiters = [];
  let exitError;

  child.on('message', message => {
    const index = waiters.findIndex(waiter => waiter.type === message?.type || message?.type === 'fatal');
    if (index < 0) {
      messages.push(message);
      return;
    }
    const [waiter] = waiters.splice(index, 1);
    clearTimeout(waiter.timer);
    if (message?.type === 'fatal') waiter.reject(new QualificationFailure('worker_startup'));
    else waiter.resolve(message);
  });
  child.once('error', () => {
    exitError = new QualificationFailure('worker_process');
    for (const waiter of waiters.splice(0)) {
      clearTimeout(waiter.timer);
      waiter.reject(exitError);
    }
  });
  child.once('exit', () => {
    exitError ||= new QualificationFailure('worker_exit');
    for (const waiter of waiters.splice(0)) {
      clearTimeout(waiter.timer);
      waiter.reject(exitError);
    }
  });

  return {
    child,
    next(type, timeoutMs = 15_000) {
      const index = messages.findIndex(message => message?.type === type || message?.type === 'fatal');
      if (index >= 0) {
        const [message] = messages.splice(index, 1);
        if (message?.type === 'fatal') return Promise.reject(new QualificationFailure('worker_startup'));
        return Promise.resolve(message);
      }
      if (exitError || child.exitCode !== null || child.signalCode !== null) {
        return Promise.reject(exitError || new QualificationFailure('worker_exit'));
      }
      return new Promise((resolve, reject) => {
        const waiter = { type, resolve, reject };
        waiter.timer = setTimeout(() => {
          const waiterIndex = waiters.indexOf(waiter);
          if (waiterIndex >= 0) waiters.splice(waiterIndex, 1);
          reject(new QualificationFailure('worker_timeout'));
        }, timeoutMs);
        waiters.push(waiter);
      });
    },
  };
}

async function waitForExit(child, signal) {
  if (child.exitCode !== null || child.signalCode !== null) return child.signalCode;
  const exited = once(child, 'exit');
  child.kill(signal);
  try {
    const [code, actualSignal] = await Promise.race([
      exited,
      delay(5_000).then(() => { throw new QualificationFailure('worker_shutdown'); }),
    ]);
    return actualSignal || code;
  } catch (error) {
    if (!(error instanceof QualificationFailure) || error.stage !== 'worker_shutdown') throw error;
    child.kill('SIGKILL');
    await Promise.race([once(child, 'exit'), delay(5_000)]);
    if (child.exitCode === null && child.signalCode === null) throw error;
    return child.signalCode || child.exitCode;
  }
}

async function eventually(fn, stage, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await fn();
    if (value) return value;
    await delay(50);
  }
  throw new QualificationFailure(stage);
}

async function runQualification(receipt) {
  const databaseURL = process.env.MCP_TASKS_TEST_DATABASE_URL;
  if (!databaseURL) throw new QualificationFailure('database_configuration');

  const schema = `gregale_mcp_qualification_${randomUUID().replaceAll('-', '')}`;
  const namespace = `qualification-${randomUUID()}`;
  const ownerKey = randomBytes(48).toString('base64url');
  const adminPool = new Pool({ connectionString: databaseURL, max: 1, connectionTimeoutMillis: 5_000, statement_timeout: 10_000 });
  adminPool.on('error', () => {});
  let schemaCreated = false;
  let pool;
  let store;
  const workers = [];
  let stage = 'database_setup';

  try {
    await adminPool.query(`CREATE SCHEMA ${schema}`);
    schemaCreated = true;
    pool = new Pool({
      connectionString: databaseURL,
      max: 12,
      connectionTimeoutMillis: 5_000,
      statement_timeout: 10_000,
      options: `-c search_path=${schema}`,
    });
    pool.on('error', () => {});
    store = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
    await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'qualification_input', version: '1' }] });
    await store.initialize();
    await pool.query(`
      CREATE TABLE gregale_mcp_qualification_runs (
        run_id uuid NOT NULL,
        worker_id text NOT NULL,
        started_at timestamptz NOT NULL DEFAULT clock_timestamp()
      )
    `);

    const alice = principal('qualification-owner');
    const firstWorker = workerProcess('crashed-worker', { databaseURL, namespace, ownerKey, schema });
    workers.push(firstWorker);
    await firstWorker.next('ready');
    const runID = randomUUID();
    const task = await store.create({
      toolName: 'build_report',
      handlerVersion: '1',
      args: { qualificationRunID: runID },
      authInfo: alice,
      authMode: 'external-oauth',
    });
    const firstExecution = await firstWorker.next('started');
    const firstLeaseQuery = await pool.query(
      'SELECT status, attempt_count, lease_token::text AS lease_token FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
      [namespace, task.task_id],
    );
    if (
      firstExecution.taskId !== task.task_id
      || firstExecution.workerID !== 'crashed-worker'
      || firstLeaseQuery.rows[0]?.status !== 'running'
      || firstLeaseQuery.rows[0]?.attempt_count !== 1
      || !firstLeaseQuery.rows[0]?.lease_token
    ) throw new QualificationFailure('initial_worker_claim');
    const staleLeaseToken = firstLeaseQuery.rows[0].lease_token;
    if (await waitForExit(firstWorker.child, 'SIGKILL') !== 'SIGKILL') {
      throw new QualificationFailure('initial_worker_crash');
    }
    receipt.checks.initial_worker_crash = 'passed';

    const replicaA = workerProcess('replacement-a', { databaseURL, namespace, ownerKey, schema });
    const replicaB = workerProcess('replacement-b', { databaseURL, namespace, ownerKey, schema });
    workers.push(replicaA, replicaB);
    await replicaA.next('ready');
    await replicaB.next('ready');
    receipt.observations.replacement_replicas = 2;
    await pool.query(
      "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
      [namespace, task.task_id],
    );

    stage = 'two_replica_recovery';
    const firstRecovered = await Promise.race([
      replicaA.next('started').then(message => ({ worker: replicaA, message })),
      replicaB.next('started').then(message => ({ worker: replicaB, message })),
    ]);
    const recoveredLease = await pool.query(
      'SELECT status, attempt_count, lease_token::text AS lease_token FROM gregale_mcp_tasks WHERE namespace = $1 AND task_id = $2::uuid',
      [namespace, task.task_id],
    );
    if (
      firstRecovered.message.taskId !== task.task_id
      || recoveredLease.rows[0]?.status !== 'running'
      || recoveredLease.rows[0]?.attempt_count !== 2
      || recoveredLease.rows[0]?.lease_token === staleLeaseToken
    ) throw new QualificationFailure('two_replica_recovery');
    receipt.observations.recovery_attempt = recoveredLease.rows[0].attempt_count;
    receipt.checks.two_replica_recovery = 'passed';

    stage = 'stale_lease_fenced';
    const staleWrite = await store.complete(task.task_id, staleLeaseToken, {
      content: [{ type: 'text', text: 'stale result' }],
    });
    if (staleWrite !== null) throw new QualificationFailure('stale_lease_fenced');
    receipt.checks.stale_lease_fenced = 'passed';

    stage = 'result_owner_isolation';
    if (await store.get({ taskID: task.task_id, authInfo: principal('other-owner'), authMode: 'external-oauth' }) !== null) {
      throw new QualificationFailure('result_owner_isolation');
    }
    firstRecovered.worker.child.send({ type: 'release' });
    const completed = await eventually(async () => {
      const value = await store.get({ taskID: task.task_id, authInfo: alice, authMode: 'external-oauth' });
      return value?.status === 'completed' ? value : null;
    }, 'task_completion');
    if (completed.result?.content?.[0]?.text !== 'qualification complete') {
      throw new QualificationFailure('task_result');
    }
    if (await store.get({ taskID: task.task_id, authInfo: principal('other-owner'), authMode: 'external-oauth' }) !== null) {
      throw new QualificationFailure('result_owner_isolation');
    }
    const executionCounts = await pool.query(
      `SELECT COUNT(*)::integer AS total_executions,
              COUNT(DISTINCT worker_id)::integer AS workers,
              COUNT(*) FILTER (WHERE worker_id <> $2)::integer AS recovered_executions
         FROM gregale_mcp_qualification_runs
        WHERE run_id = $1::uuid`,
      [runID, 'crashed-worker'],
    );
    if (
      executionCounts.rows[0]?.total_executions !== 2
      || executionCounts.rows[0]?.workers !== 2
      || executionCounts.rows[0]?.recovered_executions !== 1
    ) {
      throw new QualificationFailure('single_recovered_execution');
    }
    receipt.observations.recovered_handler_executions = executionCounts.rows[0].recovered_executions;
    receipt.checks.result_owner_isolation = 'passed';

    stage = 'final_attempt_input_resume';
    const inputTask = await store.create({
      toolName: 'qualification_input',
      handlerVersion: '1',
      args: {},
      authInfo: alice,
      authMode: 'external-oauth',
      inputMethods: ['elicitation/create:form'],
    });
    let finalLease;
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      finalLease = await store.claim(3, 30_000);
      if (!finalLease || finalLease.task_id !== inputTask.task_id || finalLease.attempt_count !== attempt) {
        throw new QualificationFailure('final_attempt_claim');
      }
      if (attempt < 3) await pool.query(
        "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
        [namespace, inputTask.task_id],
      );
    }
    receipt.observations.final_attempt = finalLease.attempt_count;
    const inputRequest = await store.requestInputs({
      taskID: inputTask.task_id,
      leaseToken: finalLease.lease_token,
      requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Qualification input' } } },
    });
    if (inputRequest.ready !== false) throw new QualificationFailure('input_request_persisted');
    if (!await store.updateInputs({
      taskID: inputTask.task_id,
      authInfo: alice,
      authMode: 'external-oauth',
      inputResponses: { approval: { action: 'accept' } },
    })) throw new QualificationFailure('input_response_accepted');
    const resumed = await store.claim(3, 30_000);
    if (!resumed || resumed.task_id !== inputTask.task_id || resumed.attempt_count !== 3) {
      throw new QualificationFailure('final_attempt_input_resume');
    }
    receipt.observations.resumed_attempt = resumed.attempt_count;
    await pool.query(
      "UPDATE gregale_mcp_tasks SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1 AND task_id = $2::uuid",
      [namespace, inputTask.task_id],
    );
    if (await store.claim(3, 30_000) !== null) throw new QualificationFailure('final_attempt_retry_limit');
    const terminal = await store.get({ taskID: inputTask.task_id, authInfo: alice, authMode: 'external-oauth' });
    if (terminal?.status !== 'failed' || terminal.attempt_count !== 3) {
      throw new QualificationFailure('final_attempt_retry_limit');
    }
    receipt.checks.final_attempt_input_resume = 'passed';

    receipt.status = 'passed';
  } catch (error) {
    const failureStage = error instanceof QualificationFailure ? error.stage : stage;
    receipt.failures.push({ stage: failureStage, reason: 'qualification_failed' });
  } finally {
    for (const worker of workers) {
      try {
        await waitForExit(worker.child, 'SIGTERM');
      } catch {
        receipt.failures.push({ stage: 'worker_cleanup', reason: 'qualification_failed' });
      }
    }
    try {
      await pool?.end();
    } catch {
      receipt.failures.push({ stage: 'database_cleanup', reason: 'qualification_failed' });
    }
    if (schemaCreated) {
      try {
        await adminPool.query(`DROP SCHEMA ${schema} CASCADE`);
      } catch {
        receipt.failures.push({ stage: 'database_cleanup', reason: 'qualification_failed' });
      }
    }
    await adminPool.end().catch(() => {
      receipt.failures.push({ stage: 'database_cleanup', reason: 'qualification_failed' });
    });
    if (receipt.failures.length > 0 || CHECK_NAMES.some(name => receipt.checks[name] !== 'passed')) {
      receipt.status = 'failed';
      if (receipt.failures.length === 0) receipt.failures.push({ stage, reason: 'qualification_failed' });
    }
  }
}

async function main() {
  let target = 'unknown';
  let receipt = createReceipt(target);
  try {
    target = targetFromArgs(process.argv.slice(2));
    receipt = createReceipt(target);
    await runQualification(receipt);
  } catch (error) {
    const stage = error instanceof QualificationFailure ? error.stage : 'qualification_runner';
    receipt.failures.push({ stage, reason: 'qualification_failed' });
  }
  process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
  if (receipt.status !== 'passed') process.exitCode = 1;
}

main().catch(() => {
  process.stdout.write(`${JSON.stringify(createReceipt('unknown'))}\n`);
  process.exitCode = 1;
});
