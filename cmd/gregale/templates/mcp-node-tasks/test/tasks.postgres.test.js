import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile, fork } from 'node:child_process';
import { createHash, createHmac, createCipheriv, randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdir, mkdtemp, readdir, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { generateKeyPair, SignJWT } from 'jose';
import { Pool } from 'pg';
import * as z from 'zod/v4';
import { createApp } from '../app.js';
import { createPostgresMcpTaskStore, createMcpTaskQueueObserver } from '../task-store.js';
import { createMcpTaskPayloadCipher } from '../task-crypto.js';
import { createMcpTaskRuntime, mcpTaskHandlers, RetryableMcpTaskError } from '../tasks.js';

const databaseURL = process.env.MCP_TASKS_TEST_DATABASE_URL;
const postgresOnly = { skip: databaseURL ? false : 'set MCP_TASKS_TEST_DATABASE_URL to run PostgreSQL integration tests' };

for (const [title, mode] of [
  ['a fresh Gregale CLI resumes the same PostgreSQL Task after a dropped connection', 'handle'],
  ['receipt watch reconciles a stream disconnect and resumes from a fresh CLI', 'watch'],
  ['batch receipt status preserves mixed outcomes in input order', 'batch'],
  ['batch receipt watch follows both Tasks on one stream and rechecks after disconnect', 'watch-batch'],
]) test(title, {
  skip: postgresOnly.skip || (!process.env.MCP_TASKS_TEST_GREGALE && 'set MCP_TASKS_TEST_GREGALE to the Gregale CLI binary'),
  timeout: 30_000,
}, async t => {
  const { store, pool, namespace } = await harness(t);
  const clientDir = await mkdtemp(join(tmpdir(), 'gregale-task-cli-'));
  t.after(() => rm(clientDir, { recursive: true, force: true }));
  const finishReport = Promise.withResolvers();
  const requestApproval = Promise.withResolvers();
  const resultText = 'Private reconnect report is ready.';
  const batchReceipt = mode === 'batch' || mode === 'watch-batch';
  const buildReport = batchReceipt ? mcpTaskHandlers.build_report : { version: '1' };
  const runtime = createMcpTaskRuntime({ store, pollIntervalMs: 500, workerConcurrency: batchReceipt ? 2 : 1, handlers: {
    build_report: { ...buildReport, async execute(args, context) {
      if (!batchReceipt || args.report === 'reconnect') await finishReport.promise;
      if (mode === 'watch-batch' && args.report === 'paused') await requestApproval.promise;
      if (batchReceipt) return buildReport.execute(args, context);
      return { content: [{ type: 'text', text: resultText }] };
    } },
  } });
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const token = (subject, scope = 'mcp:tools reports:read') => new SignJWT({ scope }).setProtectedHeader({ alg: 'ES256' })
    .setAudience(auth.resource).setIssuer(auth.issuer).setSubject(subject).setExpirationTime('5m').sign(privateKey);
  const owner = await token('alice');
  const otherCaller = await token('bob');
  const reduced = await token('alice', 'mcp:tools');
  const subscriptionTaskIDs = [];
  const subscribe = runtime.subscribe.bind(runtime);
  runtime.subscribe = (taskIDs, ...args) => {
    subscriptionTaskIDs.push(taskIDs);
    return subscribe(taskIDs, ...args);
  };
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth,
  }, { taskRuntime: runtime, keyResolver: publicKey, log() {} });

  const requests = [];
  const subscriptions = [];
  let dropNextStatus = false;
  let throttleNextLookup = false;
  const server = createServer((req, res) => {
    const method = req.headers['mcp-method'];
    requests.push(method);
    if (method === 'subscriptions/listen') subscriptions.push(res);
    if (method === 'tasks/get' && dropNextStatus) {
      dropNextStatus = false;
      req.destroy();
      return;
    }
    if (method === 'gregale/tasks/lookup' && throttleNextLookup) {
      throttleNextLookup = false;
      res.writeHead(429, { 'Retry-After': '7' });
      res.end('private endpoint error text');
      return;
    }
    api.app(req, res);
  });
  try {
    await runtime.start();
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
    const run = promisify(execFile);
    const binary = resolve(process.env.MCP_TASKS_TEST_GREGALE);
    async function cli(verb, credential, args, expectedExit = 0, onStart) {
      const pending = run(binary, [
        'mcp', 'client', verb, '--url', endpoint, '--token-env', 'GREGALE_JOURNEY_TOKEN',
        ...(verb === 'watch' ? [] : ['--timeout', '5s', '--json']), ...args,
      ], { cwd: clientDir, env: { XDG_CONFIG_HOME: clientDir, GREGALE_JOURNEY_TOKEN: credential }, timeout: 8000 });
      onStart?.(pending.child);
      const response = await pending.then(
        ({ stdout, stderr }) => ({ stdout, stderr, code: 0 }),
        ({ stdout, stderr, code }) => ({ stdout, stderr, code }),
      );
      assert.equal(response.code, expectedExit, `unexpected exit from mcp client ${verb}`);
      assert.ok(!response.stdout.includes(credential) && !response.stderr.includes(credential), 'CLI leaked a caller token');
      assert.ok(!response.stderr.includes(resultText), 'CLI leaked a Task result to stderr');
      return response.stdout;
    }
    const receipt = ['--task-receipt', join(clientDir, 'task.json')];
    const submitted = JSON.parse(await cli('call', owner, ['--tool', 'build_report', '--tasks',
      ...(mode === 'handle' ? [] : receipt), '--arguments', '{"report":"reconnect","steps":1}']));
    assert.equal(submitted.result.resultType, 'task');
    assert.equal(submitted.result.status, 'working');
    const taskId = submitted.result.taskId;
    assert.ok(taskId);
    const rows = async () => (await pool.query(`SELECT task_id::text, status, attempt_count,
      result_encrypted IS NOT NULL AS has_result FROM gregale_mcp_tasks WHERE namespace=$1`, [namespace])).rows;
    await eventually(async () => (await rows())[0]?.status === 'running', 'worker did not claim the Task');

    if (batchReceipt) {
      async function submitReceipt(report, credential, needsInput = false) {
        const path = join(clientDir, `${report}.json`);
        const argumentsJSON = JSON.stringify({ report, steps: 1, ...(needsInput ? { request_approval: true } : {}) });
        const { result } = JSON.parse(await cli('call', credential, ['--tool', 'build_report', '--tasks',
          '--task-receipt', path, '--arguments', argumentsJSON, ...(needsInput ? ['--defer-form-input'] : [])]));
        return { path, id: result.taskId };
      }
      const receiptFlags = paths => paths.flatMap(path => ['--task-receipt', path]);
      if (mode === 'watch-batch') {
        const foreign = await submitReceipt('foreign', otherCaller);
        const paused = await submitReceipt('paused', owner, true);
        await eventually(async () => (await rows()).some(row => row.task_id === paused.id && row.status === 'running'),
          'worker did not start the approval Task');
        const paths = [paused.path, foreign.path, receipt[1], receipt[1]];
        const beforeDenied = requests.length;
        const denied = await cli('watch', reduced, receiptFlags(paths), 1);
        assert.deepEqual(denied.trim().split('\n').map(line => JSON.parse(line)),
          paths.map((_, receiptIndex) => ({ receiptIndex, outcome: 'unavailable' })));
        assert.deepEqual(requests.slice(beforeDenied), ['server/discover', ...Array(4).fill('gregale/tasks/lookup')]);
        assert.deepEqual(subscriptionTaskIDs, []);

        let output = '';
        const beforeWatch = requests.length;
        const pending = cli('watch', owner, receiptFlags(paths), 1, child => {
          child.stdout.setEncoding('utf8');
          child.stdout.on('data', chunk => { output += chunk; });
        });
        void pending.catch(() => {});
        await eventually(() => subscriptions[0]?.headersSent, 'batch watch did not open a shared subscription');
        assert.deepEqual(subscriptionTaskIDs, [[paused.id, taskId]]);
        requestApproval.resolve();
        await eventually(() => output.includes('"nextAction":"provide_input"'), 'batch watch missed the live approval transition');
        assert.equal(subscriptions.length, 1);

        api.closeTaskSubscriptions();
        await eventually(() => subscriptions[1]?.headersSent, 'batch watch did not reconnect for the remaining Task');
        assert.deepEqual(subscriptionTaskIDs, [[paused.id, taskId], [taskId]]);
        finishReport.resolve();
        const observed = (await pending).trim().split('\n').map(line => JSON.parse(line));
        assert.equal(observed.length, 7);
        const event = (index, id, status, nextAction, extra = {}) => {
          const lastUpdatedAt = observed[index].task.lastUpdatedAt;
          assert.ok(Number.isFinite(Date.parse(lastUpdatedAt)));
          return { outcome: 'available', task: { taskId: id, status, lastUpdatedAt, nextAction, ...extra } };
        };
        const working = { pollIntervalMs: 500 };
        assert.deepEqual(observed, [
          { receiptIndex: 0, ...event(0, paused.id, 'working', 'poll_status', working) },
          { receiptIndex: 1, outcome: 'unavailable' },
          { receiptIndex: 2, ...event(2, taskId, 'working', 'poll_status', working) },
          { receiptIndex: 3, ...event(3, taskId, 'working', 'poll_status', working) },
          { receiptIndex: 0, ...event(4, paused.id, 'input_required', 'provide_input') },
          { receiptIndex: 2, ...event(5, taskId, 'completed', 'read_result') },
          { receiptIndex: 3, ...event(6, taskId, 'completed', 'read_result') },
        ]);
        assert.deepEqual(requests.slice(beforeWatch), [
          'server/discover', ...Array(4).fill('gregale/tasks/lookup'), 'subscriptions/listen',
          'gregale/tasks/lookup', ...Array(2).fill('gregale/tasks/lookup'), 'subscriptions/listen',
          ...Array(2).fill('gregale/tasks/lookup'),
        ]);
        const persisted = new Map((await rows()).map(row => [row.task_id, row]));
        assert.deepEqual(persisted.get(paused.id), { task_id: paused.id, status: 'input_required', attempt_count: 1, has_result: false });
        assert.deepEqual(persisted.get(taskId), { task_id: taskId, status: 'completed', attempt_count: 1, has_result: true });
        return;
      }
      const paused = await submitReceipt('paused', owner, true);
      const completed = await submitReceipt('done', owner);
      const foreign = await submitReceipt('foreign', otherCaller);
      const expired = await submitReceipt('expired', owner);
      await eventually(async () => {
        const statuses = new Map((await rows()).map(row => [row.task_id, row.status]));
        return statuses.get(paused.id) === 'input_required' &&
          [completed.id, foreign.id, expired.id].every(id => statuses.get(id) === 'completed');
      }, 'PostgreSQL did not retain all expected Task states');
      await pool.query('UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2',
        [namespace, expired.id]);

      const statusFlags = paths => ['--metadata-only', ...receiptFlags(paths)];
      const goodPaths = [paused.path, completed.path, receipt[1]];
      const beforeInvalid = requests.length;
      assert.equal(await cli('status', owner, [...receiptFlags(goodPaths), '--read-result'], 1), '');
      assert.equal(await cli('status', owner, [...statusFlags(goodPaths), '--task-id', taskId], 1), '');
      assert.equal(await cli('cancel', owner, receiptFlags(goodPaths), 1), '');
      assert.equal(requests.length, beforeInvalid);

      const beforeMixed = requests.length;
      const mixed = JSON.parse(await cli('status', owner, statusFlags([
        paused.path, foreign.path, expired.path, join(clientDir, 'missing.json'), completed.path, receipt[1],
      ]), 1));
      assert.deepEqual(requests.slice(beforeMixed), ['server/discover', ...Array(5).fill('gregale/tasks/lookup')]);
      const available = (outcome, id, status, nextAction, extra = {}) => {
        assert.ok(Number.isFinite(Date.parse(outcome.task.lastUpdatedAt)));
        return { outcome: 'available', task: { taskId: id, status, lastUpdatedAt: outcome.task.lastUpdatedAt, nextAction, ...extra } };
      };
      assert.deepEqual(mixed, [
        available(mixed[0], paused.id, 'input_required', 'provide_input'),
        { outcome: 'unavailable' },
        { outcome: 'unavailable' },
        { outcome: 'invalid_receipt' },
        available(mixed[4], completed.id, 'completed', 'read_result'),
        available(mixed[5], taskId, 'working', 'poll_status', { pollIntervalMs: 500 }),
      ]);
      const goodOutcomes = [mixed[0], mixed[4], mixed[5]];
      assert.deepEqual(JSON.parse(await cli('status', owner, statusFlags(goodPaths))), goodOutcomes);
      assert.deepEqual(JSON.parse(await cli('status', owner, statusFlags([completed.path]))), mixed[4].task);
      assert.deepEqual(JSON.parse(await cli('status', reduced, statusFlags(goodPaths), 1)), Array(3).fill({ outcome: 'unavailable' }));

      const beforeThrottle = requests.length;
      throttleNextLookup = true;
      assert.deepEqual(JSON.parse(await cli('status', owner, statusFlags([paused.path, completed.path]), 3)), [
        { outcome: 'retry', task: { status: 'unknown', nextAction: 'retry_status', pollIntervalMs: 7000 } },
        mixed[4],
      ]);
      assert.deepEqual(requests.slice(beforeThrottle), ['server/discover', 'gregale/tasks/lookup', 'gregale/tasks/lookup']);
      return;
    }

    const handle = ['--task-id', taskId];
    async function metadata(credential, expectedExit = 0) {
      const from = requests.length;
      const stdout = await cli('status', credential, [...handle, '--metadata-only'], expectedExit);
      assert.deepEqual(requests.slice(from), ['server/discover', 'tasks/get']);
      return stdout;
    }
    dropNextStatus = true;
    assert.deepEqual(JSON.parse(await metadata(owner, 3)), { taskId, status: 'unknown', nextAction: 'retry_status' });
    const working = JSON.parse(await metadata(owner));
    assert.ok(Number.isFinite(Date.parse(working.lastUpdatedAt)));
    assert.deepEqual(working, { taskId, status: 'working', lastUpdatedAt: working.lastUpdatedAt,
      pollIntervalMs: 500, nextAction: 'poll_status' });

    if (mode === 'watch') {
      const beforeInvalid = requests.length;
      assert.equal(await cli('watch', owner, [...receipt, '--uri', 'task://tasks/' + taskId], 1), '');
      assert.equal(await cli('watch', owner, [...receipt, '--url', `${endpoint}/other`], 1), '');
      assert.equal(requests.length, beforeInvalid);
      for (const credential of [otherCaller, reduced]) {
        const beforeDenied = requests.length;
        assert.equal(await cli('watch', credential, receipt, 1), '');
        assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
      }
      let watcher;
      const beforeWatch = requests.length;
      const interrupted = cli('watch', owner, receipt, 0, child => { watcher = child; });
      void interrupted.catch(() => {});
      await eventually(() => subscriptions[0]?.headersSent, 'first watch did not open its Task subscription');
      api.closeTaskSubscriptions();
      await eventually(() => subscriptions[1]?.headersSent, 'watch did not reconnect to its Task subscription');
      assert.ok(watcher.kill('SIGINT'));
      assert.deepEqual(JSON.parse((await interrupted).trim()), working);
      assert.deepEqual(requests.slice(beforeWatch), [
        'server/discover', 'gregale/tasks/lookup', 'subscriptions/listen', 'gregale/tasks/lookup', 'subscriptions/listen',
      ]);

      const beforeResume = requests.length;
      const resumed = cli('watch', owner, receipt);
      void resumed.catch(() => {});
      await eventually(() => subscriptions[2]?.headersSent, 'fresh CLI did not open a Task subscription');
      finishReport.resolve();
      const [first, last, ...extra] = (await resumed).trim().split('\n').map(line => JSON.parse(line));
      assert.deepEqual(first, working);
      assert.ok(Number.isFinite(Date.parse(last.lastUpdatedAt)));
      assert.deepEqual(last, { taskId, status: 'completed', lastUpdatedAt: last.lastUpdatedAt, nextAction: 'read_result' });
      assert.deepEqual(extra, []);
      assert.deepEqual(requests.slice(beforeResume), ['server/discover', 'gregale/tasks/lookup', 'subscriptions/listen', 'gregale/tasks/lookup']);
      const beforeTerminal = requests.length;
      assert.deepEqual(JSON.parse(await cli('watch', owner, receipt)), last);
      assert.deepEqual(requests.slice(beforeTerminal), ['server/discover', 'gregale/tasks/lookup']);
      assert.equal(JSON.parse(await cli('status', owner, [...receipt, '--read-result'])).result.content[0].text, resultText);
      assert.deepEqual(await rows(), [{ task_id: taskId, status: 'completed', attempt_count: 1, has_result: true }]);
      return;
    }

    finishReport.resolve();
    await eventually(async () => (await rows())[0]?.status === 'completed', 'worker did not complete the Task');
    assert.equal(await metadata(otherCaller, 1), '');
    const completed = JSON.parse(await metadata(owner));
    assert.ok(Number.isFinite(Date.parse(completed.lastUpdatedAt)));
    assert.deepEqual(completed, { taskId, status: 'completed', lastUpdatedAt: completed.lastUpdatedAt, nextAction: 'read_result' });
    const full = JSON.parse(await cli('status', owner, handle));
    assert.equal(full.taskId, taskId);
    assert.equal(full.result.content[0].text, resultText);
    assert.equal(requests.filter(method => method === 'tools/call').length, 1);
    assert.deepEqual(await rows(), [{ task_id: taskId, status: 'completed', attempt_count: 1, has_result: true }]);
  } finally {
    finishReport.resolve();
    requestApproval.resolve();
    api.closeTaskSubscriptions();
    await new Promise(resolve => {
      server.close(resolve);
      server.closeAllConnections();
    });
    await api.handler.close();
    await runtime.stop();
  }
});

