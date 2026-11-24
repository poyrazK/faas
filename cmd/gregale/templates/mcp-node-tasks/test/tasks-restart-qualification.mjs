import assert from 'node:assert/strict';
import { execFile, fork } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { generateKeyPair, SignJWT } from 'jose';
import { Pool } from 'pg';
import { createApp } from '../app.js';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime, mcpTaskHandlers } from '../tasks.js';

const RUNNER = 'gregale-mcp-tasks-restart-v1';
const REVIEWED_RUNNER = 'gregale-mcp-tasks-restart-v2';
const CHECK = 'tasks_restart_replicas';
const REVIEWED_CHECK = 'reviewed_input_worker_replacement';
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

function optionsFromArgs(argv) {
  if (argv[0] !== '--target' || argv[2] !== '--confirm-disposable-database'
      || (argv.length !== 3 && (argv.length !== 5 || argv[3] !== '--gregale' || !argv[4]))) {
    throw new QualificationFailure('arguments');
  }
  const target = argv[1];
  if (!target?.trim() || target.length > 512 || [...target].some(char => char.charCodeAt(0) < 32)) {
    throw new QualificationFailure('arguments');
  }
  return { target: target.trim(), gregale: argv[4] && resolve(argv[4]) };
}

function principal(subject) {
  return {
    resource: new URL('https://qualification.example/mcp'),
    extra: { subject },
    clientId: 'gregale-mcp-qualification',
  };
}

function createReceipt(target, gregale) {
  return {
    version: gregale ? 2 : 1,
    runner: gregale ? REVIEWED_RUNNER : RUNNER,
    check: CHECK,
    status: 'failed',
    target,
    observed_at: new Date().toISOString(),
    checks: Object.fromEntries((gregale ? [...CHECK_NAMES, REVIEWED_CHECK] : CHECK_NAMES).map(name => [name, 'failed'])),
    observations: {
      replacement_replicas: 0,
      recovery_attempt: 0,
      recovered_handler_executions: 0,
      final_attempt: 0,
      resumed_attempt: 0,
      ...(gregale ? { denied_input_updates: 0, accepted_input_updates: 0, reviewed_task_results: 0 } : {}),
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
      MCP_TASKS_TEST_REVIEWED_INPUT: config.reviewedInput ? '1' : '0',
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

async function qualifyReviewedInput({ gregale, store, pool, namespace, replaceWorker }) {
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const mint = scope => new SignJWT({ scope }).setProtectedHeader({ alg: 'ES256' })
    .setAudience(auth.resource).setIssuer(auth.issuer).setSubject('reviewed-input-owner')
    .setExpirationTime('5m').sign(privateKey);
  const owner = await mint('mcp:tools reports:read');
  const reduced = await mint('mcp:tools');
  let requests = 0, accepted = 0;
  const webStore = {
    ...store,
    async updateInputs(args) {
      const updated = await store.updateInputs(args);
      if (updated) accepted++;
      return updated;
    },
  };
  const web = createMcpTaskRuntime({ store: webStore, handlers: mcpTaskHandlers, workerEnabled: false, pollIntervalMs: 500 });
  const api = createApp({
    version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth,
  }, { taskRuntime: web, keyResolver: publicKey, log() {} });
  const server = createServer((req, res) => {
    if (req.headers['mcp-method'] === 'tasks/update') requests++;
    return api.app(req, res);
  });
  const dir = await mkdtemp(join(tmpdir(), 'gregale-reviewed-input-'));
  try {
    await web.start();
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
    const exec = promisify(execFile);
    const cli = async (verb, token, code, ...args) => {
      const result = await exec(gregale, ['mcp', 'client', verb, '--url', endpoint,
        '--token-env', 'GREGALE_QUALIFICATION_TOKEN', '--timeout', '10s', '--json', ...args], {
        cwd: dir, env: { XDG_CONFIG_HOME: dir, GREGALE_QUALIFICATION_TOKEN: token }, timeout: 12_000,
      }).then(value => ({ ...value, code: 0 }), error => error);
      assert.equal(result.code, code);
      assert.ok(!result.stdout.includes(token) && !result.stderr.includes(token));
      if (code === 0) return JSON.parse(result.stdout);
      assert.equal(result.stdout, '');
    };
    const selector = ['--task-receipt', join(dir, 'task.json')];
    const file = join(dir, 'review.json');
    const submitted = await cli('call', owner, 0, ...selector, '--defer-form-input', '--tasks',
      '--tool', 'build_report', '--arguments', '{"report":"qualification","steps":1,"request_approval":true}');
    const id = submitted.result.taskId;
    const rows = async () => (await pool.query(`SELECT task_id::text, status, attempt_count, updated_at,
      input_state_encrypted, result_encrypted IS NOT NULL AS has_result
      FROM gregale_mcp_tasks WHERE namespace=$1 AND submission_key IS NOT NULL`, [namespace])).rows;
    const pending = await eventually(async () => {
      const value = await rows();
      return value.length === 1 && value[0].task_id === id && value[0].status === 'input_required' ? value : null;
    }, 'reviewed_input_pending');
    assert.equal(pending[0].attempt_count, 1);
    assert.equal(pending[0].has_result, false);
    const snapshot = await cli('status', owner, 0, ...selector, '--write-input-template', file);
    assert.equal(snapshot.taskId, id);
    assert.equal(snapshot.nextAction, 'provide_input');
    assert.equal((await stat(file)).mode & 0o777, 0o600);
    const review = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(review.taskId, id);
    assert.deepEqual(Object.keys(review.forms), ['approval']);
    assert.equal(review.forms.approval.response, null);
    review.forms.approval.response = { action: 'accept', content: { approved: true } };
    await writeFile(file, JSON.stringify(review));

    const replacement = await replaceWorker();
    assert.deepEqual(await rows(), pending);
    await cli('wait', reduced, 1, ...selector, '--reviewed-input-file', file);
    const deniedUpdates = requests;
    assert.equal(deniedUpdates, 0);
    assert.equal(accepted, 0);
    assert.deepEqual(await rows(), pending);

    const completed = await cli('wait', owner, 0, ...selector, '--reviewed-input-file', file);
    assert.equal(completed.taskId, id);
    assert.equal(completed.status, 'completed');
    assert.equal(completed.result.content[0].text, 'Report qualification is ready after 1 steps.');
    assert.deepEqual(await replacement.next('reviewed_result'), {
      type: 'reviewed_result', taskId: id, workerID: 'reviewed-replacement',
    });
    const durable = await rows();
    assert.equal(durable.length, 1);
    assert.deepEqual([durable[0].task_id, durable[0].status, durable[0].attempt_count, durable[0].has_result],
      [id, 'completed', 1, true]);
    assert.equal(requests, 1);
    assert.equal(accepted, 1);
    return { denied_input_updates: deniedUpdates, accepted_input_updates: accepted, reviewed_task_results: durable.length };
  } finally {
    api.closeTaskSubscriptions();
    try {
      await new Promise(done => { server.close(done); server.closeAllConnections(); });
      await api.handler.close();
    } finally {
      try {
        await web.stop();
      } finally {
        await rm(dir, { recursive: true, force: true });
      }
    }
  }
}

async function runQualification(receipt, gregale) {
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
    const admissionHandlers = [{ name: 'build_report', version: '1' }, { name: 'qualification_input', version: '1' }];
    if (gregale) admissionHandlers.push({ name: 'build_report', version: mcpTaskHandlers.build_report.version });
    await store.migrate({ admissionHandlers });
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

    if (gregale) {
      stage = REVIEWED_CHECK;
      await Promise.all([replicaA, replicaB].map(replica => waitForExit(replica.child, 'SIGTERM')));
      const reviewedConfig = { databaseURL, namespace, ownerKey, schema, reviewedInput: true };
      const original = workerProcess('reviewed-original', reviewedConfig);
      workers.push(original);
      await original.next('ready');
      const observations = await qualifyReviewedInput({ gregale, store, pool, namespace, replaceWorker: async () => {
        if (await waitForExit(original.child, 'SIGKILL') !== 'SIGKILL') {
          throw new QualificationFailure('reviewed_input_worker_exit');
        }
        const replacement = workerProcess('reviewed-replacement', reviewedConfig);
        workers.push(replacement);
        await replacement.next('ready');
        return replacement;
      } });
      Object.assign(receipt.observations, observations);
      receipt.checks[REVIEWED_CHECK] = 'passed';
    }
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
    if (receipt.failures.length > 0 || Object.values(receipt.checks).some(status => status !== 'passed')) {
      receipt.status = 'failed';
      if (receipt.failures.length === 0) receipt.failures.push({ stage, reason: 'qualification_failed' });
    }
  }
}

async function main() {
  let target = 'unknown';
  let receipt = createReceipt(target);
  try {
    const options = optionsFromArgs(process.argv.slice(2));
    target = options.target;
    receipt = createReceipt(target, options.gregale);
    await runQualification(receipt, options.gregale);
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