test('a fresh CLI finds only the caller’s retained Task inbox', {
  skip: postgresOnly.skip || (!process.env.MCP_TASKS_TEST_GREGALE && 'set MCP_TASKS_TEST_GREGALE to the Gregale CLI binary'),
  timeout: 30_000,
}, async t => {
  const { store, pool, namespace } = await harness(t, { ttlMs: 120_000 });
  await pool.query('DROP INDEX gregale_mcp_tasks_inbox_idx');
  await store.initialize();
  assert.equal(store.supportsTaskInbox, false);
  assert.equal(store.supportsTaskSubmission, true);
  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  const runtime = createMcpTaskRuntime({ store, handlers: mcpTaskHandlers, pollIntervalMs: 500 });
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const mint = (subject, scope = 'mcp:tools reports:read', client_id = 'original-client') =>
    new SignJWT({ scope, client_id }).setJti(randomUUID()).setProtectedHeader({ alg: 'ES256' })
      .setAudience(auth.resource).setIssuer(auth.issuer).setSubject(subject).setExpirationTime('5m').sign(privateKey);
  const [owner, refreshed, foreign, reduced, otherClient] = await Promise.all([
    mint('alice'), mint('alice'), mint('bob'), mint('alice', 'mcp:tools'), mint('alice', undefined, 'other-client'),
  ]);
  const clientDir = await mkdtemp(join(tmpdir(), 'gregale-task-inbox-'));
  t.after(() => rm(clientDir, { recursive: true, force: true }));
  const freshDir = join(clientDir, 'fresh');
  await mkdir(freshDir);
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth,
  }, { taskRuntime: runtime, keyResolver: publicKey, log() {} });
  const methods = [];
  let fault, racingTask;
  const server = createServer((req, res) => {
    const method = req.headers['mcp-method'];
    methods.push(method);
    if (method === 'gregale/tasks/respond-reviewed' && fault) {
      const action = fault;
      fault = undefined;
      if (action === 'before') {
        req.socket.destroy();
        return;
      }
      if (action === 'after') {
        res.write = () => true;
        res.end = () => { req.socket.destroy(); return res; };
      }
      if (action === 'race') {
        // Consume the form after the CLI validates the draft and before the
        // guarded writer takes its PostgreSQL lock.
        const authInfo = { resource: new URL(auth.resource), clientId: 'original-client', extra: { subject: 'alice' } };
        void store.updateInputs({ taskID: racingTask, authInfo, authMode: 'external-oauth',
          inputResponses: { approval: { action: 'decline' } } })
          .then(ok => { assert.equal(ok, true); api.app(req, res); })
          .catch(error => res.destroy(error));
        return;
      }
    }
    api.app(req, res);
  });
  try {
    await runtime.start();
    assert.equal(store.supportsTaskInbox, true);
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
    const run = promisify(execFile);
    const binary = resolve(process.env.MCP_TASKS_TEST_GREGALE);
    async function cli(verb, credential, args, { success = true, expectedExit = success ? 0 : 1, cwd = freshDir } = {}) {
      const response = await run(binary, [
        'mcp', 'client', verb, '--url', endpoint, '--token-env', 'GREGALE_JOURNEY_TOKEN',
        '--timeout', '5s', ...args,
      ], { cwd, env: { XDG_CONFIG_HOME: cwd, GREGALE_JOURNEY_TOKEN: credential }, timeout: 8000 })
        .then(({ stdout, stderr }) => ({ stdout, stderr, code: 0 }), error => error);
      assert.equal(response.code, expectedExit, response.stderr);
      assert.ok(!(response.stdout + response.stderr).includes(credential));
      if (expectedExit === 0 || expectedExit === 3) return JSON.parse(response.stdout);
      assert.equal(response.stdout, '');
    }
    async function submit(name, credential, paused = true) {
      const { result } = await cli('call', credential, ['--tool', 'build_report', '--tasks', '--defer-form-input',
        '--task-receipt', join(clientDir, `${name}.json`),
        '--arguments', JSON.stringify({ report: `private-inbox-${name}`, steps: 1, request_approval: paused }),
      ], { cwd: clientDir });
      return result.taskId;
    }
    const older = await submit('older', owner);
    const newer = await submit('newer', owner);
    const expired = await submit('expired', owner);
    const other = await submit('foreign', foreign);
    const completed = await submit('completed', owner, false);
    await eventually(async () => {
      const rows = await pool.query('SELECT task_id::text, status FROM gregale_mcp_tasks WHERE namespace=$1', [namespace]);
      const statuses = new Map(rows.rows.map(row => [row.task_id, row.status]));
      return [older, newer, expired, other].every(id => statuses.get(id) === 'input_required') && statuses.get(completed) === 'completed';
    }, 'PostgreSQL did not retain all expected Task states');
    // Both page anchors round to the same JavaScript millisecond.
    await pool.query(`UPDATE gregale_mcp_tasks SET expires_at = CASE task_id
        WHEN $2::uuid THEN date_trunc('second', statement_timestamp()) + interval '60 seconds 1 microsecond'
        WHEN $3::uuid THEN date_trunc('second', statement_timestamp()) + interval '60 seconds 2 microseconds'
        ELSE clock_timestamp() END
      WHERE namespace=$1 AND task_id IN ($2, $3, $4)`, [namespace, older, newer, expired]);

    const filter = ['--status', 'input_required', '--limit', '1'];
    const page = await cli('inbox', refreshed, filter);
    assert.equal(page.tasks.length, 1);
    const [found] = page.tasks;
    assert.ok(Number.isFinite(Date.parse(found.createdAt)));
    assert.ok(Number.isFinite(Date.parse(found.lastUpdatedAt)));
    assert.deepEqual(found, { taskId: newer, status: 'input_required', nextAction: 'provide_input',
      createdAt: found.createdAt, lastUpdatedAt: found.lastUpdatedAt });
    assert.ok(page.nextCursor);
    const cursor = ['--cursor', page.nextCursor];
    const badCursor = (page.nextCursor.startsWith('A') ? 'B' : 'A') + page.nextCursor.slice(1);
    await cli('inbox', foreign, [...filter, ...cursor], { success: false });
    await cli('inbox', refreshed, ['--status', 'working', ...cursor], { success: false });
    await cli('inbox', refreshed, [...filter, '--cursor', badCursor], { success: false });
    assert.deepEqual(await cli('inbox', reduced, [...filter, ...cursor]), { tasks: [] });
    assert.deepEqual(await cli('inbox', otherClient, []), { tasks: [] });
    assert.deepEqual((await cli('inbox', foreign, [])).tasks.map(task => task.taskId), [other]);
    assert.deepEqual((await cli('inbox', refreshed, ['--status', 'completed'])).tasks.map(task => task.taskId), [completed]);
    const status = await cli('status', refreshed, ['--task-id', found.taskId, '--metadata-only', '--json']);
    assert.equal(status.nextAction, 'provide_input');

    // A continuation survives expiry of the previous page’s last row.
    await pool.query('UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2', [namespace, newer]);
    const next = await cli('inbox', refreshed, [...filter, ...cursor]);
    assert.deepEqual(next.tasks.map(task => task.taskId), [older]);
    assert.equal(next.nextCursor, undefined);
    assert.deepEqual(await readdir(freshDir), []);

    const writes = () => methods.filter(method => method === 'gregale/tasks/respond-reviewed').length;
    const checks = () => methods.filter(method => method === 'gregale/tasks/check-reviewed-response').length;
    const wait = taskId => cli('wait', refreshed, ['--task-id', taskId]);
    async function draft(taskId, name) {
      const file = join(freshDir, `${name}.json`);
      await cli('status', refreshed, ['--task-id', taskId, '--write-input-template', file]);
      assert.equal((await stat(file)).mode & 0o777, 0o600);
      const template = JSON.parse(await readFile(file, 'utf8'));
      assert.equal(template.taskId, taskId);
      assert.equal(template.version, 2);
      assert.match(template.reviewRevision, /^[0-9a-f]{64}$/);
      template.forms.approval.response = { action: 'accept', content: { approved: true } };
      await writeFile(file, JSON.stringify(template));
      return ['--task-id', taskId, '--reviewed-input-file', file];
    }

    const reviewed = await draft(next.tasks[0].taskId, 'reviewed');
    await cli('respond', foreign, reviewed, { success: false });
    await cli('respond', reduced, reviewed, { success: false });
    await cli('respond', otherClient, reviewed, { success: false });
    assert.equal(writes(), 0);

    fault = 'before';
    const unconfirmed = await cli('respond', refreshed, reviewed, { expectedExit: 3 });
    assert.equal(unconfirmed.outcome, 'unconfirmed');
    assert.equal(unconfirmed.status, 'input_required');
    assert.equal(unconfirmed.nextAction, 'retry_status');
    assert.equal(writes(), 1);
    assert.equal(checks(), 1);

    fault = 'after';
    const applied = await cli('respond', refreshed, reviewed);
    assert.equal(applied.outcome, 'applied');
    assert.equal(applied.taskId, older);
    assert.equal(writes(), 2);
    assert.equal(checks(), 2);
    assert.match((await wait(older)).result.content[0].text, /Report private-inbox-older is ready/);

    assert.equal((await cli('respond', refreshed, reviewed)).outcome, 'applied');
    assert.equal(writes(), 2);
    assert.equal(checks(), 3);

    racingTask = await submit('racing', owner);
    await eventually(async () =>
      (await pool.query('SELECT status FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2', [namespace, racingTask])).rows[0]?.status === 'input_required',
      'PostgreSQL did not retain the Task for the concurrent response');
    const stale = await draft(racingTask, 'racing');
    fault = 'race';
    await cli('respond', refreshed, stale, { success: false });
    assert.equal(writes(), 3);
    assert.equal(checks(), 3);
    assert.equal((await wait(racingTask)).result.content[0].text, 'Report was not approved.');
    assert.equal(methods.filter(method => method === 'tasks/update').length, 0);
  } finally {
    api.closeTaskSubscriptions();
    await new Promise(resolve => {
      server.close(resolve);
      server.closeAllConnections();
    });
    await api.handler.close();
    await runtime.stop();
  }
});

test('Task inbox watch discovers paused Tasks and reconciles a failed page', {
  skip: postgresOnly.skip || (!process.env.MCP_TASKS_TEST_GREGALE && 'set MCP_TASKS_TEST_GREGALE to the Gregale CLI binary'),
  timeout: 45_000,
}, async t => {
  const { store, pool, namespace } = await harness(t, { ttlMs: 120_000 });
  const runtime = createMcpTaskRuntime({ store, handlers: mcpTaskHandlers, workerEnabled: false });
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const clientId = 'inbox-watch-fixture';
  const ownerToken = await new SignJWT({ scope: 'mcp:tools reports:read', client_id: clientId })
    .setProtectedHeader({ alg: 'ES256' }).setAudience(auth.resource).setIssuer(auth.issuer)
    .setSubject('alice').setExpirationTime('5m').sign(privateKey);
  const owner = subject => ({ clientId, resource: new URL(auth.resource), extra: { subject } });
  const config = { version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth };
  const options = { taskRuntime: runtime, keyResolver: publicKey, log() {} };
  const api = createApp(config, options);
  const restricted = createApp({ ...config, auth: { ...auth, tool_scopes: { build_report: ['reports:review'] } } }, options);

  const seeded = Promise.withResolvers();
  const retryScan = Promise.withResolvers();
  let inboxResponse, dropped = false, scans = 0, completeInitial = false, revoked = false, revokedReads = 0;
  const list = runtime.inbox.bind(runtime);
  runtime.inbox = async (query, ...args) => {
    if (!query.cursor && ++scans > 1) {
      await seeded.promise;
      if (dropped) await retryScan.promise;
    }
    if (query.cursor && !dropped) {
      dropped = true;
      inboxResponse.destroy();
    }
    return list(query, ...args);
  };
  const server = createServer((req, res) => {
    if (req.headers['mcp-method'] === 'gregale/tasks/inbox') {
      inboxResponse = res;
      const wasRevoked = revoked;
      res.on('finish', () => {
        completeInitial = true;
        if (wasRevoked) revokedReads++;
      });
    }
    (revoked ? restricted : api).app(req, res);
  });
  const profile = await mkdtemp(join(tmpdir(), 'gregale-inbox-watch-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  let watch, closed;
  const form = { method: 'elicitation/create', params: { mode: 'form', message: 'Private watch prompt',
    requestedSchema: { type: 'object', properties: {} } } };
  async function pause(subject, requests = { first: form }) {
    const task = await store.create({ toolName: 'build_report', handlerVersion: '1',
      args: { report: 'watch fixture', steps: 1 }, authInfo: owner(subject), authMode: 'external-oauth',
      inputMethods: ['elicitation/create:form'] });
    const claimed = await store.claim(3, 30_000);
    assert.equal(claimed.task_id, task.task_id);
    assert.equal((await store.requestInputs({ taskID: task.task_id, leaseToken: claimed.lease_token, requests })).ready, false);
    return task.task_id;
  }

  try {
    await runtime.start();
    const foreign = await pause('bob');
    const expired = await pause('alice');
    await pool.query('UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2', [namespace, expired]);
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
    watch = execFile(resolve(process.env.MCP_TASKS_TEST_GREGALE), [
      'mcp', 'client', 'inbox', '--url', endpoint, '--token-env', 'GREGALE_JOURNEY_TOKEN',
      '--status', 'input_required', '--watch', '--interval', '1s', '--timeout', '5s', '--limit', '1',
    ], { cwd: profile, env: { XDG_CONFIG_HOME: profile, GREGALE_JOURNEY_TOKEN: ownerToken }, timeout: 30_000 });
    closed = new Promise(resolve => watch.once('close', (code, signal) => resolve({ code, signal })));
    let stdout = '', stderr = '';
    watch.stdout.setEncoding('utf8').on('data', chunk => { stdout += chunk; });
    watch.stderr.setEncoding('utf8').on('data', chunk => { stderr += chunk; });
    const events = () => stdout.split('\n').slice(0, -1).map(line => JSON.parse(line));

    await eventually(() => completeInitial, 'watch did not read an empty inbox', 400);
    assert.equal(stdout, '');
    const older = await pause('alice', { first: form, second: form });
    const newer = await pause('alice');
    seeded.resolve();
    await eventually(() => dropped && stderr.includes('Retrying'), 'watch did not retry the disconnected page', 400);
    assert.equal(stdout, '', 'watch published a partial scan');
    retryScan.resolve();
    await eventually(() => events().length >= 2, 'watch did not publish the recovered inbox scan', 400);
    assert.deepEqual(events().map(event => event.taskId), [newer, older]);
    for (const event of events()) {
      assert.equal(event.status, 'input_required');
      assert.equal(event.nextAction, 'provide_input');
      assert.deepEqual(Object.keys(event).sort(), ['createdAt', 'lastUpdatedAt', 'nextAction', 'status', 'taskId']);
    }

    const before = events()[1].lastUpdatedAt;
    assert.equal(await store.updateInputs({ taskID: older, authInfo: owner('alice'), authMode: 'external-oauth',
      inputResponses: { first: { action: 'decline' } } }), true);
    await eventually(() => events().length >= 3, 'watch did not report the changed pending Task', 400);
    assert.equal(events()[2].taskId, older);
    assert.notEqual(events()[2].lastUpdatedAt, before);

    revoked = true;
    const hidden = await pause('alice');
    await eventually(() => revokedReads >= 2, 'watch stopped polling after tool access changed', 400);
    assert.deepEqual(events().map(event => event.taskId), [newer, older, older]);
    assert.ok(![foreign, expired, hidden, ownerToken, form.params.message].some(value => (stdout + stderr).includes(value)));
    assert.deepEqual(await readdir(profile), []);

    watch.kill('SIGINT');
    assert.deepEqual(await closed, { code: 0, signal: null });
  } finally {
    seeded.resolve();
    retryScan.resolve();
    if (watch && watch.exitCode === null && watch.signalCode === null) watch.kill('SIGKILL');
    if (closed) await closed;
    await new Promise(resolve => {
      server.close(resolve);
      server.closeAllConnections();
    });
    await Promise.all([api.handler.close(), restricted.handler.close()]);
    await runtime.stop();
  }
});

const taskOutputContract = { type: 'object', required: ['amount'], additionalProperties: false,
  $defs: { amount: { type: 'integer', minimum: 1 } }, properties: { amount: { $ref: '#/$defs/amount' } } };

test('output-schema task handles survive HTTP validation and retained workers enforce the saved catalog', postgresOnly, async t => {
  const { store, pool } = await harness(t);
  const producer = createMcpTaskRuntime({ store, workerEnabled: false, handlers: { build_report: {
    version: '1', async validate(args) { await Promise.resolve(); if (args.report === 'reject') throw new Error('invalid arguments'); },
    async execute() { throw new Error('producer must not execute'); },
  } } });
  await producer.start();
  t.after(() => producer.stop());
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: true, allowed_origins: [], tasks: { enabled: true }, auth: { mode: 'open' } }, { taskRuntime: producer, log() {} });
  const definition = { inputSchema: z.object({ report: z.string() }), outputSchema: z.object({ amount: z.number().int().min(1) }) };
  api.replaceTool('build_report', definition, async () => ({ content: [], structuredContent: { amount: 2 } }));
  const server = api.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => { api.closeTaskSubscriptions(); await api.handler.close(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  async function call(report) {
    const response = await fetch(`http://127.0.0.1:${server.address().port}/mcp`, { method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
        'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'tools/call', 'Mcp-Name': 'build_report' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'build_report', arguments: { report }, _meta: {
        'io.modelcontextprotocol/protocolVersion': '2026-07-28',
        'io.modelcontextprotocol/clientInfo': { name: 'output-postgres-test', version: '1' },
        'io.modelcontextprotocol/clientCapabilities': { extensions: { 'io.modelcontextprotocol/tasks': {} } },
      } } }),
    });
    const text = await response.text();
    assert.equal(response.status, 200, text);
    const message = JSON.parse(text.startsWith('{') ? text : text.split('\n').find(line => line.startsWith('data: ')).slice(6));
    assert.equal(message.error, undefined);
    return message.result;
  }
  assert.equal((await call('reject')).isError, true);
  assert.equal((await pool.query('SELECT count(*)::int AS count FROM gregale_mcp_tasks')).rows[0].count, 0);
  const handles = [];
  for (const report of ['good', 'bad', 'business-error']) {
    const handle = await call(report);
    assert.equal(handle.resultType, 'task');
    assert.equal(handle.isError, undefined);
    assert.ok(handle.taskId);
    handles.push([report, handle]);
  }
  const rows = (await pool.query('SELECT output_schema FROM gregale_mcp_tasks')).rows;
  assert.equal(rows.length, 3, 'each delivered handle has exactly one persisted task');
  for (const row of rows) assert.equal(row.output_schema.properties.amount.type, 'integer');
  api.replaceTool('build_report', { ...definition, outputSchema: z.object({ label: z.string() }) }, async () => ({ content: [] }));
  await producer.stop();
  const execute = async ({ report }) => report === 'business-error' ? { isError: true, content: [] }
    : { content: [], structuredContent: { amount: report === 'bad' ? 'secret-output' : 2 } };
  // The replacement worker has only executors, with the original version retained.
  const worker = createMcpTaskRuntime({ store, pollIntervalMs: 500, handlers: { build_report: {
    version: '2', execute, previousVersions: { '1': execute },
  } } });
  await worker.start();
  try {
    for (const [report, handle] of handles) {
      const task = await eventually(async () => {
        const result = await worker.get(handle.taskId, undefined, 'open');
        return ['completed', 'failed'].includes(result.status) ? result : null;
      }, 'worker did not settle task');
      assert.equal(task.status, report === 'bad' ? 'failed' : 'completed');
      if (report === 'bad') {
        assert.deepEqual(task.error, { code: -32603, message: 'Task execution failed' });
        assert.equal(JSON.stringify(task).includes('secret-output'), false);
        assert.equal((await pool.query('SELECT result_encrypted FROM gregale_mcp_tasks WHERE task_id=$1', [handle.taskId])).rows[0].result_encrypted, null);
      }
    }
  } finally { await worker.stop(); }
});

test('output contracts survive retry and input pauses across a replacement worker', postgresOnly, async t => {
  for (const pause of [false, true]) await t.test(pause ? 'input' : 'retry', async t => {
    const { store, pool } = await harness(t);
    const contract = structuredClone(taskOutputContract);
    const request = { method: 'elicitation/create', params: { message: 'Approve?', requestedSchema: { type: 'object', properties: {} } } };
    const initial = createMcpTaskRuntime({ store, pollIntervalMs: 500, retryBaseDelayMs: 1000, retryMaxDelayMs: 1000, handlers: {
      build_report: { version: '1', async execute(_args, { requestInput }) {
        if (pause) return requestInput('approval', request);
        throw new RetryableMcpTaskError();
      } },
    } });
    await initial.start();
    t.after(() => initial.stop());
    const handle = await initial.create('build_report', {}, undefined, 'open', { elicitation: { form: {} } }, contract);
    await eventually(async () => {
      const row = (await pool.query('SELECT status,attempt_count FROM gregale_mcp_tasks WHERE task_id=$1', [handle.taskId])).rows[0];
      return row.attempt_count === 1 && row.status === (pause ? 'input_required' : 'queued');
    }, 'task did not pause');
    await initial.stop();
    contract.$defs.amount.type = 'string';
    if (pause) await store.updateInputs({ taskID: handle.taskId, authMode: 'open', inputResponses: { approval: { action: 'accept', content: {} } } });
    else await pool.query('UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp() WHERE task_id=$1', [handle.taskId]);
    const execute = async (_args, { requestInput }) => {
      if (pause) assert.equal((await requestInput('approval', request)).action, 'accept');
      return { content: [], structuredContent: { amount: 2 } };
    };
    const replacement = createMcpTaskRuntime({ store, pollIntervalMs: 500, handlers: { build_report: { version: '2', execute, previousVersions: { '1': execute } } } });
    await replacement.start();
    try {
      await eventually(async () => (await replacement.get(handle.taskId, undefined, 'open')).status === 'completed', 'replacement worker lost the saved output contract');
    } finally { await replacement.stop(); }
  });
});

test('output contract expansion requires an explicit migration and preserves legacy tasks', postgresOnly, async t => {
  const { store, pool } = await harness(t);
  await pool.query('ALTER TABLE gregale_mcp_tasks DROP COLUMN output_schema');
  await assert.rejects(store.initialize());
  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  await store.initialize();
  const producer = createMcpTaskRuntime({ store, workerEnabled: false, handlers: {
    build_report: { version: '1', async execute() { return { content: [] }; } },
  } });
  await producer.start();
  try {
    for (const schema of [{ type: 42 }, { $ref: 'https://provider.example/schema' }]) {
      await assert.rejects(producer.create('build_report', {}, undefined, 'open', {}, schema));
    }
    assert.equal((await pool.query('SELECT count(*)::int AS count FROM gregale_mcp_tasks')).rows[0].count, 0);
    const handle = await producer.create('build_report', {}, undefined, 'open');
    assert.equal((await pool.query('SELECT output_schema FROM gregale_mcp_tasks WHERE task_id=$1', [handle.taskId])).rows[0].output_schema, null);
    const worker = createMcpTaskRuntime({ store, pollIntervalMs: 500, handlers: { build_report: { version: '1', async execute() { return { content: [] }; } } } });
    await worker.start();
    try {
      await eventually(async () => (await worker.get(handle.taskId, undefined, 'open')).status === 'completed', 'legacy task failed after migration');
    } finally { await worker.stop(); }
  } finally { await producer.stop(); }
});

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

async function harness(t, { legacySchema = false, maxRunning = 64, maxRunningPerOwner = 64, ownerKey = randomBytes(48).toString('base64url'), encryptionKeys, ttlMs = 60_000 } = {}) {
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
  const store = createPostgresMcpTaskStore({
    pool,
    namespace,
    ownerKey,
    encryptionKeys,
    ttlMs,
    maxRunning, maxRunningPerOwner,
  });
  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  return { adminPool, namespace, ownerKey, pool, schema, store };
}

test('cancelled expired leases settle without another execution attempt', postgresOnly, async t => {
  for (const maxAttempts of [1, 3]) await t.test(`budget ${maxAttempts}`, async t => {
    const { store, pool } = await harness(t);
    const task = await create(store, 'alice');
    const original = await store.claim(maxAttempts, 30_000);
    assert.equal(await store.requestCancel({ taskID: task.task_id, authInfo: principal('alice'), authMode: 'external-oauth' }), true);
    assert.equal(await store.claim(maxAttempts, 30_000), null);
    const live = (await pool.query('SELECT status,lease_token::text FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0];
    assert.deepEqual(live, { status: 'running', lease_token: original.lease_token });
    await pool.query("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [task.task_id]);
    const next = await create(store, 'bob');
    assert.equal((await store.claim(maxAttempts, 30_000)).task_id, next.task_id);
    const settled = (await pool.query(`SELECT status,attempt_count,
      lease_token IS NULL AND lease_expires_at IS NULL AND NOT resume_pending AND next_attempt_at IS NULL AS cleared
      FROM gregale_mcp_tasks WHERE task_id=$1`, [task.task_id])).rows[0];
    assert.deepEqual(settled, { status: 'cancelled', attempt_count: 1, cleared: true });
  });
});

test('visible ASCII tool names work through admission, worker claims and results', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const admission = createMcpTaskAdmissionController({ pool, namespace });
  for (const name of ['BuildReport', 'acme:report/build', '9lookup', '_lookup', 'a'.repeat(128)]) {
    const handlers = { [name]: { version: '1', async execute() { return { content: [{ type: 'text', text: name }] }; } } };
    await store.migrate({ admissionHandlers: [{ name, version: '1' }] });
    const runtime = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
    try {
      await runtime.start();
      const task = await runtime.create(name, {}, principal('alice'), 'external-oauth');
      // Startup returns before queue draining; allow multiple worker polls.
      const result = await eventually(async () => {
        const value = await store.get({ taskID: task.taskId, authInfo: principal('alice'), authMode: 'external-oauth' });
        return value.status === 'completed' ? value : null;
      }, `worker did not complete task for ${name}`);
      assert.equal(result.status, 'completed');
      assert.equal(result.result.content[0].text, name);
      await admission.change('disable', name, '1');
      await assert.rejects(runtime.create(name, {}, principal('alice'), 'external-oauth'), /not accepting/);
    } finally { await runtime.stop(); }
  }
});

test('preflight authenticates every retained legacy payload before retiring a key', postgresOnly, async t => {
  for (const [field, column, value] of [
    ['arguments', 'arguments_encrypted', { report: 'retained' }],
    ['result', 'result_encrypted', { content: [] }],
    ['error', 'error_encrypted', { code: -32603, message: 'Failure' }],
    ['input-state', 'input_state_encrypted', { requests: {}, responses: {} }],
  ]) await t.test(field, async t => {
    const { pool, namespace, schema, ownerKey } = await harness(t);
    const previousKey = randomBytes(48).toString('base64url');
    const nativeOwnerKeys = [{ id: 'default', key: ownerKey }, { id: 'previous', key: previousKey }];
    const full = createPostgresMcpTaskStore({ pool, namespace, ownerKey, nativeOwnerKeys, ttlMs: 60_000 });
    await full.migrate({ admissionHandlers: [{ name: 'build_report', version: '2' }] });
    const records = [];
    for (let n = 0; n < 34; n++) records.push(await full.create({ toolName: 'build_report', handlerVersion: '2', args: {}, authMode: 'open' }));
    records.sort((a, b) => a.task_id.localeCompare(b.task_id));
    const retained = records.at(-1); // Neither a sample nor the first batch proves readability.
    const payload = createMcpTaskPayloadCipher(previousKey).encrypt(Buffer.from(JSON.stringify(value)), `gregale-mcp-task:v1:${namespace}:${retained.task_id}:${field}`);
    await pool.query(`UPDATE gregale_mcp_tasks SET ${column}=$1 WHERE task_id=$2`, [payload, retained.task_id]);
    await full.initialize();
    const reduced = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
    await assert.rejects(reduced.initialize(), /authentication failed/);
    await assert.rejects(reduced.migrate(), /authentication failed/);
    const { run } = await taskDoctorFixture(t, { schema, namespace, ownerKey });
    const doctor = await run('task-doctor.js');
    assert.equal(doctor.code, 1);
    assert.ok(doctor.report.checks.some(check => check.name === 'encryption_keys' && check.status === 'failed'));
    assert.equal(JSON.stringify(doctor.report).includes(previousKey), false);
    await pool.query("UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [retained.task_id]);
    await reduced.initialize();
    await reduced.migrate();
  });
});

test('a claim committed during shutdown is released without exhausting its unexecuted attempt', postgresOnly, async t => {
  const { store, pool } = await harness(t);
  const task = await create(store, 'alice');
  let committed, releaseResponse, drained, executions = 0;
  const committedPromise = new Promise(resolve => { committed = resolve; });
  const responsePromise = new Promise(resolve => { releaseResponse = resolve; });
  const drainedPromise = new Promise(resolve => { drained = resolve; });
  const originalClaim = store.claim.bind(store), originalDraining = store.workerDraining.bind(store);
  store.claim = async (...args) => { const row = await originalClaim(...args); committed(); await responsePromise; return row; };
  store.workerDraining = async (...args) => { await originalDraining(...args); drained(); };
  const runtime = createMcpTaskRuntime({ store, maxAttempts: 1, handlers: { build_report: { version: '1', async execute() { executions++; return { content: [] }; } } } });
  const starting = runtime.start();
  await committedPromise;
  const stopping = runtime.stop();
  await drainedPromise;
  releaseResponse();
  await starting;
  assert.deepEqual(await stopping, { timedOut: false });
  assert.equal(executions, 0);
  const row = (await pool.query('SELECT status,attempt_count,resume_pending FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0];
  assert.deepEqual(row, { status: 'queued', attempt_count: 1, resume_pending: true });
  const recovered = await originalClaim(1, 30000);
  assert.ok(recovered);
  assert.equal(recovered.attempt_count, 1);
});

test('canonical UUID aliases preserve input updates and runtime subscriptions for both encryption formats', postgresOnly, async t => {
  for (const encryptionKeys of [undefined, { activeKeyId: 'current', keys: { current: 'test-encryption-'.repeat(3) } }]) {
    await t.test(encryptionKeys ? 'v2' : 'v1', async t => {
      const { store } = await harness(t, { encryptionKeys });
      const task = await create(store, 'alice');
      const claimed = await store.claim(3, 30000);
      await store.requestInputs({ taskID: task.task_id, leaseToken: claimed.lease_token, requests: { approval: { method: 'roots/list', params: {} } } });
      const authInfo = principal('alice'), upper = task.task_id.toUpperCase();
      const runtime = createMcpTaskRuntime({ store, handlers: {}, workerEnabled: false });
      await runtime.start();
      try {
        const subscription = await runtime.subscribe([task.task_id, upper], authInfo, 'external-oauth', () => true, () => {});
        assert.deepEqual(subscription.taskIds, [task.task_id]);
        assert.equal(subscription.tasks.length, 1);
        subscription.close();
        assert.equal(await store.updateInputs({ taskID: upper, authInfo, authMode: 'external-oauth', inputResponses: { approval: { roots: [] } } }), true);
        assert.equal((await store.get({ taskID: task.task_id, authInfo, authMode: 'external-oauth' })).status, 'queued');
        assert.equal(await store.get({ taskID: upper, authInfo: principal('bob'), authMode: 'external-oauth' }), null);
      } finally { await runtime.stop(); }
    });
  }
});

test('canonical and retained Go owner aliases share execution capacity across authentication and group merges', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t, { maxRunning: 3, maxRunningPerOwner: 1 });
  const first = await create(store, 'alice');
  const second = await create(store, 'alice');
  const aliases = (await pool.query('SELECT owner_hash FROM gregale_mcp_task_owners WHERE namespace=$1', [namespace])).rows.map(row => row.owner_hash);
  const canonical = (await pool.query('SELECT owner_hash FROM gregale_mcp_tasks WHERE task_id=$1', [first.task_id])).rows[0].owner_hash;
  const legacy = aliases.find(value => !value.equals(canonical));
  await pool.query('UPDATE gregale_mcp_tasks SET owner_hash=$2 WHERE task_id=$1', [second.task_id, legacy]);
  // Simulate an upgraded queue whose callers have not returned yet.
  await pool.query('DELETE FROM gregale_mcp_task_owners WHERE namespace=$1', [namespace]);
  assert.ok(await store.claim(3, 30000));
  assert.equal(await store.claim(3, 30000), null);
  await get(store, first.task_id, 'alice');
  assert.equal(await store.claim(3, 30000), null);
  const bob = await create(store, 'bob');
  assert.equal((await store.claim(3, 30000)).task_id, bob.task_id);
  const { associateMcpTaskOwners } = await import('../task-owners.js');
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await associateMcpTaskOwners(client, namespace, [Buffer.from('old-generation'), canonical]);
    await associateMcpTaskOwners(client, namespace, [Buffer.from('new-generation'), Buffer.from('old-generation')]);
    await client.query('COMMIT');
  } finally { await client.query('ROLLBACK'); client.release(); }
  const groups = await pool.query('SELECT COUNT(DISTINCT owner_group)::int AS count FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[])', [namespace, [...aliases, Buffer.from('old-generation'), Buffer.from('new-generation')]]);
  assert.equal(groups.rows[0].count, 1);
  const metrics = await store.queueMetrics();
  assert.equal(metrics.runningTasks, 2);
  assert.equal(metrics.capacityWaitingTasks, 1);
});

test('migration and runtime startup accept retained native v2 Tasks and preserve authenticated payloads', postgresOnly, async t => {
  const { pool, namespace, ownerKey, store } = await harness(t);
  const task = await create(store, 'alice');
  const nativeID = 'initial';
  const handoff = createPostgresMcpTaskStore({ pool, namespace, ownerKey, nativeOwnerKeys: [{ id: nativeID, key: ownerKey }], ttlMs: 60000 });
  function nativePayload(value, field) {
    const key = createHmac('sha256', ownerKey).update('gregale-mcp-task-payload:v1').digest();
    const nonce = randomBytes(12);
    const cipher = createCipheriv('aes-256-gcm', key, nonce);
    cipher.setAAD(Buffer.from(`gregale-mcp-task:v1:${namespace}:${task.task_id}:${field}`));
    const encrypted = Buffer.concat([cipher.update(JSON.stringify(value)), cipher.final()]);
    return Buffer.concat([Buffer.from([2, nativeID.length]), Buffer.from(nativeID), nonce, cipher.getAuthTag(), encrypted]);
  }
  await pool.query('UPDATE gregale_mcp_tasks SET arguments_encrypted=$2 WHERE task_id=$1', [task.task_id, nativePayload({ report: 'retained' }, 'arguments')]);
  await handoff.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  await handoff.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  const leased = await handoff.claim(3, 30000);
  assert.deepEqual(leased.arguments, { report: 'retained' });
  await handoff.complete(leased.task_id, leased.lease_token, { content: [] });
  await pool.query('UPDATE gregale_mcp_tasks SET result_encrypted=$2 WHERE task_id=$1', [task.task_id, nativePayload({ content: [{ type: 'text', text: 'retained result' }] }, 'result')]);
  await handoff.initialize();
  const retained = await get(handoff, task.task_id, 'alice');
  assert.equal(retained.result.content[0].text, 'retained result');
  const wrong = createPostgresMcpTaskStore({ pool, namespace, ownerKey, nativeOwnerKeys: [{ id: nativeID, key: 'wrong'.repeat(8) }], ttlMs: 60000 });
  await assert.rejects(wrong.initialize(), /stable/);
});

test('owner cleanup keeps every alias until the final retained Task expires', postgresOnly, async t => {
  const { pool, namespace, store } = await harness(t);
  const task = await create(store, 'alice');
  const canonical = (await pool.query('SELECT owner_hash FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0].owner_hash;
  const legacy = (await pool.query('SELECT owner_hash FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash<>$2', [namespace, canonical])).rows[0].owner_hash;
  await pool.query('UPDATE gregale_mcp_tasks SET owner_hash=$2 WHERE task_id=$1', [task.task_id, legacy]);
  await get(store, task.task_id, 'unused-owner');
  await store.cleanupExpired();
  assert.equal((await pool.query('SELECT 1 FROM gregale_mcp_task_owners WHERE namespace=$1', [namespace])).rowCount, 2);
  await pool.query("UPDATE gregale_mcp_tasks SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [task.task_id]);
  await store.cleanupExpired();
  assert.equal((await pool.query('SELECT 1 FROM gregale_mcp_task_owners WHERE namespace=$1', [namespace])).rowCount, 0);
});

test('claims roll back unreadable payloads without consuming attempts', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  for (const column of ['arguments_encrypted', 'result_encrypted', 'error_encrypted', 'input_state_encrypted']) {
    const task = await create(store, 'alice');
    await pool.query(`UPDATE gregale_mcp_tasks SET ${column}=$2 WHERE task_id=$1`, [task.task_id, Buffer.from('corrupted payload')]);
    for (let attempt=0; attempt<3; attempt++) await assert.rejects(store.claim(3, 30000));
    const row = (await pool.query('SELECT status,attempt_count,lease_token FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0];
    assert.deepEqual(row, { status: 'queued', attempt_count: 0, lease_token: null }, column);
    await pool.query('DELETE FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id]);
  }
});

test('worker writes require a live lease and unexpired Task', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  for (const operation of ['complete', 'cancel', 'input', 'retry']) {
    for (const condition of ['live', 'lease-expired', 'ttl-expired', 'replaced']) {
      const task = await store.create({ toolName: 'build_report', handlerVersion: '1', args: { report: 'test' },
        authInfo: principal('alice'), authMode: 'external-oauth', inputMethods: ['elicitation/create:form'] });
      const leased = await store.claim(3, 30000);
      const patch = { 'lease-expired': "lease_expires_at=clock_timestamp()-interval '1 second'",
        'ttl-expired': "created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second'",
        replaced: 'lease_token=gen_random_uuid()' }[condition];
      if (patch) await pool.query(`UPDATE gregale_mcp_tasks SET ${patch} WHERE task_id=$1`, [task.task_id]);
      if (operation==='complete') await store.complete(task.task_id, leased.lease_token, { content: [] });
      if (operation==='cancel') await store.finishCancelled(task.task_id, leased.lease_token);
      if (operation==='retry') await store.fail(task.task_id, leased.lease_token, { code: -32603, message: 'test' }, { retryable: true });
      if (operation==='input') await store.requestInputs({ taskID: task.task_id, leaseToken: leased.lease_token,
        requests: { approve: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } } });
      const row = (await pool.query('SELECT status,result_encrypted,input_state_encrypted FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0];
      const expected = condition==='live' ? { complete: 'completed', cancel: 'cancelled', input: 'input_required', retry: 'queued' }[operation] : 'running';
      assert.equal(row.status, expected, `${operation}/${condition}`);
      if (condition!=='live') { assert.equal(row.result_encrypted, null); assert.equal(row.input_state_encrypted, null); }
      await pool.query("UPDATE gregale_mcp_tasks SET status='failed',lease_token=NULL,lease_expires_at=NULL WHERE task_id=$1", [task.task_id]);
    }
  }
});

test('completion rechecks lease expiry after waiting for a row lock', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  const task = await create(store, 'alice');
  const lease = await store.claim(3, 30000);
  await pool.query("UPDATE gregale_mcp_tasks SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE task_id=$1", [task.task_id]);
  const lock = await pool.connect();
  let completion;
  try {
    await lock.query('BEGIN');
    await lock.query('SELECT 1 FROM gregale_mcp_tasks WHERE task_id=$1 FOR UPDATE', [task.task_id]);
    completion = store.complete(task.task_id, lease.lease_token, { content: [] });
    const deadline=Date.now()+5000;
    while (!(await pool.query("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'WITH lease AS MATERIALIZED%') AS waiting")).rows[0].waiting) {
      assert.ok(Date.now()<deadline, 'completion did not block on the row lock'); await delay(5);
    }
    while (!(await pool.query('SELECT lease_expires_at<=clock_timestamp() AS expired FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0].expired) {
      assert.ok(Date.now()<deadline, 'lease did not expire'); await delay(5);
    }
    await lock.query('COMMIT');
    assert.equal(await completion, null);
    assert.equal((await pool.query('SELECT status FROM gregale_mcp_tasks WHERE task_id=$1', [task.task_id])).rows[0].status, 'running');
  } finally { await lock.query('ROLLBACK'); lock.release(); await completion?.catch(() => {}); }
});

test('Node preserves canonical identity and access to retained Go owner hashes', postgresOnly, async t => {
  const ownerKey = '01234567890123456789012345678901';
  const { pool, namespace, store } = await harness(t, { ownerKey });
  const alice = { resource: new URL('https://example.com/mcp'), extra: { subject: 'alice' }, clientId: 'test-client' };
  const bob = { ...alice, extra: { subject: 'bob' } };
  const task = await store.create({ toolName: 'build_report', handlerVersion: '2', args: { report: 'test', steps: 1 }, authInfo: alice, authMode: 'external-oauth' });
  const raw = await pool.query("SELECT encode(owner_hash,'hex') AS owner FROM gregale_mcp_tasks WHERE task_id=$1", [task.task_id]);
  assert.equal(raw.rows[0].owner, '4fbc7a60b90569b9ac1b0d8b0c1f59ce770120370a5da54c662ce779b04edcab');
  const claimed = await store.claim(3, 30_000, [{ name: 'build_report', version: '2' }]);
  await store.requestInputs({ taskID: task.task_id, leaseToken: claimed.lease_token,
    requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } } });
  await pool.query("UPDATE gregale_mcp_tasks SET owner_hash=decode($2,'hex') WHERE task_id=$1",
    [task.task_id, '803002df4e65c74a4f74c141874d907b0d661cb39332bc05c3424af3145a2928']);
  const request = { taskID: task.task_id, authInfo: alice, authMode: 'external-oauth' };
  assert.equal((await store.get(request)).task_id, task.task_id);
  assert.equal((await store.getMany({ ...request, taskIDs: [task.task_id] })).length, 1);
  assert.equal(await store.get({ ...request, authInfo: bob }), null);
  assert.equal(await store.updateInputs({ ...request, authInfo: bob, inputResponses: { approval: { action: 'accept' } } }), false);
  assert.equal(await store.requestCancel({ ...request, authInfo: bob }), false);
  assert.equal(await store.updateInputs({ ...request, inputResponses: { approval: { action: 'accept' } } }), true);
  const limited = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000, maxOutstandingPerOwner: 1 });
  await assert.rejects(limited.create({ toolName: 'build_report', handlerVersion: '2', args: {}, authInfo: alice, authMode: 'external-oauth' }), /capacity/);
  assert.equal(await store.requestCancel(request), true);
  assert.equal((await store.get(request)).status, 'cancelled');
});

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

function sqlIdentifier(value) {
  return `"${value.replaceAll('"', '""')}"`;
}

async function eventually(fn, message, attempts = 100) {
  for (let attempt = 0; attempt < attempts; attempt++) {
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

test('PostgreSQL observer metrics require a role without table writes or schema creation privileges', postgresOnly, async t => {
  const { adminPool, namespace, schema, pool, store } = await harness(t);
  const role = `gregale_mcp_observer_${randomUUID().replaceAll('-', '')}`;
  const password = randomBytes(32).toString('hex');
  const quotedRole = sqlIdentifier(role);
  const quotedSchema = sqlIdentifier(schema);
  const databaseName = (await adminPool.query('SELECT current_database() AS name')).rows[0].name;
  const quotedDatabase = sqlIdentifier(databaseName);
  let observerPool;
  let roleCreated = false;

  try {
    await adminPool.query(`CREATE ROLE ${quotedRole} LOGIN PASSWORD '${password}'`);
    roleCreated = true;
    await adminPool.query(`GRANT CONNECT ON DATABASE ${quotedDatabase} TO ${quotedRole}`);
    await adminPool.query(`GRANT USAGE ON SCHEMA ${quotedSchema} TO ${quotedRole}`);
    await adminPool.query(`GRANT SELECT ON TABLE ${quotedSchema}.gregale_mcp_tasks TO ${quotedRole}`);
    await adminPool.query(`GRANT SELECT ON TABLE ${quotedSchema}.gregale_mcp_task_worker_handlers, ${quotedSchema}.gregale_mcp_task_owners TO ${quotedRole}`);

    const observerURL = new URL(databaseURL);
    observerURL.username = role;
    observerURL.password = password;
    observerPool = new Pool({
      connectionString: observerURL.href,
      max: 2,
      connectionTimeoutMillis: 5_000,
      statement_timeout: 10_000,
      options: `-c search_path=${schema}`,
    });
    await adminPool.query(`GRANT SELECT ON TABLE ${quotedSchema}.gregale_mcp_task_workers TO ${quotedRole}`);
    const observer = createMcpTaskQueueObserver({ pool: observerPool, namespace, compatibility: true });
    const metrics = await observer.queueMetrics();
    assert.equal(metrics.outstandingTasks, 0);
    assert.equal(metrics.oldestAgeSeconds, 0);
    assert.equal(metrics.withoutCompatibleWorkerTasks, 0);
    assert.deepEqual(metrics.handlerCompatibility, []);
    assert.equal(metrics.compatibilityTruncated, false);
    const retry = await create(store, 'alice');
    for (let attempt = 1; attempt <= 3; attempt++) {
      const lease = await store.claim(5, 30_000);
      assert.equal(lease.attempt_count, attempt);
      await store.fail(retry.task_id, lease.lease_token, { code: -32603, message: 'Retryable failure' }, { retryable: true, maxAttempts: 5, retryDelayMs: 10_000 });
      if (attempt < 3) await pool.query("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [retry.task_id]);
    }
    const retryObserver = createMcpTaskQueueObserver({ pool: observerPool, namespace, maxAttempts: 5, compatibility: true });
    const delayed = await retryObserver.queueMetrics();
    assert.equal(delayed.retryWaitingTasks, 1);
    assert.equal(delayed.withoutCompatibleWorkerTasks, 0, 'future retries are not currently blocked work');
    await pool.query("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [retry.task_id]);
    const due = await retryObserver.queueMetrics();
    assert.equal(due.withoutCompatibleWorkerTasks, 1, 'a due fourth attempt still needs a compatible worker');
    assert.equal(due.handlerCompatibility[0].waiting_tasks, 1);
    assert.equal((await observer.queueMetrics()).withoutCompatibleWorkerTasks, 0, 'the default budget remains three attempts');
    const fourth = await store.claim(5, 30_000);
    assert.equal(fourth.attempt_count, 4);
    assert.equal((await retryObserver.queueMetrics()).withoutCompatibleWorkerTasks, 0, 'live leases are not blocked work');
    await store.releaseMany([fourth]);
    assert.equal((await observer.queueMetrics()).withoutCompatibleWorkerTasks, 1, 'a drain resume remains eligible above the current budget');
    for (const privilege of ['INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER']) {
      await adminPool.query(`GRANT ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_owners TO ${quotedRole}`);
      await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
      await adminPool.query(`REVOKE ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_owners FROM ${quotedRole}`);
    }
    for (const privilege of ['INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER']) {
      await adminPool.query(`GRANT ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_workers TO ${quotedRole}`);
      await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
      await adminPool.query(`REVOKE ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_workers FROM ${quotedRole}`);
    }

    for (const privilege of ['INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER']) {
      await adminPool.query(`GRANT ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_worker_handlers TO ${quotedRole}`);
      await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
      await adminPool.query(`REVOKE ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_task_worker_handlers FROM ${quotedRole}`);
    }

    for (const privilege of ['INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER']) {
      await adminPool.query(`GRANT ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_tasks TO ${quotedRole}`);
      await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
      await adminPool.query(`REVOKE ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_tasks FROM ${quotedRole}`);
    }

    for (const privilege of ['INSERT (namespace)', 'UPDATE (namespace)', 'REFERENCES (namespace)']) {
      await adminPool.query(`GRANT ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_tasks TO ${quotedRole}`);
      await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
      await adminPool.query(`REVOKE ${privilege} ON TABLE ${quotedSchema}.gregale_mcp_tasks FROM ${quotedRole}`);
    }

    await adminPool.query(`GRANT CREATE ON SCHEMA ${quotedSchema} TO ${quotedRole}`);
    await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
    await adminPool.query(`REVOKE CREATE ON SCHEMA ${quotedSchema} FROM ${quotedRole}`);

    await adminPool.query(`GRANT CREATE ON DATABASE ${quotedDatabase} TO ${quotedRole}`);
    await assert.rejects(observer.queueMetrics(), /read-only PostgreSQL access/);
    await adminPool.query(`REVOKE CREATE ON DATABASE ${quotedDatabase} FROM ${quotedRole}`);
  } finally {
    await observerPool?.end();
    if (roleCreated) {
      await adminPool.query(`DROP OWNED BY ${quotedRole}`).catch(() => {});
      await adminPool.query(`DROP ROLE IF EXISTS ${quotedRole}`);
    }
  }
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
  await retryingStore.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

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

  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

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
  await store.migrate({ admissionHandlers: [{ name: 'approval', version: '1' }] });
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
  await otherStore.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });

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

test('PostgreSQL settles queued retries when the attempt budget is reduced', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  const record = await create(store, 'alice');
  for (let attempt = 1; attempt <= 3; attempt++) {
    const lease = await store.claim(5, 30_000);
    assert.equal(lease.attempt_count, attempt);
    await store.fail(record.task_id, lease.lease_token, { code: -32603, message: 'Retryable failure' }, { retryable: true, maxAttempts: 5, retryDelayMs: 10_000 });
    if (attempt < 3) await pool.query("UPDATE gregale_mcp_tasks SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", [record.task_id]);
  }
  assert.equal(await store.claim(3, 30_000), null);
  const task = await get(store, record.task_id, 'alice');
  assert.equal(task.status, 'failed');
  assert.equal(task.attempt_count, 3);
  assert.equal((await pool.query('SELECT next_attempt_at,resume_pending FROM gregale_mcp_tasks WHERE task_id=$1', [record.task_id])).rows[0].next_attempt_at, null);
  assert.equal((await store.queueMetrics()).outstandingTasks, 0);
});

test('PostgreSQL preserves live attempts and input/drain resumptions after lowering the budget', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  for (const mode of ['input', 'drain']) {
    const record = await create(store, 'alice');
    await pool.query('UPDATE gregale_mcp_tasks SET attempt_count=3 WHERE task_id=$1', [record.task_id]);
    const active = await store.claim(5, 30_000);
    assert.equal(active.attempt_count, 4);
    assert.equal(await store.claim(3, 30_000), null, 'a reduced budget must preserve a live attempt');
    if (mode === 'input') {
      await store.requestInputs({ taskID: record.task_id, leaseToken: active.lease_token,
        requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } },
      });
      await store.updateInputs({ taskID: record.task_id, authInfo: principal('alice'), authMode: 'external-oauth',
        inputResponses: { approval: { action: 'accept', content: {} } },
      });
    } else await store.releaseMany([active]);
    const resumed = await store.claim(3, 30_000);
    assert.equal(resumed.task_id, record.task_id);
    assert.equal(resumed.attempt_count, 4, 'resumption must keep the existing attempt count');
    await store.fail(record.task_id, resumed.lease_token, { code: -32603, message: 'Failed' }, { retryable: true, maxAttempts: 3 });
    assert.equal((await get(store, record.task_id, 'alice')).status, 'failed');
    assert.equal(await store.claim(3, 30_000), null, 'resumption must not grant another attempt');
  }
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
  assert.deepEqual(await store.queueMetrics(), { outstandingTasks: 0, oldestAgeSeconds: 0, runningTasks: 0, capacityWaitingTasks: 0, failedTasks: 0, retryWaitingTasks: 0, activeWorkers: 0, drainingWorkers: 0, unsupportedHandlerTasks: 0, withoutCompatibleWorkerTasks: 0 });
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

test('heartbeat renewal rechecks lease and Task expiry after a row lock', postgresOnly, async t => {
  for (const field of ['lease_expires_at', 'expires_at']) await t.test(field, async t => {
    const { pool, store } = await harness(t);
    const task = await create(store, 'alice');
    const lease = await store.claim(3, 30_000);
    await pool.query(`UPDATE gregale_mcp_tasks SET ${field}=clock_timestamp()+interval '1 second' WHERE task_id=$1`, [task.task_id]);
    const lock = await pool.connect();
    let renewal;
    try {
      await lock.query('BEGIN');
      await lock.query('SELECT task_id FROM gregale_mcp_tasks WHERE task_id=$1 FOR UPDATE', [task.task_id]);
      renewal = store.heartbeat(task.task_id, lease.lease_token, 30_000);
      await eventually(async () => (await pool.query("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%UPDATE gregale_mcp_tasks%lease_expires_at%') AS waiting")).rows[0].waiting, 'heartbeat must wait for the row lock');
      await eventually(async () => (await pool.query(`SELECT ${field}<=clock_timestamp() AS expired FROM gregale_mcp_tasks WHERE task_id=$1`, [task.task_id])).rows[0].expired, 'Task or lease must expire while locked');
      await lock.query('COMMIT');
      assert.deepEqual(await renewal, { owned: false, cancelRequested: false });
      assert.equal((await pool.query(`SELECT ${field}<=clock_timestamp() AS expired FROM gregale_mcp_tasks WHERE task_id=$1`, [task.task_id])).rows[0].expired, true);
      if (field === 'lease_expires_at') assert.notEqual((await store.claim(3, 30_000)).lease_token, lease.lease_token);
    } finally { await lock.query('ROLLBACK'); lock.release(); await renewal?.catch(() => {}); }
  });
});


test('PostgreSQL persists retry delays, releases capacity and bounds retries by attempts and TTL', postgresOnly, async t => {
  const { store, pool, namespace, ownerKey } = await harness(t);
  const record = await create(store, 'alice');
  const lease = await store.claim(3, 60_000);
  assert.equal(await store.fail(lease.task_id, lease.lease_token, { message: 'sanitized' }, { retryable: true, maxAttempts: 2, retryDelayMs: 30_000 }), 'queued');
  const restarted = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
  await restarted.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
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
  await old.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  const task = await create(old, 'alice');
  const running = await old.claim(3, 60000);
  await old.requestInputs({ taskID: task.task_id, leaseToken: running.lease_token, requests: { approval: { method: 'elicitation/create', params: { mode: 'form', message: 'Approve?' } } } });
  const rotated = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'b', keys: { a, b } } });
  await rotated.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  assert.deepEqual((await get(rotated, legacy.task_id, 'alice')).result, { legacy: true });
  assert.equal(await get(rotated, task.task_id, 'bob'), null);
  await rotated.updateInputs({ taskID: task.task_id, authInfo: principal('alice'), authMode: 'external-oauth', inputResponses: { approval: { action: 'accept', content: {} } } });
  const resumed = await rotated.claim(3, 60000);
  assert.equal(resumed.task_id, task.task_id);
  assert.deepEqual(resumed.arguments, { report: 'weekly' });
  await rotated.complete(resumed.task_id, resumed.lease_token, { rotated: true });
  assert.deepEqual((await get(rotated, task.task_id, 'alice')).result, { rotated: true });
  const withoutOld = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'b', keys: { b } } });
  await assert.rejects(withoutOld.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /unavailable/);
  await assert.rejects(createPostgresMcpTaskStore(options).migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /unavailable/, 'falling back to legacy writes cannot omit live versioned keys');
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE task_id = $1", [task.task_id]);
  await withoutOld.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
  const changedSecret = createPostgresMcpTaskStore({ ...options, encryptionKeys: { activeKeyId: 'a', keys: { a: b } } });
  await assert.rejects(changedSecret.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /must remain stable/);
  await pool.query("UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE namespace = $1", [namespace]);
  const changedOwner = createPostgresMcpTaskStore({ ...options, ownerKey: 'new-owner'.repeat(8), encryptionKeys: { activeKeyId: 'b', keys: { b } } });
  await assert.rejects(changedOwner.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] }), /must remain stable/);
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
  await other.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }] });
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

async function taskDoctorFixture(t, { schema, namespace, ownerKey }) {
  const { mkdtemp, copyFile, writeFile, rm } = await import('node:fs/promises');
  const { execFile } = await import('node:child_process');
  const { promisify } = await import('node:util');
  const root = dirname(dirname(fileURLToPath(import.meta.url)));
  const fixture = await mkdtemp(join(root, 'doctor-fixture-'));
  t.after(() => rm(fixture, { recursive: true, force: true }));
  for (const file of ['task-doctor.js', 'tasks-compatibility.js', 'task-compatibility.js', 'tasks.js', 'task-output.js', 'task-lease.js', 'task-runtime.js', 'task-handlers.js', 'task-store.js', 'task-inbox.js', 'task-owners.js', 'task-crypto.js', 'task-admission.js', 'task-schema.js', 'tasks-admission.js', 'tasks-migrate.js', 'tasks-roles.js', 'task-metrics.js', 'task-limits.json']) await copyFile(join(root, file), join(fixture, file));
  await writeFile(join(fixture, 'package.json'), JSON.stringify({ type: 'module' }));
  await writeFile(join(fixture, 'gregale-mcp.json'), JSON.stringify({ tasks: { enabled: true, database_url_env: 'DOCTOR_DATABASE', owner_key_env: 'DOCTOR_OWNER' } }));
  const url = new URL(databaseURL);
  url.searchParams.set('options', `-c search_path=${schema}`);
  const env = { ...process.env, DOCTOR_DATABASE: url.href, DOCTOR_OWNER: ownerKey, MCP_TASK_NAMESPACE: namespace };
  const run = async (script, overrides = {}, args = []) => {
    try { const result = await promisify(execFile)(process.execPath, [script, ...args], { cwd: fixture, env: { ...env, ...overrides }, timeout: 15000 }); return { code: 0, report: JSON.parse(result.stdout) }; }
    catch (error) { return { code: error.code, report: JSON.parse(error.stdout) }; }
  };
  return { run, url };
}

test('Task doctor and compatibility command return safe deployment reports', postgresOnly, async t => {
  const { pool, schema, namespace, ownerKey, store } = await harness(t);
  const { run, url } = await taskDoctorFixture(t, { schema, namespace, ownerKey });
  await store.workerHeartbeat(randomUUID(), [{ name: 'build_report', version: '1' }]);
  const ready = await run('task-doctor.js');
  assert.equal(ready.code, 0, JSON.stringify(ready.report));
  assert.equal(ready.report.ok, true);
  assert.equal(ready.report.schema.version, 1);
  const status = await run('tasks-migrate.js', { DOCTOR_OWNER: '' }, ['status']);
  assert.equal(status.code, 0);
  assert.equal(status.report.version, 1);
  const apply = await run('tasks-migrate.js', { MCP_TASK_MIGRATION_DATABASE_URL: url.href }, ['apply']);
  assert.equal(apply.code, 0, JSON.stringify(apply.report));
  assert.equal(apply.report.changed, false);
  const withoutBinding = await run('tasks-migrate.js', { MCP_TASK_MIGRATION_DATABASE_URL: '' }, ['apply']);
  assert.equal(withoutBinding.code, 1);
  const observerDoctor = await run('task-doctor.js', { DOCTOR_OWNER: '' }, ['--role', 'observer']);
  assert.equal(observerDoctor.code, 0);
  assert.equal(observerDoctor.report.schema.role, 'observer');
  const operatorDoctor = await run('task-doctor.js', { DOCTOR_OWNER: '' }, ['--role', 'operator']);
  assert.equal(operatorDoctor.code, 0);
  assert.equal(operatorDoctor.report.schema.role, 'operator');
  const currentUser = (await pool.query('SELECT current_user AS name')).rows[0].name;
  const plan = await run('tasks-roles.js', { DOCTOR_OWNER: '' }, ['plan', 'runtime', currentUser]);
  assert.equal(plan.code, 0);
  assert.equal(plan.report.applied, false);
  assert.ok(plan.report.statements.every(statement => statement.startsWith('GRANT ')));
  const grant = await run('tasks-roles.js', { DOCTOR_OWNER: '', MCP_TASK_MIGRATION_DATABASE_URL: url.href }, ['grant', 'runtime', currentUser]);
  assert.equal(grant.code, 0);
  assert.equal(grant.report.applied, true);
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
  const disabled = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['disable', 'build_report', '2']);
  assert.equal(disabled.code, 0);
  assert.equal(disabled.report.versions.find(entry => entry.version === '2').state, 'draining');
  const fenced = await run('task-doctor.js');
  assert.equal(fenced.code, 1);
  assert.ok(fenced.report.checks.some(check => check.name === 'task_admission' && check.status === 'failed'));
  const retired = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['retire', 'build_report', '2']);
  assert.equal(retired.code, 0);
  assert.equal(retired.report.versions.find(entry => entry.version === '2').state, 'retired');
  const audit = await run('tasks-admission.js', { DOCTOR_OWNER: '' }, ['audit']);
  assert.equal(audit.code, 0);
  assert.ok(audit.report.events.some(event => event.handler_version === '2' && event.retired_at));
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
  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
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
  await store.migrate({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
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
    await other.migrate({ admissionHandlers: [{ name: 'build_report', version: '2' }] });
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

test('runtime startup is read-only and rejects missing or future schema versions', postgresOnly, async t => {
  const { pool, namespace, ownerKey, store } = await harness(t);
  const calls = [];
  const wrapped = {
    query: (...args) => pool.query(...args),
    async connect() {
      const client = await pool.connect();
      return { async query(sql, params) { calls.push(sql); return client.query(sql, params); }, release: () => client.release() };
    },
  };
  const reader = createPostgresMcpTaskStore({ pool: wrapped, namespace, ownerKey, ttlMs: 60000 });
  await reader.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  assert.equal(calls[0], 'BEGIN READ ONLY');
  assert.ok(calls.every(sql => !/^\s*(CREATE|ALTER|INSERT|UPDATE|DELETE|DO|LOCK)\b/i.test(sql)), 'startup performs only read-only checks');
  await pool.query('UPDATE gregale_mcp_task_schema SET version = 99');
  await assert.rejects(reader.initialize(), /Unsupported MCP Task schema version/);
  await assert.rejects(store.migrate(), /cannot downgrade/);
  assert.equal((await pool.query('SELECT version FROM gregale_mcp_task_schema')).rows[0].version, 99);
  await pool.query('DELETE FROM gregale_mcp_task_schema');
  await assert.rejects(reader.initialize(), /Unsupported MCP Task schema version/);
  assert.equal((await store.migrate()).schemaVersion, 1);
  assert.equal((await store.migrate()).changed, false, 'repeat migration is idempotent');
});

test('database role profiles support runtime, observer and operator without DDL or policy access for runtime', postgresOnly, async t => {
  const { pool, namespace, ownerKey, schema } = await harness(t);
  const { checkMcpTaskSchema, planMcpTaskDatabaseGrants } = await import('../task-schema.js');
  const { createMcpTaskQueueObserver } = await import('../task-store.js');
  const { createMcpTaskAdmissionController } = await import('../task-admission.js');
  const admin = new Pool({ connectionString: databaseURL, options: `-c search_path=${schema}`, max: 1 });
  const roles = {};
  const pools = [];
  t.after(async () => {
    await Promise.all(pools.map(rolePool => rolePool.end()));
    try { for (const role of Object.values(roles)) { await admin.query(`DROP OWNED BY ${role}`); await admin.query(`DROP ROLE ${role}`); } }
    finally { await admin.end(); }
  });
  for (const profile of ['runtime', 'observer', 'operator']) {
    const role = `mcp_${profile}_${randomUUID().replaceAll('-', '')}`;
    await admin.query(`CREATE ROLE ${role} NOLOGIN`);
    roles[profile] = role;
    const plan = await planMcpTaskDatabaseGrants({ pool: admin, role: profile, databaseRole: role });
    for (const statement of plan.statements) await admin.query(statement);
    const rolePool = new Pool({ connectionString: databaseURL, options: `-c search_path=${schema} -c role=${role}`, max: 2 });
    pools.push(rolePool);
    assert.equal((await checkMcpTaskSchema({ pool: rolePool, namespace, role: profile })).version, 1);
    await assert.rejects(rolePool.query('CREATE TABLE forbidden_ddl (id integer)'), error => error.code === '42501');
  }
  const runtime = createPostgresMcpTaskStore({ pool: pools[0], namespace, ownerKey, ttlMs: 60000 });
  await runtime.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  const task = await create(runtime, 'alice');
  const leased = await runtime.claim(3, 30000, [{ name: 'build_report', version: '1' }]);
  assert.equal(leased.task_id, task.task_id);
  await runtime.complete(leased.task_id, leased.lease_token, { content: [] });
  await assert.rejects(pools[0].query('UPDATE gregale_mcp_task_admission SET enabled = true'), error => error.code === '42501');
  await assert.rejects(pools[0].query('DELETE FROM gregale_mcp_task_crypto_keys'), error => error.code === '42501');
  await assert.rejects(pools[1].query('SELECT * FROM gregale_mcp_task_crypto_keys'), error => error.code === '42501');
  await createMcpTaskQueueObserver({ pool: pools[1], namespace }).queueMetrics();
  const operator = createMcpTaskAdmissionController({ pool: pools[2], namespace });
  await operator.change('disable', 'build_report', '1');
  const event = (await operator.audit()).events[0];
  assert.equal(event.database_role, roles.operator, 'definer trigger preserves the operator identity');
  await assert.rejects(create(runtime, 'alice'), error => error.code === 'MCP_TASK_HANDLER_DISABLED');
  await runtime.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
  await assert.rejects(pools[2].query('DELETE FROM gregale_mcp_tasks'), error => error.code === '42501');
});

test('concurrent migrations serialize and record the baseline once', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  await pool.query('DELETE FROM gregale_mcp_task_schema');
  const results = await Promise.all([store.migrate(), store.migrate()]);
  assert.equal(results.filter(result => result.changed).length, 1);
  assert.deepEqual((await pool.query('SELECT version FROM gregale_mcp_task_schema')).rows, [{ version: 1 }]);
});

test('migration and startup reject a schema writable by PUBLIC and invalid role profiles', postgresOnly, async t => {
  const { pool, schema, namespace, store } = await harness(t);
  const { planMcpTaskDatabaseGrants } = await import('../task-schema.js');
  await assert.rejects(planMcpTaskDatabaseGrants({ pool, role: '__proto__', databaseRole: 'example_role' }), /Invalid MCP Task database role/);
  await pool.query(`GRANT CREATE ON SCHEMA ${schema} TO PUBLIC`);
  await assert.rejects(store.migrate(), /must not grant CREATE to PUBLIC/);
  await assert.rejects(store.initialize(), /must not grant CREATE to PUBLIC/);
  await pool.query(`REVOKE CREATE ON SCHEMA ${schema} FROM PUBLIC`);
  await store.initialize();
});

test('read-only startup waits for an active migration before checking compatibility', postgresOnly, async t => {
  const { pool, store } = await harness(t);
  const migration = await pool.connect();
  let initialized = false;
  try {
    await migration.query('BEGIN');
    await migration.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', ['gregale_mcp_tasks']);
    const starting = store.initialize().then(() => { initialized = true; });
    await delay(50);
    assert.equal(initialized, false);
    await migration.query('COMMIT');
    await starting;
    assert.equal(initialized, true);
  } finally { await migration.query('ROLLBACK').catch(() => {}); migration.release(); }
});

test('release gate starts verified replacements before draining old workers', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const old = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  const candidate = createMcpTaskRuntime({ store, handlers, pollIntervalMs: 500 });
  t.after(async () => { await old.stop(); await candidate.stop(); });
  await old.start();
  await create(store, 'release-owner');
  const report = await releaseMcpTasks({ pool, namespace, store, handlers, timeoutMs: 5000,
    migrate: () => store.migrate({ admissionHandlers: [{ name: 'build_report', version: '2' }] }),
    start: async ({ previousWorkerIDs }) => {
      assert.deepEqual(previousWorkerIDs, [old.workerID]);
      await candidate.start();
      return [candidate.workerID];
    },
    drain: async ({ previousWorkerIDs, replacementWorkerIDs }) => {
      assert.deepEqual(previousWorkerIDs, [old.workerID]);
      assert.deepEqual(replacementWorkerIDs, [candidate.workerID]);
      await old.stop();
    },
  });
  assert.equal(report.ok, true);
  assert.equal(report.stage, 'complete');
});

test('release gate fails closed for missing admitted handlers, false readiness and failed drain', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const full = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  let drained = false;
  const base = { pool, namespace, store, timeoutMs: 100, migrate: async () => {}, drain: async () => { drained = true; } };
  const missing = await releaseMcpTasks({ ...base, handlers: { build_report: { version: '2', execute: async () => ({}) } }, start: async () => { throw new Error('must not start'); } });
  assert.equal(missing.stage, 'candidate_preflight');
  assert.equal(missing.ok, false);
  const unregistered = await releaseMcpTasks({ ...base, handlers: full, start: async () => [randomUUID()] });
  assert.equal(unregistered.stage, 'replacement_readiness');
  assert.equal(unregistered.ok, false);
  assert.equal(drained, false);
  const oldID = randomUUID(), newID = randomUUID();
  const inventory = [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }];
  await store.workerHeartbeat(oldID, inventory);
  const reused = await releaseMcpTasks({ ...base, handlers: full, start: async () => [oldID] });
  assert.equal(reused.stage, 'start_replacements');
  assert.equal(reused.ok, false);
  const stalled = await releaseMcpTasks({ ...base, handlers: full, start: async () => { await store.workerHeartbeat(newID, inventory); return [newID]; } });
  assert.equal(stalled.stage, 'drain_previous');
  assert.equal(stalled.ok, false);
  assert.equal(drained, true);
});

test('release gate serializes namespace rollouts and releases its lock on failure', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const client = await pool.connect();
  await client.query('SELECT pg_advisory_lock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
  try {
    const report = await releaseMcpTasks({ pool, namespace, store, handlers, migrate: async () => { throw new Error('must not run'); }, start: async () => [], drain: async () => {} });
    assert.deepEqual(report, { ok: false, stage: 'release_lock' });
  } finally {
    await client.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
    client.release();
  }
  const report = await releaseMcpTasks({ pool, namespace, store, handlers, migrate: async () => { throw new Error('secret credential'); }, start: async () => [], drain: async () => {} });
  assert.deepEqual(report, { ok: false, stage: 'migrations' });
});

test('release CLI executes bounded adapters without migration credentials', postgresOnly, async t => {
  const { pool, schema, namespace, ownerKey, store } = await harness(t);
  const { mkdtemp, copyFile, writeFile, rm } = await import('node:fs/promises');
  const { execFile } = await import('node:child_process');
  const { promisify } = await import('node:util');
  const root = dirname(dirname(fileURLToPath(import.meta.url)));
  const fixture = await mkdtemp(join(root, 'release-fixture-'));
  t.after(() => rm(fixture, { recursive: true, force: true }));
  for (const file of ['task-release.js', 'tasks-release.js', 'tasks-release-readiness.js', 'task-compatibility.js', 'tasks.js', 'task-output.js', 'task-lease.js', 'task-runtime.js', 'task-handlers.js', 'task-store.js', 'task-inbox.js', 'task-owners.js', 'task-crypto.js', 'task-admission.js', 'task-schema.js', 'task-metrics.js', 'task-limits.json']) await copyFile(join(root, file), join(fixture, file));
  await writeFile(join(fixture, 'package.json'), '{"type":"module"}');
  await writeFile(join(fixture, 'gregale-mcp.json'), JSON.stringify({ tasks: { enabled: true, database_url_env: 'RELEASE_DATABASE', owner_key_env: 'RELEASE_OWNER' } }));
  // The current candidate executes version 2 and retains version 1 for queued work.
  const previous = randomUUID();
  await store.workerHeartbeat(previous, [{ name: 'build_report', version: '1' }]);
  await writeFile(join(fixture, 'adapter.js'), `
    import pg from 'pg';
    import { randomUUID } from 'node:crypto';
    if (process.env.MCP_TASK_MIGRATION_DATABASE_URL) throw new Error('Migration credentials leaked');
    const input = JSON.parse(process.env.MCP_TASK_RELEASE_INPUT);
    const pool = new pg.Pool({ connectionString: process.env.RELEASE_DATABASE });
    try {
      if (process.argv[2] === 'start') {
        const id = randomUUID();
        await pool.query("INSERT INTO gregale_mcp_task_workers (namespace, worker_id, handlers, heartbeat_at, expires_at) VALUES ($1, $2, $3, clock_timestamp(), clock_timestamp() + interval '90 seconds')", [process.env.MCP_TASK_NAMESPACE, id, JSON.stringify([{name:'build_report',version:'1'},{name:'build_report',version:'2'}])]);
        console.log(JSON.stringify({workerIDs:[id]}));
      } else {
        await pool.query('DELETE FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=ANY($2::uuid[])', [process.env.MCP_TASK_NAMESPACE,input.previousWorkerIDs]);
      }
    } finally { await pool.end(); }
  `);
  const releasePlan = { releaseId: 'candidate-one', checkpointPath: join(fixture, 'checkpoint.json'), timeoutMs: 5000, start: [process.execPath, join(fixture, 'adapter.js'), 'start'], drain: [process.execPath, join(fixture, 'adapter.js'), 'drain'] };
  await writeFile(join(fixture, 'plan.json'), JSON.stringify(releasePlan));
  const url = new URL(databaseURL);
  url.searchParams.set('options', `-c search_path=${schema}`);
  const result = await promisify(execFile)(process.execPath, ['tasks-release.js', 'plan.json'], { cwd: fixture, timeout: 15000, env: { ...process.env, RELEASE_DATABASE: url.href, RELEASE_OWNER: ownerKey, MCP_TASK_NAMESPACE: namespace, MCP_TASK_MIGRATION_DATABASE_URL: url.href } });
  assert.equal(JSON.parse(result.stdout).ok, true);
  const replacementWorkerIDs = JSON.parse(result.stdout).replacementWorkerIDs;
  const readiness = await promisify(execFile)(process.execPath, ['tasks-release-readiness.js'], { cwd: fixture, timeout: 15000, env: { ...process.env, RELEASE_DATABASE: url.href, RELEASE_OWNER: '', MCP_TASK_NAMESPACE: namespace, MCP_TASK_RELEASE_INPUT: JSON.stringify({ replacementWorkerIDs }) } });
  assert.deepEqual(JSON.parse(readiness.stdout), { ok: true });
  assert.equal((await pool.query('SELECT 1 FROM gregale_mcp_task_workers WHERE worker_id=$1', [previous])).rowCount, 0);
  const options = { cwd: fixture, timeout: 15000, env: { ...process.env, RELEASE_DATABASE: url.href, RELEASE_OWNER: ownerKey, MCP_TASK_NAMESPACE: namespace, MCP_TASK_MIGRATION_DATABASE_URL: url.href } };
  const retry = await promisify(execFile)(process.execPath, ['tasks-release.js', 'plan.json'], options);
  assert.deepEqual(JSON.parse(retry.stdout).replacementWorkerIDs, replacementWorkerIDs);
  await writeFile(join(fixture, 'plan.json'), JSON.stringify({ ...releasePlan, releaseId: 'candidate-two' }));
  const next = JSON.parse((await promisify(execFile)(process.execPath, ['tasks-release.js', 'plan.json'], options)).stdout);
  assert.equal(next.ok, true);
  assert.notDeepEqual(next.replacementWorkerIDs, replacementWorkerIDs);
  assert.deepEqual((await pool.query('SELECT worker_id::text FROM gregale_mcp_task_workers WHERE namespace=$1', [namespace])).rows.map(row => row.worker_id), next.replacementWorkerIDs);
  const receipt = JSON.parse(await (await import('node:fs/promises')).readFile(releasePlan.checkpointPath, 'utf8'));
  assert.equal(receipt.completed, true);
  assert.equal(receipt.releaseID, 'candidate-two');
  await writeFile(join(fixture, 'plan.json'), JSON.stringify({ ...releasePlan, releaseId: undefined }));
  await assert.rejects(promisify(execFile)(process.execPath, ['tasks-release.js', 'plan.json'], options), error => JSON.parse(error.stdout).stage === 'configuration');
  await writeFile(join(fixture, 'plan.json'), JSON.stringify({ timeoutMs: 1000, start: [process.execPath, '-e', "console.log('private-hook-output'); setInterval(() => {}, 1000)"], drain: [process.execPath, '-e', "throw new Error('must not drain')"] }));
  await assert.rejects(promisify(execFile)(process.execPath, ['tasks-release.js', 'plan.json'], { cwd: fixture, timeout: 15000, env: { ...process.env, RELEASE_DATABASE: url.href, RELEASE_OWNER: ownerKey, MCP_TASK_NAMESPACE: namespace, MCP_TASK_MIGRATION_DATABASE_URL: url.href } }), error => {
    assert.equal(error.code, 1);
    assert.deepEqual(JSON.parse(error.stdout), { ok: false, stage: 'start_replacements', executorSettled: true });
    assert.equal(error.stdout.includes('private-hook-output'), false);
    return true;
  });
});

test('release checkpoint resumes after drain failure without treating replacements as old workers', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const inventory = [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }];
  const previous = randomUUID(), replacement = randomUUID();
  await store.workerHeartbeat(previous, inventory);
  let checkpoint;
  let starts = 0;
  const base = { pool, namespace, store, handlers, timeoutMs: 1000, migrate: async () => {}, saveCheckpoint: async value => { checkpoint = value; }, start: async input => {
    starts++;
    assert.deepEqual(input.previousWorkerIDs, [previous]);
    await store.workerHeartbeat(replacement, inventory);
    return [replacement];
  } };
  const interrupted = await releaseMcpTasks({ ...base, drain: async () => { throw new Error('interrupted drain'); } });
  assert.deepEqual(interrupted, { ok: false, stage: 'drain_previous' });
  assert.equal(checkpoint.namespace, namespace);
  const originalTimestamp = checkpoint.startedAt;
  const resumed = await releaseMcpTasks({ ...base, checkpoint, drain: async ({ previousWorkerIDs }) => {
    assert.deepEqual(previousWorkerIDs, [previous]);
    await store.workerStopped(previous);
  } });
  assert.equal(resumed.ok, true);
  assert.equal(starts, 1, 'resuming must reuse recorded replacement workers');
  assert.equal(checkpoint.completed, true);
  assert.equal(checkpoint.startedAt, originalTimestamp);
  const mismatch = await releaseMcpTasks({ ...base, checkpoint: { ...checkpoint, namespace: 'other-namespace' }, drain: async () => assert.fail('must not drain') });
  assert.equal(mismatch.ok, false);
});

test('completed checkpoints distinguish retries from a new rollout', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const inventory = [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }];
  const original = randomUUID(), first = randomUUID(), second = randomUUID();
  await store.workerHeartbeat(original, inventory);
  let checkpoint, starts = 0, drains = [];
  const base = { pool, namespace, store, handlers, timeoutMs: 1000, migrate: async () => {},
    saveCheckpoint: async value => { checkpoint = value; },
    start: async ({ releaseID }) => {
      starts++;
      const id = releaseID === 'release-one' ? first : second;
      await store.workerHeartbeat(id, inventory);
      return [id];
    },
    drain: async ({ previousWorkerIDs }) => {
      drains.push(previousWorkerIDs);
      for (const id of previousWorkerIDs) await store.workerStopped(id);
    },
  };
  assert.equal((await releaseMcpTasks({ ...base, releaseID: 'release-one' })).ok, true);
  assert.equal(checkpoint.version, 1);
  assert.equal(checkpoint.completed, true);
  assert.deepEqual(checkpoint.replacementWorkerIDs, [first]);
  const retry = await releaseMcpTasks({ ...base, checkpoint, releaseID: 'release-one' });
  assert.deepEqual(retry, { ok: true, stage: 'complete', replacementWorkerIDs: [first] });
  assert.equal(starts, 1);
  assert.equal(drains.length, 1);
  assert.equal((await releaseMcpTasks({ ...base, checkpoint, releaseID: 'release-two' })).ok, true);
  assert.equal(starts, 2);
  assert.deepEqual(drains, [[original], [first]]);
  assert.deepEqual((await pool.query('SELECT worker_id::text FROM gregale_mcp_task_workers WHERE namespace=$1', [namespace])).rows, [{ worker_id: second }]);
  // Completed receipt reuse cannot turn a dead replacement into a healthy release.
  await store.workerStopped(second);
  assert.equal((await releaseMcpTasks({ ...base, checkpoint, releaseID: 'release-two' })).ok, false);
  assert.equal(starts, 2);
});

test('checkpoint failures preserve release identity and replacement recovery', postgresOnly, async t => {
  const { releaseMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const inventory = [{ name: 'build_report', version: '1' }, { name: 'build_report', version: '2' }];
  let checkpoint, starts = 0, failCompletion = true;
  const id = randomUUID();
  const base = { pool, namespace, store, handlers, releaseID: 'recoverable', timeoutMs: 1000, migrate: async () => {},
    saveCheckpoint: async value => { if (value.completed && failCompletion) throw new Error('disk unavailable'); checkpoint = value; },
    start: async () => { starts++; await store.workerHeartbeat(id, inventory); return [id]; }, drain: async () => {},
  };
  assert.deepEqual(await releaseMcpTasks(base), { ok: false, stage: 'checkpoint_complete' });
  assert.deepEqual(checkpoint.replacementWorkerIDs, [id]);
  assert.equal(checkpoint.completed, undefined);
  assert.deepEqual(await releaseMcpTasks({ ...base, checkpoint, releaseID: 'another-release', migrate: async () => assert.fail('must not migrate') }), { ok: false, stage: 'checkpoint' });
  failCompletion = false;
  assert.equal((await releaseMcpTasks({ ...base, checkpoint })).ok, true);
  assert.equal(starts, 1);
  for (const invalid of [
    { namespace, previousWorkerIDs: [], startedAt: checkpoint.startedAt },
    { ...checkpoint, version: 99 },
    { ...checkpoint, previousWorkerIDs: ['invalid'] },
    { ...checkpoint, startedAt: 'invalid' },
    { ...checkpoint, replacementWorkerIDs: undefined },
  ]) assert.deepEqual(await releaseMcpTasks({ ...base, checkpoint: invalid, migrate: async () => assert.fail('must not migrate') }), { ok: false, stage: 'checkpoint' });
});

test('native replacement recheck rejects stale, draining and unsupported registrations', postgresOnly, async t => {
  const { checkMcpTaskReplacementReadiness } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '1', execute: async () => ({}) } };
  const id = randomUUID();
  const check = () => checkMcpTaskReplacementReadiness({ pool, namespace, handlers, workerIDs: [id] });
  assert.equal(await check(), false);
  await store.workerHeartbeat(id, [{ name: 'build_report', version: '1' }]);
  assert.equal(await check(), true);
  await store.workerDraining(id);
  assert.equal(await check(), false);
  await store.workerHeartbeat(id, [{ name: 'build_report', version: '2' }]);
  assert.equal(await check(), false);
  await store.workerHeartbeat(id, [{ name: 'build_report', version: '1' }]);
  await pool.query("UPDATE gregale_mcp_task_workers SET heartbeat_at=clock_timestamp()-interval '50 seconds' WHERE namespace=$1 AND worker_id=$2", [namespace, id]);
  assert.equal(await check(), false);
});

test('restore gate serializes with releases, checks retained and admitted handlers, and sanitizes hook failure', postgresOnly, async t => {
  const { restoreMcpTasks } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const handlers = { build_report: { version: '2', previousVersions: { '1': async () => ({}) }, execute: async () => ({}) } };
  const denied = await restoreMcpTasks({ pool, namespace, handlers: { build_report: { version: '1', execute: async () => ({}) } }, restore: async () => { throw new Error('must not restore'); } });
  assert.deepEqual(denied, { ok: false, stage: 'restore_compatibility' });
  await create(store, 'restore-owner');
  let restored = 0;
  const base = { pool, namespace, handlers, restore: async () => { restored++; } };
  const held = await pool.connect();
  try {
    await held.query('SELECT pg_advisory_lock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
    const busy = await restoreMcpTasks(base);
    assert.deepEqual(busy, { ok: false, stage: 'release_lock' });
    assert.equal(restored, 0);
  } finally {
    await held.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [`gregale_mcp_release:${namespace}`]);
    held.release();
  }
  const missing = await restoreMcpTasks({ ...base, handlers: { build_report: { version: '2', execute: async () => ({}) } } });
  assert.deepEqual(missing, { ok: false, stage: 'restore_compatibility' });
  assert.equal(restored, 0);
  const failed = await restoreMcpTasks({ ...base, restore: async () => { throw new Error('postgres://credential'); } });
  assert.deepEqual(failed, { ok: false, stage: 'restore_web' });
  const success = await restoreMcpTasks(base);
  assert.deepEqual(success, { ok: true, stage: 'web_restored' });
  assert.equal(restored, 1);
  assert.equal((await store.get({ taskID: (await pool.query('SELECT task_id FROM gregale_mcp_tasks WHERE namespace=$1', [namespace])).rows[0].task_id, authInfo: principal('restore-owner'), authMode: 'external-oauth' })).status, 'queued');
});

test('quarantine fences new claims across heartbeats while preserving active leases and healthy capacity', postgresOnly, async t => {
  const { quarantineMcpTaskWorkers } = await import('../task-release.js');
  const { pool, namespace, store } = await harness(t);
  const missing = randomUUID();
  await quarantineMcpTaskWorkers({ pool, namespace, workerIDs: [missing] });
  await store.workerHeartbeat(missing, [{ name: 'build_report', version: '1' }]);
  assert.equal(await store.claim(3, 30000, [{ name: 'build_report', version: '1' }], missing), null);
  const bad = randomUUID(), healthy = randomUUID();
  const inventory = [{ name: 'build_report', version: '1' }];
  await store.workerHeartbeat(bad, inventory);
  await store.workerHeartbeat(healthy, inventory);
  const first = await create(store, 'quarantine-owner');
  const active = await store.claim(3, 30000, inventory, bad);
  assert.equal(active.task_id, first.task_id);
  await create(store, 'quarantine-owner');
  await quarantineMcpTaskWorkers({ pool, namespace, workerIDs: [bad] });
  await pool.query("UPDATE gregale_mcp_task_workers SET expires_at=clock_timestamp()-interval '1 second' WHERE namespace=$1 AND worker_id=$2", [namespace, bad]);
  await store.cleanupExpired();
  await store.workerHeartbeat(bad, inventory);
  assert.equal(await store.claim(3, 30000, inventory, bad), null);
  const row = (await pool.query('SELECT lease_token::text, status FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2', [namespace, first.task_id])).rows[0];
  assert.equal(row.status, 'running');
  assert.equal(row.lease_token, active.lease_token);
  assert.ok(await store.claim(3, 30000, inventory, healthy));
  assert.equal(await store.claim(3, 30000, inventory, randomUUID()), null);
  await quarantineMcpTaskWorkers({ pool, namespace, workerIDs: [bad] });
  assert.equal((await pool.query('SELECT draining FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=$2', [namespace, bad])).rows[0].draining, true);
});

for (const { title, useReceipt, receiptInbox, supervisor, supervisorSubmissionFault, deferredForm, submissionReplyFault, formReplyFault, reviewedInput, respondOnce, cancelReplyFault, timeout } of [
  { title: 'keyed Task submission recovers the committed handle after a lost CLI reply', timeout: 75_000 },
  { title: 'Task receipt submission retry creates one Task after a lost request before commit', useReceipt: true, submissionReplyFault: 'before', timeout: 35_000 },
  { title: 'Task receipt submission retry returns the original Task after a lost reply after commit', useReceipt: true, submissionReplyFault: 'after', timeout: 35_000 },
  { title: 'private Task receipt resumes waiting after the CLI dies and the retry window closes', useReceipt: true, timeout: 90_000 },
  { title: 'private Task receipt resumes deferred form input after the submitting CLI dies', useReceipt: true, deferredForm: true, timeout: 35_000 },
  { title: 'receipt directory recovers the original Tasks after the submitting CLI dies', useReceipt: true, receiptInbox: true, deferredForm: true, timeout: 35_000 },
  { title: 'Task supervisor restarts after submission and approval and saves one result', useReceipt: true, supervisor: true, deferredForm: true, formReplyFault: 'after', timeout: 45_000 },
  { title: 'Task supervisor recovers an interrupted submission before commit and saves one result', useReceipt: true, supervisor: true, deferredForm: true, supervisorSubmissionFault: 'before', timeout: 45_000 },
  { title: 'Task supervisor recovers a lost submission reply after commit and saves one result', useReceipt: true, supervisor: true, deferredForm: true, supervisorSubmissionFault: 'after', timeout: 45_000 },
  { title: 'receipt wait recovers a form reply lost after commit', useReceipt: true, deferredForm: true, formReplyFault: 'after', timeout: 35_000 },
  { title: 'receipt wait reports pending input for a form reply lost before commit', useReceipt: true, deferredForm: true, formReplyFault: 'before', timeout: 35_000 },
  { title: 'reviewed Task input stays bound to the original Task and form after restart', useReceipt: true, deferredForm: true, reviewedInput: true, timeout: 45_000 },
  { title: 'receipt watch hands off to reviewed approval and resumes to the original result', useReceipt: true, deferredForm: true, respondOnce: true, timeout: 45_000 },
  { title: 'receipt respond verifies a form reply lost after commit', useReceipt: true, deferredForm: true, respondOnce: true, formReplyFault: 'after', timeout: 45_000 },
  { title: 'receipt respond reports pending input for a form reply lost before commit', useReceipt: true, deferredForm: true, respondOnce: true, formReplyFault: 'before', timeout: 45_000 },
  { title: 'receipt cancel recovers cancellation committed before the reply was lost', useReceipt: true, deferredForm: true, cancelReplyFault: 'after', timeout: 35_000 },
  { title: 'receipt cancel reports pending state without repeating a lost cancellation', useReceipt: true, deferredForm: true, cancelReplyFault: 'before', timeout: 35_000 },
]) test(title, {
  skip: postgresOnly.skip || (!process.env.MCP_TASKS_TEST_GREGALE && 'set MCP_TASKS_TEST_GREGALE to the Gregale CLI binary'),
  timeout,
}, async t => {
  const { execFile } = await import('node:child_process');
  const { mkdir, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } = await import('node:fs/promises');
  const { createServer } = await import('node:http');
  const { tmpdir } = await import('node:os');
  const { resolve } = await import('node:path');
  const { promisify } = await import('node:util');
  const { generateKeyPair, SignJWT } = await import('jose');
  const binary = resolve(process.env.MCP_TASKS_TEST_GREGALE);
  const run = promisify(execFile);
  const clientDir = await mkdtemp(join(tmpdir(), 'gregale-task-submission-'));
  t.after(() => rm(clientDir, { recursive: true, force: true }));
  const supervisorDir = join(clientDir, 'work');
  const receiptDir = supervisor ? join(supervisorDir, 'receipts') : receiptInbox ? join(clientDir, 'receipts') : clientDir;
  if (receiptInbox) await mkdir(receiptDir, { mode: 0o700 });
  const receiptPath = join(receiptDir, 'task-receipt.json');
  const { store, pool, namespace } = await harness(t, { ttlMs: 120_000 });
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const mint = (subject, scope = 'mcp:tools reports:read') => new SignJWT({ scope }).setJti(randomUUID()).setProtectedHeader({ alg: 'ES256' })
    .setAudience(auth.resource).setIssuer(auth.issuer).setSubject(subject).setExpirationTime('5m').sign(privateKey);
  const owner = await mint('alice');
  const foreign = await mint('bob');
  const ownerWithoutTool = await mint('alice', 'mcp:tools');
  const makeKey = (time = Date.now()) => {
    const stamp = time.toString(16).padStart(12, '0');
    return `${stamp.slice(0, 8)}-${stamp.slice(8)}-7${randomUUID().slice(15)}`;
  };
  const report = 'Original private report';
  const watchGates = respondOnce && !formReplyFault ? Array.from({ length: 2 }, () => Promise.withResolvers()) : [];
  const finishSupervisorReport = Promise.withResolvers();
  let executions = 0;
  let subscriptions = 0;
  const buildReport = mcpTaskHandlers.build_report;
  let handlers = deferredForm ? mcpTaskHandlers : {
    build_report: { version: '1', async execute() { return { content: [{ type: 'text', text: report }] }; } },
  };
  if (supervisor || watchGates.length) {
    handlers = { ...handlers, build_report: { ...buildReport, async execute(args, context) {
      if (args.report === 'original') {
        if (supervisor) {
          if (executions++ > 0) await finishSupervisorReport.promise;
        } else {
          // Pause the initial execution and its replay until each watch subscribes.
          const gate = watchGates[executions++];
          if (gate) await gate.promise;
        }
      }
      return buildReport.execute(args, context);
    } } };
  }
  const runtime = createMcpTaskRuntime({ store, pollIntervalMs: 500, handlers });
  await runtime.start();
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth,
  }, { taskRuntime: runtime, keyResolver: publicKey, log() {} });
  const rows = async () => (await pool.query(`SELECT task_id::text, status, attempt_count,
    result_encrypted IS NOT NULL AS has_result FROM gregale_mcp_tasks WHERE namespace=$1`, [namespace])).rows;
  let dropFirstReply = true, committedID, proxyError, submittingCLI;
  let respondingSupervisor;
  let pendingFormReplyFault = formReplyFault, pendingCancelReplyFault = cancelReplyFault;
  const requests = [];
  async function killCLI(child) {
    const exited = once(child, 'exit');
    assert.ok(child.kill('SIGKILL'));
    const [, signal] = await exited;
    assert.equal(signal, 'SIGKILL');
  }
  const server = createServer((req, res) => {
    // The relay commits the request through the real handler. Count only
    // updates and cancellations sent by the CLI when checking for repeats.
    if (req.headers['x-gregale-test-task-relay'] === '1') return api.app(req, res);
    const method = req.headers['mcp-method'];
    requests.push(method);
    if (method === 'subscriptions/listen' && watchGates.length) watchGates[subscriptions++]?.resolve();
    if (method === 'subscriptions/listen' && supervisor && requests.includes('tasks/update')) {
      finishSupervisorReport.resolve();
    }
    const dropSubmission = dropFirstReply && method === 'gregale/tasks/submit';
    const dropFormReply = method === 'tasks/update' && pendingFormReplyFault;
    const dropCancelReply = method === 'tasks/cancel' && pendingCancelReplyFault;
    if (!dropSubmission && !dropFormReply && !dropCancelReply) return api.app(req, res);
    if (dropSubmission) dropFirstReply = false;
    if (dropFormReply) pendingFormReplyFault = undefined;
    if (dropCancelReply) pendingCancelReplyFault = undefined;
    if (dropFormReply === 'before' || dropCancelReply === 'before') {
      req.destroy();
      return;
    }
    void (async () => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
      const body = Buffer.concat(chunks);
      if (dropSubmission && useReceipt) {
        const { _meta: metadata, arguments: args, name: tool } = JSON.parse(body).params;
        const key = metadata['io.gregale/task-submission-key'];
        assert.match(key, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
        assert.deepEqual(metadata['io.modelcontextprotocol/clientCapabilities'].elicitation,
          deferredForm ? { form: {} } : undefined);
        assert.equal((await stat(receiptPath)).mode & 0o777, 0o600);
        const endpointSha256 = createHash('sha256').update(endpoint).digest('hex');
        const requestSha256 = createHash('sha256').update(JSON.stringify({
          arguments: args, deferFormInput: Boolean(deferredForm), tool,
        })).digest('hex');
        const proof = createHmac('sha256', owner).update(`gregale/mcp-task-receipt/v3:${endpointSha256}${key}${requestSha256}`).digest('hex');
        assert.deepEqual(JSON.parse(await readFile(receiptPath, 'utf8')), {
          version: 3, endpointSha256, submissionKey: key, requestSha256, credentialProof: proof,
        });
        if (submissionReplyFault === 'before' || supervisorSubmissionFault === 'before') {
          assert.deepEqual(await rows(), []);
          await killCLI(submittingCLI);
          return;
        }
      }
      const headers = dropFormReply || dropCancelReply ? { ...req.headers, 'x-gregale-test-task-relay': '1' } : req.headers;
      const reply = await fetch(endpoint, {
        method: 'POST', headers, body, signal: AbortSignal.timeout(5000),
      });
      assert.equal(reply.status, 200);
      await reply.arrayBuffer();
      if (dropCancelReply === 'after') {
        assert.deepEqual(await rows(), [{ task_id: committedID, status: 'cancelled', attempt_count: 1, has_result: false }]);
        return;
      }
      if (dropFormReply === 'after') {
        const task = (await rows()).find(row => row.task_id === committedID);
        assert.ok(task && ['queued', 'running', 'completed'].includes(task.status), 'form update did not commit');
        if (supervisor) await killCLI(respondingSupervisor);
        return;
      }
      const persisted = await rows();
      assert.equal(persisted.length, 1);
      committedID = persisted[0].task_id;
      if (useReceipt) await killCLI(submittingCLI);
    })().catch(error => { proxyError = error; }).finally(() => res.destroy());
  });
  try {
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
    function startCLI(verb, credential, args) {
      return run(binary, ['mcp', 'client', verb, '--url', endpoint,
        '--token-env', 'GREGALE_JOURNEY_TOKEN', ...(verb === 'watch' ? [] : ['--timeout', '6s']), '--json', ...args], {
        cwd: clientDir, env: { XDG_CONFIG_HOME: clientDir, GREGALE_JOURNEY_TOKEN: credential }, timeout: 8000,
      });
    }
    async function cli(verb, credential, expectedCode, args, expectedError) {
      const response = await startCLI(verb, credential, args)
        .then(({ stdout, stderr }) => ({ stdout, stderr, code: 0 }), ({ stdout, stderr, code }) => ({ stdout, stderr, code }));
      assert.ok(!response.stdout.includes(credential) && !response.stderr.includes(credential), 'CLI leaked a caller token');
      assert.equal(response.code, expectedCode, `unexpected exit from mcp client ${verb}: ${response.stderr}`);
      if (expectedError) assert.match(response.stderr, expectedError);
      return response.stdout;
    }
    function startSupervisor(command, args = []) {
      const example = fileURLToPath(new URL('../examples/task-supervisor.mjs', import.meta.url));
      return run(process.execPath, [example, command, '--url', endpoint, '--dir', supervisorDir,
        '--token-env', 'GREGALE_JOURNEY_TOKEN', ...args], {
        cwd: clientDir, env: { XDG_CONFIG_HOME: clientDir, GREGALE_JOURNEY_TOKEN: owner, GREGALE_BIN: binary },
        timeout: 12_000,
      });
    }
    async function runSupervisor(command = 'run', args = []) {
      const { stdout, stderr } = await startSupervisor(command, args);
      assert.ok(!stdout.includes(owner) && !stderr.includes(owner), 'supervisor leaked a caller token');
      return stdout.trim().split('\n').map(line => JSON.parse(line));
    }
    // Use a real, already aged UUID so the test crosses the replay deadline
    // without changing either the stored key or the database clock.
    const issuedAt = Date.now() - 30_000;
    const key = makeKey(issuedAt);
    const submission = (credential, expectedCode, value = 'original', submissionKey = key) => cli('call', credential, expectedCode, [
      '--tool', 'build_report', '--tasks', '--task-submission-key', submissionKey,
      '--arguments', JSON.stringify({ report: value, steps: 1 }),
    ]);
    const receiptArgs = deferredForm ? { report: 'original', steps: 1, request_approval: true } : { report: 'original', steps: 1 };
    const receiptCallFor = (path, args) => ['--tool', 'build_report', '--tasks', '--task-receipt', path,
      '--arguments', JSON.stringify(args), ...(deferredForm ? ['--defer-form-input'] : [])];
    const receiptCall = receiptCallFor(receiptPath, receiptArgs);
    const jobFile = join(clientDir, 'report-job.json');
    if (supervisorSubmissionFault) await writeFile(jobFile, JSON.stringify(receiptArgs), { mode: 0o600, flag: 'wx' });
    if (useReceipt) {
      const reportArgs = supervisorSubmissionFault ? ['--job-file', jobFile] : ['--report', 'original'];
      const pending = supervisor ? startSupervisor('submit', [...reportArgs, '--receipt', 'task-receipt.json']) :
        startCLI('call', owner, receiptCall);
      submittingCLI = pending.child;
      await assert.rejects(pending, error => error.signal === 'SIGKILL' && error.stdout === '');
    } else {
      assert.equal(await submission(owner, 3), '');
    }
    assert.ifError(proxyError);
    if (submissionReplyFault) {
      const initialRows = await rows();
      if (submissionReplyFault === 'before') {
        assert.deepEqual(initialRows, []);
      } else {
        assert.ok(committedID);
        assert.equal(initialRows[0].task_id, committedID);
        assert.equal(initialRows.length, 1);
      }
      const receiptBody = await readFile(receiptPath, 'utf8');
      const receiptFile = await stat(receiptPath);
      const { endpointSha256, submissionKey, requestSha256 } = JSON.parse(receiptBody);
      const legacyPaths = [
        join(clientDir, 'v1-receipt.json'),
        join(clientDir, 'v2-receipt.json'),
      ];
      await writeFile(legacyPaths[0], JSON.stringify({ version: 1, endpointSha256, submissionKey }), { mode: 0o600, flag: 'wx' });
      await writeFile(legacyPaths[1], JSON.stringify({ version: 2, endpointSha256, submissionKey, requestSha256 }),
        { mode: 0o600, flag: 'wx' });
      const renewedOwner = await mint('alice');
      const retry = [...receiptCallFor(receiptPath, { steps: 1, report: 'original' }), '--retry-submission'];
      const beforeDenials = requests.length;
      for (const credential of [foreign, renewedOwner]) {
        assert.equal(await cli('call', credential, 1, retry, /requires the original client credential/), '');
      }
      for (const args of [
        [...receiptCallFor(receiptPath, { ...receiptArgs, report: 'changed' }), '--retry-submission'],
        [...receiptCall, '--defer-form-input', '--retry-submission'],
        [...receiptCall, '--tool', 'different_tool', '--retry-submission'],
      ]) {
        assert.equal(await cli('call', owner, 1, args, /request does not match the receipt/), '');
      }
      assert.equal(await cli('call', owner, 1, [...receiptCall, '--url', `${endpoint}/other`, '--retry-submission'],
        /receipt does not match the selected endpoint/), '');
      for (const legacyPath of legacyPaths) {
        assert.equal(await cli('call', owner, 1, [...receiptCallFor(legacyPath, receiptArgs), '--retry-submission'],
          /receipt lacks a credential binding/), '');
      }
      assert.equal(await cli('call', owner, 1, ['--tasks', '--tool', 'build_report', '--retry-submission'],
        /--retry-submission requires --task-receipt/), '');
      assert.equal(requests.length, beforeDenials);
      assert.deepEqual((await rows()).map(row => row.task_id), initialRows.map(row => row.task_id));
      const beforeRetry = requests.length;
      const { result } = JSON.parse(await cli('call', owner, 0, retry));
      assert.equal(result.resultType, 'task');
      assert.equal(requests.slice(beforeRetry).filter(method => method === 'gregale/tasks/submit').length, 1);
      if (committedID) assert.equal(result.taskId, committedID);
      const stored = await pool.query('SELECT task_id::text, submission_key::text FROM gregale_mcp_tasks WHERE namespace=$1', [namespace]);
      assert.deepEqual(stored.rows, [{ task_id: result.taskId, submission_key: submissionKey }]);
      for (const path of [receiptPath, ...legacyPaths]) {
        const status = JSON.parse(await cli('status', renewedOwner, 0, ['--task-receipt', path, '--metadata-only']));
        assert.equal(status.taskId, result.taskId);
      }
      assert.equal(await readFile(receiptPath, 'utf8'), receiptBody);
      assert.equal((await stat(receiptPath)).ino, receiptFile.ino);
      return;
    }
    const selector = useReceipt ? ['--task-receipt', receiptPath] : ['--task-submission-key', key];
    if (supervisor) {
      const receipt = 'task-receipt.json';
      const reviewFile = join('reviews', receipt);
      const resultFile = join('results', receipt);
      const reviewRequired = [{ receipt, action: 'review_required', file: reviewFile }];
      const receiptBody = await readFile(receiptPath, 'utf8');
      const receiptFile = await stat(receiptPath);
      if (supervisorSubmissionFault) {
        if (supervisorSubmissionFault === 'before') {
          assert.deepEqual(await rows(), []);
          assert.deepEqual(await runSupervisor(), [{ receipt, action: 'unavailable' }]);
          const changedJob = join(clientDir, 'changed-job.json');
          await writeFile(changedJob, JSON.stringify({ ...receiptArgs, report: 'changed' }), { mode: 0o600, flag: 'wx' });
          const beforeChanged = requests.length;
          assert.deepEqual(await runSupervisor('recover', ['--receipt', receipt, '--job-file', changedJob]),
            [{ receipt, action: 'submission_unresolved' }]);
          assert.deepEqual(requests.slice(beforeChanged), ['server/discover', 'gregale/tasks/lookup']);
          assert.deepEqual(await rows(), []);
        } else {
          assert.ok(committedID, 'the first reply must be dropped after PostgreSQL commits');
        }
        const beforeRecovery = requests.length;
        assert.deepEqual(await runSupervisor('recover', ['--receipt', receipt, '--job-file', jobFile]), reviewRequired);
        assert.equal(requests.slice(beforeRecovery).filter(method => method === 'gregale/tasks/submit').length, 1);
        const persisted = await pool.query('SELECT task_id::text, submission_key::text FROM gregale_mcp_tasks WHERE namespace=$1',
          [namespace]);
        assert.equal(persisted.rows.length, 1);
        const task = persisted.rows[0];
        if (committedID) assert.equal(task.task_id, committedID);
        assert.equal(task.submission_key, JSON.parse(receiptBody).submissionKey);
        committedID = task.task_id;
        assert.equal(await readFile(receiptPath, 'utf8'), receiptBody);
        assert.equal((await stat(receiptPath)).ino, receiptFile.ino);
      } else {
        assert.ok(committedID, 'the first reply must be dropped after PostgreSQL commits');
        await eventually(async () => (await rows())[0]?.status === 'input_required', 'worker did not persist the review form');
        assert.deepEqual(await runSupervisor(), reviewRequired);
      }

      assert.equal(requests.filter(method => method === 'tasks/update').length, 0);
      const reviewPath = join(supervisorDir, reviewFile);
      const template = JSON.parse(await readFile(reviewPath, 'utf8'));
      assert.equal((await stat(reviewPath)).mode & 0o777, 0o600);
      assert.equal(template.taskId, committedID);
      assert.equal(template.forms.approval.response, null);
      template.forms.approval.response = { action: 'accept', content: { approved: true } };
      await writeFile(reviewPath, JSON.stringify(template));

      if (formReplyFault) {
        const pending = startSupervisor('run');
        respondingSupervisor = pending.child;
        await assert.rejects(pending, error => error.signal === 'SIGKILL' && error.stdout === '');
        assert.ifError(proxyError);
      }
      const beforeResume = requests.length;
      assert.deepEqual(await runSupervisor(), [{ receipt, action: 'result_saved', file: resultFile }]);
      assert.ok(requests.slice(beforeResume).includes('subscriptions/listen'));
      assert.equal(requests.filter(method => method === 'tasks/update').length, 1);
      assert.equal(requests.filter(method => method === 'gregale/tasks/submit').length,
        supervisorSubmissionFault === 'after' ? 3 : 2, 'submission attempts include the relay when the initial call commits');
      const destination = join(supervisorDir, resultFile);
      const before = await stat(destination);
      const saved = await readFile(destination);
      assert.equal(before.mode & 0o777, 0o600);
      const completed = JSON.parse(saved);
      assert.equal(completed.taskId, committedID);
      assert.equal(completed.status, 'completed');
      assert.equal(completed.result.content[0].text, 'Report original is ready after 1 steps.');

      const beforeReplay = requests.length;
      assert.deepEqual(await runSupervisor(), [{ receipt, action: 'already_saved', file: resultFile }]);
      assert.deepEqual(requests.slice(beforeReplay), ['server/discover', 'gregale/tasks/lookup']);
      assert.equal((await stat(destination)).ino, before.ino);
      assert.deepEqual(await readFile(destination), saved);
      assert.deepEqual(await readdir(join(supervisorDir, 'results')), [receipt]);
      const completedRows = [{ task_id: committedID, status: 'completed', attempt_count: 1, has_result: true }];
      assert.deepEqual(await rows(), completedRows);
      if (supervisorSubmissionFault === 'before') {
        const expiredReceipt = 'expired-recovery.json';
        await writeFile(join(receiptDir, expiredReceipt), JSON.stringify({
          ...JSON.parse(receiptBody), submissionKey: makeKey(Date.now() - 61_000),
        }), { mode: 0o600, flag: 'wx' });
        const beforeExpiry = requests.length;
        assert.deepEqual(await runSupervisor('recover', ['--receipt', expiredReceipt, '--job-file', jobFile]),
          [{ receipt: expiredReceipt, action: 'submission_unresolved' }]);
        assert.deepEqual(requests.slice(beforeExpiry), ['server/discover', 'gregale/tasks/lookup']);
        assert.deepEqual(await rows(), completedRows);
      }
      return;
    }
    assert.ok(committedID, 'the first CLI reply must be dropped after a confirmed PostgreSQL commit');
    if (receiptInbox) {
      async function submitInboxReceipt(name, credential) {
        const { result } = JSON.parse(await cli('call', credential, 0,
          receiptCallFor(join(receiptDir, name), { report: name, steps: 1 })));
        return result.taskId;
      }
      const readyName = 'ready report.receipt';
      const readyID = await submitInboxReceipt(readyName, owner);
      const foreignID = await submitInboxReceipt('foreign.json', foreign);
      const expiredID = await submitInboxReceipt('expired.json', owner);
      await eventually(async () => {
        const states = new Map((await rows()).map(row => [row.task_id, row.status]));
        return states.get(committedID) === 'input_required' &&
          [readyID, foreignID, expiredID].every(id => states.get(id) === 'completed');
      }, 'worker did not persist the expected inbox states');
      await pool.query('UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2', [namespace, expiredID]);

      const statusArgs = ['--metadata-only', '--receipt-dir', receiptDir];
      const subdir = join(receiptDir, 'archive');
      await mkdir(subdir, { mode: 0o700 });
      const beforeEmpty = requests.length;
      assert.deepEqual(JSON.parse(await cli('status', owner, 0, ['--metadata-only', '--receipt-dir', subdir])), []);
      assert.equal(requests.length, beforeEmpty);

      const original = JSON.parse(await readFile(receiptPath, 'utf8'));
      const privateFile = { mode: 0o600, flag: 'wx' };
      await writeFile(join(subdir, 'nested.receipt'), JSON.stringify(original), privateFile);
      await writeFile(join(receiptDir, `.gregale-submission-${randomUUID()}`), JSON.stringify(original), privateFile);
      await writeFile(join(receiptDir, 'bad.json'), 'private malformed receipt', privateFile);
      await writeFile(join(receiptDir, 'wrong-endpoint.json'), JSON.stringify({
        ...original, endpointSha256: createHash('sha256').update(`${endpoint}/other`).digest('hex'),
      }), privateFile);
      await symlink(receiptPath, join(receiptDir, 'symlink.json'));

      const beforeInvalid = requests.length;
      assert.equal(await cli('status', owner, 1, [...statusArgs, ...selector]), '');
      assert.equal(await cli('status', owner, 1, [...statusArgs, '--read-result']), '');
      assert.equal(await cli('status', owner, 1, ['--receipt-dir', receiptDir]), '');
      assert.equal(requests.length, beforeInvalid);

      const persisted = await rows();
      const beforeScan = requests.length;
      const inbox = JSON.parse(await cli('status', owner, 1, statusArgs));
      assert.deepEqual(requests.slice(beforeScan), ['server/discover', ...Array(4).fill('gregale/tasks/lookup')]);
      const ready = inbox[3].task, paused = inbox[5].task;
      for (const task of [ready, paused]) assert.ok(Number.isFinite(Date.parse(task.lastUpdatedAt)));
      assert.deepEqual(inbox, [
        { receipt: 'bad.json', outcome: 'invalid_receipt' },
        { receipt: 'expired.json', outcome: 'unavailable' },
        { receipt: 'foreign.json', outcome: 'unavailable' },
        { receipt: readyName, outcome: 'available', task: { taskId: readyID, status: 'completed', lastUpdatedAt: ready.lastUpdatedAt, nextAction: 'read_result' } },
        { receipt: 'symlink.json', outcome: 'invalid_receipt' },
        { receipt: 'task-receipt.json', outcome: 'available', task: { taskId: committedID, status: 'input_required', lastUpdatedAt: paused.lastUpdatedAt, nextAction: 'provide_input' } },
        { receipt: 'wrong-endpoint.json', outcome: 'invalid_receipt' },
      ]);
      const nested = JSON.parse(await cli('status', owner, 0, ['--metadata-only', '--receipt-dir', subdir]));
      assert.deepEqual(nested, [{ receipt: 'nested.receipt', outcome: 'available', task: paused }]);
      assert.deepEqual(await rows(), persisted);
      return;
    }
    const lookup = (credential, code) => cli('status', credential, code, [...selector, '--metadata-only']);
    async function watch(status, nextAction) {
      await eventually(async () => (await rows()).some(row => row.task_id === committedID && row.status === 'running'),
        'worker did not reach the watch gate');
      const beforeWatch = requests.length;
      const output = await cli('watch', owner, 0, selector);
      assert.deepEqual(requests.slice(beforeWatch), ['server/discover', 'gregale/tasks/lookup', 'subscriptions/listen', 'gregale/tasks/lookup']);
      const [first, last, ...extra] = output.trim().split('\n').map(line => JSON.parse(line));
      assert.deepEqual(extra, []);
      for (const event of [first, last]) assert.ok(Number.isFinite(Date.parse(event.lastUpdatedAt)));
      assert.deepEqual(first, { taskId: committedID, status: 'working', lastUpdatedAt: first.lastUpdatedAt,
        nextAction: 'poll_status', pollIntervalMs: 500 });
      assert.deepEqual(last, { taskId: committedID, status, lastUpdatedAt: last.lastUpdatedAt, nextAction });
      return last;
    }
    if (deferredForm) {
      let watchedForm;
      if (watchGates.length) {
        for (const credential of [foreign, ownerWithoutTool]) {
          const beforeDenied = requests.length;
          assert.equal(await cli('watch', credential, 1, selector), '');
          assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
        }
        watchedForm = await watch('input_required', 'provide_input');
      }
      await eventually(async () => (await rows())[0]?.status === 'input_required', 'worker did not persist the form request');
      const snapshot = JSON.parse(await lookup(owner, 0));
      assert.ok(Number.isFinite(Date.parse(snapshot.lastUpdatedAt)));
      assert.deepEqual(snapshot, { taskId: committedID, status: 'input_required', lastUpdatedAt: snapshot.lastUpdatedAt,
        nextAction: 'provide_input' });
      if (watchedForm) assert.deepEqual(snapshot, watchedForm);
      if (cancelReplyFault) {
        const beforeInvalid = requests.length;
        assert.equal(await cli('cancel', owner, 1, [...selector, '--url', `${endpoint}/other`]), '');
        assert.equal(await cli('cancel', owner, 1, [...selector, '--task-id', committedID]), '');
        assert.equal(requests.length, beforeInvalid);
        for (const credential of [foreign, ownerWithoutTool]) {
          const beforeDenied = requests.length;
          assert.equal(await cli('cancel', credential, 1, selector), '');
          assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
        }
        const beforeLostReply = requests.length;
        const dropped = JSON.parse(await cli('cancel', owner, cancelReplyFault === 'before' ? 3 : 0, selector));
        assert.ifError(proxyError);
        assert.deepEqual(requests.slice(beforeLostReply),
          ['server/discover', 'gregale/tasks/lookup', 'tasks/cancel', 'gregale/tasks/lookup']);
        assert.equal(requests.filter(method => method === 'tasks/cancel').length, 1);
        if (cancelReplyFault === 'before') {
          assert.deepEqual(dropped, { ...snapshot, nextAction: 'retry_status' });
          assert.deepEqual(await rows(), [{ task_id: committedID, status: 'input_required', attempt_count: 1, has_result: false }]);
          const requested = JSON.parse(await cli('cancel', owner, 0, selector));
          assert.deepEqual(requested, { task_id: committedID, cancellation_requested: true });
        } else {
          assert.deepEqual(dropped, { taskId: committedID, status: 'cancelled', lastUpdatedAt: dropped.lastUpdatedAt, nextAction: 'none' });
          assert.ok(Number.isFinite(Date.parse(dropped.lastUpdatedAt)));
        }
        const beforeTerminal = requests.length;
        const terminal = JSON.parse(await cli('cancel', owner, 0, selector));
        assert.equal(terminal.taskId, committedID);
        assert.equal(terminal.status, 'cancelled');
        assert.deepEqual(requests.slice(beforeTerminal), ['server/discover', 'gregale/tasks/lookup']);
        assert.equal(requests.filter(method => method === 'tasks/cancel').length, cancelReplyFault === 'before' ? 2 : 1);
        assert.deepEqual(await rows(), [{ task_id: committedID, status: 'cancelled', attempt_count: 1, has_result: false }]);
        return;
      }
      if (reviewedInput || respondOnce) {
        const verb = respondOnce ? 'respond' : 'wait';
        if (reviewedInput) {
          const beforeWatch = requests.length;
          assert.deepEqual(JSON.parse(await cli('watch', owner, 0, selector)), snapshot);
          assert.deepEqual(requests.slice(beforeWatch), ['server/discover', 'gregale/tasks/lookup']);
        }
        const beforeRead = requests.length;
        assert.deepEqual(JSON.parse(await cli('status', owner, 0, [...selector, '--read-result'])), snapshot);
        assert.deepEqual(requests.slice(beforeRead), ['server/discover', 'gregale/tasks/lookup']);
        const file = join(clientDir, 'reviewed-input.json');
        const exportArgs = [...selector, '--write-input-template', file];
        assert.equal(await cli('status', foreign, 1, exportArgs), '');
        await assert.rejects(stat(file), { code: 'ENOENT' });
        assert.deepEqual(JSON.parse(await cli('status', owner, 0, exportArgs)), snapshot);
        const template = JSON.parse(await readFile(file, 'utf8'));
        assert.equal((await stat(file)).mode & 0o777, 0o600);
        assert.equal(template.taskId, committedID);
        assert.equal(template.endpointSha256, createHash('sha256').update(endpoint).digest('hex'));
        assert.deepEqual(Object.keys(template.forms), ['approval']);
        assert.equal(template.forms.approval.message, 'Approve generating this report?');
        assert.deepEqual(template.forms.approval.schema.required, ['approved']);
        assert.match(template.forms.approval.formSha256, /^[a-f0-9]{64}$/);
        assert.equal(template.forms.approval.response, null);
        const reviewedArgs = ['--reviewed-input-file', file];
        assert.equal(await cli(verb, owner, 1, [...selector, ...reviewedArgs]), '');

        template.forms.approval.response = { action: 'accept', content: { approved: true } };
        const filled = JSON.stringify(template);
        await writeFile(file, filled);
        assert.equal(await cli('status', owner, 1, exportArgs), '');
        assert.equal(await readFile(file, 'utf8'), filled);

        const otherReceipt = join(clientDir, 'other-task.json');
        const otherArgs = watchGates.length ? { ...receiptArgs, report: 'separate report' } : receiptArgs;
        const second = JSON.parse(await cli('call', owner, 0, receiptCallFor(otherReceipt, otherArgs)));
        const otherID = second.result.taskId;
        assert.notEqual(otherID, committedID);
        await eventually(async () => (await rows()).some(row => row.task_id === otherID && row.status === 'input_required'),
          'second Task did not persist its form request');
        const beforeWrongTask = requests.length;
        assert.equal(await cli(verb, owner, 1, ['--task-receipt', otherReceipt, ...reviewedArgs]), '');
        assert.deepEqual(requests.slice(beforeWrongTask), ['server/discover', 'gregale/tasks/lookup']);
        const beforeWrongEndpoint = requests.length;
        const endpointSelector = respondOnce ? selector : ['--task-id', committedID];
        assert.equal(await cli(verb, owner, 1, [...endpointSelector, ...reviewedArgs, '--url', `${endpoint}/other`]), '');
        if (respondOnce) {
          assert.equal(await cli(verb, owner, 1, reviewedArgs), '');
          assert.equal(await cli(verb, owner, 1, selector), '');
          assert.equal(await cli(verb, owner, 1, [...selector, ...reviewedArgs, '--task-id', committedID]), '');
        }
        assert.equal(requests.length, beforeWrongEndpoint);
        if (respondOnce) {
          for (const credential of [foreign, ownerWithoutTool]) {
            const beforeDenied = requests.length;
            assert.equal(await cli(verb, credential, 1, [...selector, ...reviewedArgs]), '');
            assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
          }
        }

        const authenticGet = runtime.get;
        runtime.get = async (...args) => {
          const task = await authenticGet(...args);
          task.inputRequests.approval.params.message = 'A different approval question';
          return task;
        };
        try {
          assert.equal(await cli(verb, owner, 1, [...selector, ...reviewedArgs]), '');
        } finally {
          runtime.get = authenticGet;
        }
        assert.equal(requests.filter(method => method === 'tasks/update').length, 0);
        assert.ok((await rows()).every(row => row.status === 'input_required' && !row.has_result));
        if (respondOnce) {
          const respondArgs = [...selector, ...reviewedArgs];
          const expectedRequests = ['server/discover', 'gregale/tasks/lookup', 'tasks/get', 'tasks/update', 'tasks/get'];
          if (formReplyFault === 'before') {
            const beforeLostReply = requests.length;
            assert.deepEqual(JSON.parse(await cli(verb, owner, 3, respondArgs)), snapshot);
            assert.deepEqual(requests.slice(beforeLostReply), expectedRequests);
            assert.ok((await rows()).every(row => row.status === 'input_required' && !row.has_result));
          }
          const beforeRespond = requests.length;
          const observed = JSON.parse(await cli(verb, owner, 0, respondArgs));
          assert.ifError(proxyError);
          assert.deepEqual(requests.slice(beforeRespond), expectedRequests);
          assert.equal(observed.taskId, committedID);
          assert.ok(Number.isFinite(Date.parse(observed.lastUpdatedAt)));
          assert.ok(['working', 'completed'].includes(observed.status));
          const next = observed.status === 'working' ? { nextAction: 'poll_status', pollIntervalMs: 500 } : { nextAction: 'read_result' };
          assert.deepEqual(observed, { taskId: committedID, status: observed.status, lastUpdatedAt: observed.lastUpdatedAt, ...next });
          if (watchGates.length) {
            assert.equal(observed.status, 'working');
            await watch('completed', 'read_result');
          } else {
            await eventually(async () => (await rows()).some(row => row.task_id === committedID && row.status === 'completed'),
              'Task did not complete after the reviewed response');
          }
          const beforeReplay = requests.length;
          const terminal = JSON.parse(await cli(verb, owner, 0, respondArgs));
          assert.deepEqual(terminal, { taskId: committedID, status: 'completed', lastUpdatedAt: terminal.lastUpdatedAt, nextAction: 'read_result' });
          assert.deepEqual(requests.slice(beforeReplay), ['server/discover', 'gregale/tasks/lookup']);
          assert.equal(await readFile(file, 'utf8'), filled);
        }
        const completed = JSON.parse(respondOnce ?
          await cli('status', owner, 0, [...selector, '--read-result']) :
          await cli('wait', owner, 0, [...selector, ...reviewedArgs]));
        assert.equal(completed.taskId, committedID);
        assert.equal(completed.result.content[0].text, 'Report original is ready after 1 steps.');
        assert.equal(requests.filter(method => method === 'tasks/update').length, formReplyFault === 'before' ? 2 : 1);
        const byID = new Map((await rows()).map(row => [row.task_id, row]));
        assert.equal(byID.size, 2);
        assert.deepEqual(byID.get(committedID), { task_id: committedID, status: 'completed', attempt_count: 1, has_result: true });
        assert.deepEqual(byID.get(otherID), { task_id: otherID, status: 'input_required', attempt_count: 1, has_result: false });
        return;
      }
      const responses = join(clientDir, 'responses.json');
      await writeFile(responses, JSON.stringify({ approval: { action: 'accept', content: { approved: true } } }),
        { flag: 'wx', mode: 0o600 });
      const waitArgs = [...selector, '--input-responses-file', responses];
      for (const credential of [foreign, ownerWithoutTool]) {
        assert.equal(await lookup(credential, 1), '');
        const beforeDenied = requests.length;
        assert.equal(await cli('wait', credential, 1, waitArgs), '');
        assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
      }
      assert.deepEqual(await rows(), [{ task_id: committedID, status: 'input_required', attempt_count: 1, has_result: false }]);
      const waitRequests = ['server/discover', 'gregale/tasks/lookup', 'tasks/get', 'tasks/update'];
      if (formReplyFault === 'before') {
        const beforeLostReply = requests.length;
        const pending = JSON.parse(await cli('wait', owner, 3, waitArgs));
        assert.deepEqual(pending, snapshot);
        assert.deepEqual(requests.slice(beforeLostReply), [...waitRequests, 'tasks/get']);
        assert.deepEqual(await rows(), [{ task_id: committedID, status: 'input_required', attempt_count: 1, has_result: false }]);
      }
      const beforeWait = requests.length;
      const completed = JSON.parse(await cli('wait', owner, 0, waitArgs));
      assert.ifError(proxyError);
      assert.equal(completed.taskId, committedID);
      assert.equal(completed.status, 'completed');
      assert.equal(completed.result.content[0].text, 'Report original is ready after 1 steps.');
      const expected = formReplyFault === 'after' ? [...waitRequests, 'tasks/get'] : waitRequests;
      assert.deepEqual(requests.slice(beforeWait, beforeWait + expected.length), expected);
      assert.equal(requests.filter(method => method === 'tasks/update').length, formReplyFault === 'before' ? 2 : 1);
      assert.equal(requests.filter(method => method === 'gregale/tasks/submit').length, 2, 'one submission relayed by the test server');
      assert.deepEqual(await rows(), [{ task_id: committedID, status: 'completed', attempt_count: 1, has_result: true }]);
      return;
    }
    if (!useReceipt) {
      for (const replay of await Promise.all([submission(owner, 0), submission(owner, 0)])) {
        const { result } = JSON.parse(replay);
        assert.equal(result.resultType, 'task');
        assert.equal(result.taskId, committedID);
      }
      assert.equal(await submission(owner, 1, 'changed'), '');
      assert.equal(await submission(foreign, 1), '');
      assert.equal(await submission(owner, 1, 'original', makeKey(Date.now() - 61_000)), '');
    }
    await eventually(async () => (await rows())[0]?.status === 'completed', 'worker did not complete the Task');
    if (!useReceipt) {
      await delay(Math.max(0, issuedAt + 60_100 - Date.now()));
      assert.equal(await submission(owner, 1), '');
    }
    const recovered = JSON.parse(await lookup(owner, 0));
    assert.ok(Number.isFinite(Date.parse(recovered.lastUpdatedAt)));
    assert.deepEqual(recovered, { taskId: committedID, status: 'completed', lastUpdatedAt: recovered.lastUpdatedAt, nextAction: 'read_result' });
    assert.equal(await lookup(foreign, 1), '');
    assert.equal(await lookup(ownerWithoutTool, 1), '');
    if (useReceipt) {
      const readArgs = [...selector, '--read-result'];
      const beforeMismatch = requests.length;
      assert.equal(await cli('status', owner, 1, [...selector, '--metadata-only', '--url', `${endpoint}/other`]), '');
      assert.equal(await cli('status', owner, 1, [...readArgs, '--url', `${endpoint}/other`]), '');
      assert.equal(await cli('status', owner, 1, [...readArgs, '--metadata-only']), '');
      assert.equal(await cli('status', owner, 1, [...readArgs, '--write-input-template', join(clientDir, 'unused.json')]), '');
      assert.equal(await cli('status', owner, 1, ['--task-id', committedID, '--read-result']), '');
      assert.equal(await cli('status', owner, 1, [...readArgs, '--task-id', committedID]), '');
      assert.equal(await cli('wait', owner, 1, [...selector, '--url', `${endpoint}/other`]), '');
      assert.equal(requests.length, beforeMismatch);
      const receipt = await readFile(receiptPath, 'utf8');
      assert.equal(await cli('call', owner, 1, receiptCall), '');
      const { submissionKey } = JSON.parse(receipt);
      const receiptIssuedAt = Number.parseInt(submissionKey.slice(0, 13).replace('-', ''), 16);
      await delay(Math.max(0, receiptIssuedAt + 60_100 - Date.now()));
      const beforeExpiredRetry = requests.length;
      assert.equal(await cli('call', owner, 1, [...receiptCall, '--retry-submission'], /submission retry window expired/), '');
      assert.equal(requests.length, beforeExpiredRetry);
      assert.equal(await submission(owner, 1, 'original', submissionKey), '');
      const beforeRead = requests.length;
      const completed = JSON.parse(await cli('status', owner, 0, readArgs));
      assert.equal(completed.taskId, committedID);
      assert.equal(completed.status, 'completed');
      assert.equal(completed.result.content[0].text, report);
      assert.deepEqual(requests.slice(beforeRead), ['server/discover', 'gregale/tasks/lookup', 'tasks/get']);
      const beforeWait = requests.length;
      const waited = JSON.parse(await cli('wait', owner, 0, selector));
      assert.equal(waited.taskId, committedID);
      assert.equal(waited.status, 'completed');
      assert.equal(waited.result.content[0].text, report);
      assert.deepEqual(requests.slice(beforeWait), ['server/discover', 'gregale/tasks/lookup', 'tasks/get']);
      for (const credential of [foreign, ownerWithoutTool]) {
        const beforeReadDenied = requests.length;
        assert.equal(await cli('status', credential, 1, readArgs), '');
        assert.deepEqual(requests.slice(beforeReadDenied), ['server/discover', 'gregale/tasks/lookup']);
        const beforeDenied = requests.length;
        assert.equal(await cli('wait', credential, 1, selector), '');
        assert.deepEqual(requests.slice(beforeDenied), ['server/discover', 'gregale/tasks/lookup']);
      }
      assert.equal(await readFile(receiptPath, 'utf8'), receipt);
    } else {
      assert.equal(await cli('status', owner, 1, ['--task-submission-key', makeKey(), '--metadata-only']), '');
    }
    const full = JSON.parse(await cli('status', owner, 0, ['--task-id', recovered.taskId]));
    assert.equal(full.result.content[0].text, report);
    assert.deepEqual(await rows(), [{ task_id: committedID, status: 'completed', attempt_count: 1, has_result: true }]);
    await pool.query('UPDATE gregale_mcp_tasks SET expires_at=clock_timestamp() WHERE namespace=$1 AND task_id=$2', [namespace, committedID]);
    assert.equal(await lookup(owner, 1), '');
    if (useReceipt) {
      assert.equal(await cli('status', owner, 1, [...selector, '--read-result']), '');
      assert.equal(await cli('wait', owner, 1, selector), '');
    }
  } finally {
    for (const gate of watchGates) gate.resolve();
    finishSupervisorReport.resolve();
    api.closeTaskSubscriptions();
    await new Promise(resolveClose => { server.close(resolveClose); server.closeAllConnections(); });
    await api.handler.close();
    await runtime.stop();
  }
});
