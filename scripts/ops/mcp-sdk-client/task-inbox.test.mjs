import assert from 'node:assert/strict';
import { execFile, fork, spawn } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  cancelMcpTask, continueMcpTaskGroupSubmission, createMcpTaskGroup, createMcpTaskReviewFetch, createMcpTaskTransport,
  getMcpTask, getTaskInputReview, listTaskInbox, lookupMcpTaskBySubmissionKey, respondToReviewedTaskInput, resumeMcpTaskGroupSubmission,
  stopMcpTaskGroup, submitMcpTask, submitMcpTaskWithReceipt, watchMcpTask, watchMcpTaskGroup, watchMcpTasks, watchTaskInbox,
} from '../../../sdk/node/dist/preview.js';

test('the official SDK watches and resumes authenticated PostgreSQL Tasks', { timeout: 45_000 }, async t => {
  const starterDir = process.env.MCP_TASKS_TEST_NODE_STARTER;
  const connectionString = process.env.MCP_TASKS_TEST_DATABASE_URL;
  assert.ok(starterDir && connectionString, 'set MCP_TASKS_TEST_NODE_STARTER and MCP_TASKS_TEST_DATABASE_URL');
  const fromStarter = file => import(pathToFileURL(resolve(starterDir, file)).href);
  const requireStarter = createRequire(resolve(starterDir, 'package.json'));
  const { Pool } = requireStarter('pg');
  const { generateKeyPair, SignJWT } = await import(pathToFileURL(requireStarter.resolve('jose')).href);
  const [{ createApp }, { createPostgresMcpTaskStore }, { createMcpTaskRuntime, mcpTaskHandlers }] = await Promise.all([
    fromStarter('app.js'), fromStarter('task-store.js'), fromStarter('tasks.js'),
  ]);
  const schema = 'mcp_inbox_sdk_' + randomUUID().replaceAll('-', '');
  const admin = new Pool({ connectionString, connectionTimeoutMillis: 5_000, statement_timeout: 10_000 });
  let pool, closeFixture;
  t.after(async () => {
    try {
      await closeFixture?.();
    } finally {
      try {
        await pool?.end();
        await admin.query(`DROP SCHEMA IF EXISTS ${schema} CASCADE`);
      } finally {
        await admin.end();
      }
    }
  });
  await admin.query(`CREATE SCHEMA ${schema}`);
  pool = new Pool({
    connectionString, options: `-c search_path=${schema}`, connectionTimeoutMillis: 5_000, statement_timeout: 10_000,
  });
  const store = createPostgresMcpTaskStore({ pool, namespace: schema, ownerKey: randomBytes(48).toString('base64url'), ttlMs: 120_000 });
  const admissionHandlers = Object.entries(mcpTaskHandlers).map(([name, { version }]) => ({ name, version }));
  await store.migrate({ admissionHandlers });

  const runtime = createMcpTaskRuntime({ store, handlers: mcpTaskHandlers, workerEnabled: false });
  const auth = {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { build_report: ['reports:read'] },
  };
  const { publicKey, privateKey } = await generateKeyPair('ES256');
  const clientId = 'sdk-inbox-fixture';
  const mint = (subject, scope, expiration = '5m', oauthClient = clientId) =>
    new SignJWT({ scope, client_id: oauthClient }).setProtectedHeader({ alg: 'ES256' })
      .setIssuer(auth.issuer).setAudience(auth.resource).setSubject(subject).setExpirationTime(expiration).sign(privateKey);
  const authInfo = subject => ({ clientId, resource: new URL(auth.resource), extra: { subject } });
  const { app } = createApp({
    version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless',
    legacy: false, allowed_origins: [], tasks: { enabled: true }, auth,
  }, { taskRuntime: runtime, keyResolver: publicKey, log() {} });

  const empty = Promise.withResolvers();
  const seeded = Promise.withResolvers();
  const failedPage = Promise.withResolvers();
  const retry = Promise.withResolvers();
  const afterUnchanged = Promise.withResolvers();
  const finalScan = Promise.withResolvers();
  const inbox = runtime.inbox.bind(runtime);
  let response, scans = 0, failed = false, watchStarted = false;
  runtime.inbox = async (query, ...args) => {
    if (watchStarted && !query.cursor) {
      scans++;
      if (scans === 1) response.once('finish', empty.resolve);
      if (scans > 1) await seeded.promise;
      if (scans === 3) await retry.promise;
      if (scans === 5) {
        afterUnchanged.resolve();
        await finalScan.promise;
      }
    }
    if (watchStarted && query.cursor && !failed) {
      failed = true;
      response.destroy();
      failedPage.resolve();
    }
    return inbox(query, ...args);
  };
  let admissionProbe, stopProbe;
  const create = runtime.create.bind(runtime);
  runtime.create = async (...args) => {
    const result = await create(...args);
    if (admissionProbe && args[1]?.report === `${admissionProbe.report} two`) {
      admissionProbe.committed.resolve(result.taskId);
      await admissionProbe.release.promise;
    }
    if (admissionProbe && args[1]?.report === `${admissionProbe.report} three`) admissionProbe.thirdKeys.add(args[6]);
    return result;
  };
  const cancel = runtime.cancel.bind(runtime);
  runtime.cancel = async (id, ...args) => {
    const result = await cancel(id, ...args);
    if (stopProbe?.ids.has(id)) {
      stopProbe.cancellations.push(id);
      if (stopProbe.hold && id === stopProbe.firstId) {
        stopProbe.committed.resolve();
        await stopProbe.release.promise;
      }
    }
    return result;
  };
  let taskWatch, groupRequests;
  const server = createServer((req, res) => {
    const method = req.headers['mcp-method'];
    if (groupRequests) {
      if (method === 'gregale/tasks/submit' || method === 'gregale/tasks/submit-bound' || method === 'tools/call') {
        groupRequests.submissions++;
      }
      if (method === 'gregale/tasks/input-review') groupRequests.reviews++;
      if (method === 'gregale/tasks/respond-reviewed') groupRequests.responses++;
    }
    if (stopProbe?.hold && method === 'gregale/tasks/lookup') {
      void stopProbe.release.promise.then(() => { if (!res.destroyed) app(req, res); });
      return;
    }
    if (method === 'gregale/tasks/inbox') response = res;
    if (taskWatch && method === 'subscriptions/listen') {
      taskWatch.stream = res;
      taskWatch.opens++;
      taskWatch.opened?.[taskWatch.opens - 1]?.resolve(res);
    }
    if (taskWatch?.stream && method === 'tasks/get') {
      if (taskWatch.opens === 2) taskWatch.reconnected?.resolve();
      taskWatch.reconciling.resolve();
      void taskWatch.release.promise.then(() => app(req, res));
      return;
    }
    app(req, res);
  });
  const stop = new AbortController();
  const clients = [];
  let watch;
  closeFixture = async () => {
    stop.abort();
    seeded.resolve();
    retry.resolve();
    finalScan.resolve();
    await watch?.return();
    await Promise.allSettled(clients.map(client => client.close()));
    await new Promise(resolve => server.close(resolve));
    await runtime.stop();
  };
  await runtime.start();
  assert.equal(store.supportsTaskInbox, true);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const endpoint = new URL(`http://127.0.0.1:${server.address().port}/mcp`);
  async function connect(subject, scope, fetch) {
    const token = await mint(subject, scope);
    const client = new Client({ name: 'gregale-inbox-sdk-interop', version: '1.0.0' }, {
      versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
    });
    clients.push(client);
    await client.connect(createMcpTaskTransport(StreamableHTTPClientTransport, endpoint, {
      requestInit: { headers: { Authorization: `Bearer ${token}` } }, onInsufficientScope: 'throw', fetch,
    }));
    return client;
  }

  const owner = await connect('alice', 'mcp:tools reports:read');
  async function claimTask(subject) {
    const principal = authInfo(subject);
    const created = await store.create({
      toolName: 'build_report', handlerVersion: mcpTaskHandlers.build_report.version, args: { report: 'private SDK fixture', steps: 1 },
      authInfo: principal, authMode: 'external-oauth', inputMethods: ['elicitation/create:form'],
    });
    const claim = await store.claim(3, 30_000);
    assert.equal(claim.task_id, created.task_id);
    return claim;
  }

  async function requestInput(claim, name = 'approval', message = 'Private SDK fixture prompt') {
    assert.equal((await store.requestInputs({
      taskID: claim.task_id, leaseToken: claim.lease_token,
      requests: { [name]: { method: 'elicitation/create', params: {
        mode: 'form', message, requestedSchema: { type: 'object', properties: {} },
      } } },
    })).ready, false);
  }

  async function pause(subject) {
    const claim = await claimTask(subject);
    await requestInput(claim);
    return claim.task_id;
  }

  watchStarted = true;
  watch = watchTaskInbox(owner, { status: 'input_required', limit: 1, intervalMs: 1_000, signal: stop.signal });
  const first = watch.next();
  await Promise.race([empty.promise, first.then(() => assert.fail('empty inbox returned an event'))]);
  const ids = new Set([await pause('alice'), await pause('alice')]);
  await pause('bob');
  seeded.resolve();
  await Promise.race([failedPage.promise, first.then(() => assert.fail('partial scan returned an event'))]);
  retry.resolve();
  for (const event of [await first, await watch.next()]) {
    assert.equal(event.done, false);
    assert.ok(ids.delete(event.value.taskId));
    assert.equal(event.value.nextAction, 'provide_input');
    assert.deepEqual(Object.keys(event.value).sort(),
      ['createdAt', 'lastUpdatedAt', 'nextAction', 'pollIntervalMs', 'status', 'taskId', 'ttlMs']);
  }
  assert.equal(ids.size, 0);

  const pending = watch.next();
  await Promise.race([afterUnchanged.promise, pending.then(() => assert.fail('unchanged scan replayed a Task'))]);
  const cancelled = assert.rejects(pending, { name: 'AbortError' });
  stop.abort();
  await cancelled;
  finalScan.resolve();
  watchStarted = false;

  const reduced = await connect('alice', 'mcp:tools');
  assert.deepEqual(await listTaskInbox(reduced, { status: 'input_required' }), { tasks: [] });

  const timestamp = (Date.now() - 1000).toString(16).padStart(12, '0');
  const submissionKey = `${timestamp.slice(0, 8)}-${timestamp.slice(8)}-7000-8000-000000000001`;
  const receipt = await submitMcpTask(owner, {
    submissionKey, toolName: 'build_report', arguments: { report: 'keyed inbox fixture', steps: 1 },
  });
  assert.equal(receipt.outcome, 'accepted');
  const found = await lookupMcpTaskBySubmissionKey(owner, submissionKey);
  assert.equal(found.taskId, receipt.task.taskId);
  assert.equal(found.status, 'working');
  await assert.rejects(lookupMcpTaskBySubmissionKey(reduced, submissionKey), { name: 'McpTaskSubmissionError' });
  assert.deepEqual(await cancelMcpTask(owner, found.taskId), { outcome: 'requested', taskId: found.taskId });
  assert.equal((await getMcpTask(owner, found.taskId)).status, 'cancelled');

  const lease = await claimTask('alice');
  taskWatch = { opens: 0, reconciling: Promise.withResolvers(), release: Promise.withResolvers() };
  const live = watchMcpTask(owner, lease.task_id, { timeoutMs: 8_000 });
  try {
    const initial = await live.next();
    assert.equal(initial.value.status, 'working');
    const next = live.next();
    const completion = await Promise.race([next, taskWatch.reconciling.promise]);
    assert.equal(completion, undefined, 'watch advanced before reconciling its subscription');
    assert.equal(taskWatch.opens, 1);
    assert.equal(taskWatch.stream.headersSent, true);
    assert.equal(taskWatch.stream.destroyed, false);
    const closed = once(taskWatch.stream, 'close');
    taskWatch.stream.destroy();
    await closed;
    const result = { content: [{ type: 'text', text: 'completed while disconnected' }] };
    assert.equal(await store.complete(lease.task_id, lease.lease_token, result), 'completed');
    taskWatch.release.resolve();
    const terminal = await next;
    assert.equal(terminal.done, false);
    assert.equal(terminal.value.taskId, lease.task_id);
    assert.equal(terminal.value.status, 'completed');
    assert.equal(Object.hasOwn(terminal.value, 'result'), false);
    assert.deepEqual(await live.next(), { done: true, value: undefined });
    assert.equal(taskWatch.opens, 1);
    assert.deepEqual((await getMcpTask(owner, lease.task_id)).result, result);
  } finally {
    taskWatch.release.resolve();
    await live.return();
    taskWatch = undefined;
  }

  const completed = await claimTask('alice');
  const paused = await claimTask('alice');
  const remaining = await claimTask('alice');
  const batchIds = [completed.task_id, paused.task_id, remaining.task_id];
  taskWatch = {
    opens: 0, reconciling: Promise.withResolvers(), reconnected: Promise.withResolvers(), release: Promise.withResolvers(),
  };
  const batch = watchMcpTasks(owner, batchIds, { timeoutMs: 8_000 });
  async function nextStatus(taskId, status) {
    const event = await batch.next();
    assert.equal(event.done, false);
    assert.equal(event.value.outcome, 'available');
    assert.equal(event.value.taskId, taskId);
    assert.equal(event.value.task.status, status);
    assert.equal(Object.hasOwn(event.value.task, 'result'), false);
    assert.equal(Object.hasOwn(event.value.task, 'inputRequests'), false);
  }
  try {
    for (const id of batchIds) await nextStatus(id, 'working');
    const recovered = nextStatus(completed.task_id, 'completed');
    await Promise.race([
      taskWatch.reconciling.promise, recovered.then(() => assert.fail('batch advanced before opening its shared stream')),
    ]);
    assert.equal(taskWatch.opens, 1);
    assert.equal(taskWatch.stream.headersSent, true);
    const closed = once(taskWatch.stream, 'close');
    taskWatch.stream.destroy();
    await closed;

    const result = { content: [{ type: 'text', text: 'completed during the batch disconnect' }] };
    assert.equal(await store.complete(completed.task_id, completed.lease_token, result), 'completed');
    await requestInput(paused);
    taskWatch.release.resolve();
    await recovered;
    await nextStatus(paused.task_id, 'input_required');

    const finished = nextStatus(remaining.task_id, 'completed');
    await Promise.race([
      taskWatch.reconnected.promise, finished.then(() => assert.fail('batch finished before it reconnected')),
    ]);
    assert.equal(taskWatch.opens, 2);
    assert.equal((await getMcpTask(owner, remaining.task_id)).status, 'working');
    assert.equal(await store.complete(remaining.task_id, remaining.lease_token, result), 'completed');
    await finished;
    assert.deepEqual(await batch.next(), { done: true, value: undefined });
    assert.equal(taskWatch.opens, 2);
    assert.deepEqual((await getMcpTask(owner, completed.task_id)).result, result);
  } finally {
    taskWatch.release.resolve();
    await batch.return();
    taskWatch = undefined;
  }

  const groupRoot = await mkdtemp(join(tmpdir(), 'gregale-sdk-task-group-'));
  const group = join(groupRoot, 'saved');
  const acknowledgementGroup = join(groupRoot, 'acknowledgement-interop');
  const agents = [];
  const scope = 'mcp:tools reports:read';
  const gregale = process.env.MCP_TASKS_TEST_GREGALE;
  const groupCLIArgs = (action, path, flags = []) => [
    'mcp', 'client', 'group', action, '--url', endpoint.href, '--group', path,
    '--token-env', 'MCP_TASK_GROUP_FIXTURE_TOKEN', '--timeout', '4s', ...flags,
  ];
  const groupCLIEnv = token => ({ ...process.env, MCP_TASK_GROUP_FIXTURE_TOKEN: token });
  async function runGroupCLI(action, path, token, ...flags) {
    const { stdout } = await promisify(execFile)(gregale, groupCLIArgs(action, path, flags), {
      env: groupCLIEnv(token), timeout: 5_000,
    });
    return action === 'watch' ? stdout.trimEnd().split('\n').map(line => JSON.parse(line)) : JSON.parse(stdout);
  }
  async function readWatchAndInterrupt(path, token, count, ...flags) {
    const child = spawn(gregale, groupCLIArgs('watch', path, flags), {
      env: groupCLIEnv(token), stdio: ['ignore', 'pipe', 'inherit'],
    });
    const exit = once(child, 'exit');
    agents.push({ child, exit });
    const lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
    const events = [];
    try {
      for await (const line of lines) {
        events.push(JSON.parse(line));
        if (events.length === count) {
          child.kill('SIGKILL');
          break;
        }
      }
    } finally {
      lines.close();
    }
    assert.deepEqual(await exit, [null, 'SIGKILL']);
    return events;
  }
  async function startAgent({ token, groupPath = group, ...configuration } = {}) {
    token ??= await mint('alice', 'mcp:tools reports:read');
    const child = fork(new URL('./task-group-agent.mjs', import.meta.url), [], {
      execArgv: [], stdio: ['ignore', 'ignore', 'inherit', 'ipc'],
    });
    const exit = once(child, 'exit');
    const events = [];
    let progress = Promise.withResolvers();
    child.on('message', event => {
      events.push(event);
      progress.resolve();
      progress = Promise.withResolvers();
    });
    const agent = {
      child, exit, events,
      async running(promise) {
        return Promise.race([promise, exit.then(() => assert.fail('Task group agent exited before the expected update'))]);
      },
      async waitForEvents(count) {
        while (events.length < count) await this.running(progress.promise);
        return events.slice(0, count);
      },
    };
    agents.push(agent);
    child.send({ endpoint: endpoint.href, token, group: groupPath, ...configuration });
    return agent;
  }
  function monitorGroup() {
    taskWatch = { opens: 0, reconciling: Promise.withResolvers(), release: Promise.withResolvers() };
    taskWatch.release.resolve();
  }

  groupRequests = { submissions: 0, reviews: 0, responses: 0 };
  try {
    const paths = ['completed', 'paused', 'remaining'].map(name => join(groupRoot, name));
    const leases = [];
    for (const path of paths) {
      const submitted = await submitMcpTaskWithReceipt(owner, path, {
        toolName: 'build_report', arguments: { report: 'Task group restart', steps: 1 }, deferFormInput: true,
      });
      assert.equal(submitted.outcome, 'accepted');
      assert.equal(submitted.taskIdRecorded, true);
      const claim = await store.claim(3, 30_000);
      assert.equal(claim.task_id, submitted.task.taskId);
      leases.push(claim);
    }
    assert.equal(groupRequests.submissions, 3);
    await createMcpTaskGroup(group, paths);

    monitorGroup();
    const original = await startAgent();
    assert.deepEqual((await original.waitForEvents(3)).map(event => [event.taskId, event.status]),
      leases.map(claim => [claim.task_id, 'working']));
    await original.running(taskWatch.reconciling.promise);
    assert.equal(taskWatch.opens, 1);
    original.child.kill('SIGKILL');
    assert.deepEqual(await original.exit, [null, 'SIGKILL']);

    const result = { content: [{ type: 'text', text: 'finished during the agent restart' }] };
    assert.equal(await store.complete(leases[0].task_id, leases[0].lease_token, result), 'completed');
    await requestInput(leases[1]);

    if (gregale) {
      await createMcpTaskGroup(acknowledgementGroup, paths);
      const initialToken = await mint('alice', scope);
      const renewedToken = await mint('alice', scope, '6m');
      assert.notEqual(renewedToken, initialToken);
      const [completedUpdate, pendingReview] = await readWatchAndInterrupt(acknowledgementGroup, initialToken, 2);
      assert.equal(completedUpdate.receiptIndex, 0);
      assert.equal(pendingReview.receiptIndex, 1);
      assert.equal(pendingReview.task.status, 'input_required');
      assert.match(pendingReview.updateId, /^[0-9a-f]{64}$/);
      assert.deepEqual(await readWatchAndInterrupt(acknowledgementGroup, renewedToken, 2, '--resume'),
        [completedUpdate, pendingReview]);

      const { stdout } = await promisify(execFile)(gregale, [
        'mcp', 'client', 'group', 'ack', '--group', acknowledgementGroup, '--update-id', completedUpdate.updateId,
      ], { timeout: 5_000 });
      assert.deepEqual(JSON.parse(stdout), { updateId: completedUpdate.updateId, acknowledged: true });
      const sdkWatch = watchMcpTaskGroup(owner, acknowledgementGroup);
      try {
        const { value: reviewNotice } = await sdkWatch.next();
        assert.equal(reviewNotice.receiptIndex, 1);
        assert.equal(reviewNotice.updateId, pendingReview.updateId);
        await reviewNotice.acknowledge();
      } finally {
        await sdkWatch.return();
      }
      const [remainingUpdate] = await readWatchAndInterrupt(acknowledgementGroup, renewedToken, 1, '--resume');
      assert.equal(remainingUpdate.receiptIndex, 2);
      assert.equal(remainingUpdate.task.status, 'working');
    }

    monitorGroup();
    const resumed = await startAgent({ followInput: true });
    assert.deepEqual((await resumed.waitForEvents(2)).map(event => [event.receiptIndex, event.taskId, event.status]), [
      [0, leases[0].task_id, 'completed'], [1, leases[1].task_id, 'input_required'],
    ]);
    await resumed.running(taskWatch.reconciling.promise);
    assert.equal(taskWatch.opens, 1);
    resumed.child.kill('SIGKILL');
    assert.deepEqual(await resumed.exit, [null, 'SIGKILL']);

    // The previous agent acknowledged the pause. A fresh agent must follow
    // both outstanding Tasks and leave the review to the separate caller.
    monitorGroup();
    const following = await startAgent({ followInput: true });
    await following.running(taskWatch.reconciling.promise);
    assert.equal(taskWatch.opens, 1);
    assert.deepEqual(following.events, []);
    assert.equal(groupRequests.reviews, 0);
    assert.equal(groupRequests.responses, 0);

    assert.equal(await store.complete(leases[2].task_id, leases[2].lease_token, result), 'completed');
    const [parallelCompletion] = await following.waitForEvents(1);
    assert.equal(parallelCompletion.taskId, leases[2].task_id);
    assert.equal(parallelCompletion.status, 'completed');
    assert.equal((await watchMcpTaskGroup(owner, group).next()).done, true);

    const reviewer = await connect('alice', 'mcp:tools reports:read', createMcpTaskReviewFetch());
    const pausedId = leases[1].task_id;
    const review = await getTaskInputReview(reviewer, pausedId);
    assert.equal(review.task.status, 'input_required');
    assert.equal(review.forms.approval.message, 'Private SDK fixture prompt');
    const answer = await respondToReviewedTaskInput(reviewer, review, { approval: { action: 'accept', content: {} } });
    assert.equal(answer.outcome, 'applied');
    assert.equal(answer.task.status, 'working');

    const [, working] = await following.waitForEvents(2);
    assert.equal(working.taskId, pausedId);
    assert.equal(working.status, 'working');
    const claim = await store.claim(3, 30_000);
    assert.equal(claim.task_id, pausedId);
    assert.equal(await store.complete(pausedId, claim.lease_token, result), 'completed');
    assert.deepEqual(await following.exit, [0, null]);
    const reviewEvents = following.events.slice(1);
    assert.ok(reviewEvents.every(event => event.taskId === pausedId && event.receiptIndex === 1));
    assert.equal(reviewEvents.some(event => event.status === 'input_required'), false);
    assert.equal(reviewEvents.at(-1).status, 'completed');
    assert.equal(taskWatch.opens, 1);
    assert.deepEqual((await getMcpTask(owner, pausedId)).result, result);
    if (gregale) {
      const token = await mint('alice', scope);
      const updates = await runGroupCLI('watch', acknowledgementGroup, token, '--resume');
      assert.deepEqual(updates.map(event => [event.receiptIndex, event.task.status]), [[1, 'completed'], [2, 'completed']]);
      const snapshots = await runGroupCLI('watch', acknowledgementGroup, token);
      assert.deepEqual(snapshots.map(event => event.receiptIndex), [0, 1, 2]);
    }

    const other = await connect('bob', 'mcp:tools reports:read');
    const inaccessible = [];
    for await (const event of watchMcpTaskGroup(other, group)) {
      assert.equal(event.outcome, 'unavailable');
      assert.deepEqual(Object.keys(event).sort(), ['error', 'outcome', 'receiptIndex']);
      inaccessible.push(event.receiptIndex);
    }
    assert.deepEqual(inaccessible, [0, 1, 2]);
    assert.deepEqual(groupRequests, { submissions: 3, reviews: 1, responses: 1 });

    const planned = join(groupRoot, 'submission');
    const submissions = ['one', 'two', 'three'].map(name => ({
      toolName: 'build_report', arguments: { report: `group submission ${name}`, steps: 1 },
    }));
    const token = await mint('carol', 'mcp:tools reports:read');
    groupRequests.submissions = 0;
    admissionProbe = {
      report: 'group submission', committed: Promise.withResolvers(), release: Promise.withResolvers(), thirdKeys: new Set(),
    };
    const submitter = await startAgent({ token, groupPath: planned, submissions, keyAgeMs: 50_000 });
    const secondId = await submitter.running(admissionProbe.committed.promise);
    assert.equal(groupRequests.submissions, 2);
    submitter.child.kill('SIGKILL');
    assert.deepEqual(await submitter.exit, [null, 'SIGKILL']);
    admissionProbe.release.resolve();
    const first = JSON.parse(await readFile(join(planned, 'member-0.json'), 'utf8'));
    const second = JSON.parse(await readFile(join(planned, 'member-1.json'), 'utf8'));
    assert.ok(first.taskId);
    assert.equal(second.taskId, undefined);
    await assert.rejects(stat(join(planned, 'member-2.json')), { code: 'ENOENT' });

    const plan = JSON.parse(await readFile(join(planned, 'group.json'), 'utf8'));
    const originalThird = plan.members[2].submissionKey;
    const keyTime = Number.parseInt(originalThird.replaceAll('-', '').slice(0, 12), 16);
    await delay(Math.max(0, keyTime + 60_010 - Date.now()));

    const configuration = {
      token, groupPath: planned, continuation: { receiptIndex: 2, submission: submissions[2] },
    };
    const recovering = await Promise.all([startAgent(configuration), startAgent(configuration)]);
    const evidence = await Promise.all(recovering.map(agent => agent.waitForEvents(1)));
    for (const [{ recovered }] of evidence) {
      assert.deepEqual(recovered.map(entry => [entry.receiptIndex, entry.outcome, entry.task?.taskId]), [
        [0, 'accepted', first.taskId], [1, 'accepted', secondId], [2, 'prepared', undefined],
      ]);
    }
    assert.equal(groupRequests.submissions, 2);
    for (const agent of recovering) agent.child.send('continue');
    const continuations = await Promise.all(recovering.map(agent => agent.waitForEvents(2)));
    assert.deepEqual(await Promise.all(recovering.map(agent => agent.exit)), [[0, null], [0, null]]);
    const third = JSON.parse(await readFile(join(planned, 'member-2.json'), 'utf8'));
    assert.notEqual(third.submissionKey, originalThird);
    assert.equal(third.plannedSubmissionKey, originalThird);
    assert.deepEqual(admissionProbe.thirdKeys, new Set([third.submissionKey]));
    assert.ok(continuations.some(([, { continued }]) => continued.outcome === 'accepted'));
    for (const [, { continued }] of continuations) {
      assert.equal(continued.receiptIndex, 2);
      if (continued.outcome === 'accepted') assert.equal(continued.task.taskId, third.taskId);
      else assert.equal(continued.outcome, 'unconfirmed');
    }

    const creator = await connect('carol', 'mcp:tools reports:read');
    const created = (await listTaskInbox(creator)).tasks.map(task => task.taskId);
    assert.equal(created.length, 3);
    assert.deepEqual(new Set(created), new Set([first.taskId, secondId, third.taskId]));

    // Empty the prior fixture's queued work before claiming a worker lease.
    for (const id of created) await cancelMcpTask(creator, id);
    for (const throughCLI of gregale ? [false, true] : [false]) {
      const stoppedGroup = join(groupRoot, throughCLI ? 'stopped-by-cli' : 'stopped-by-sdk');
      const subject = throughCLI ? 'dana-cli' : 'dana';
      const stoppable = ['one', 'two', 'three'].map(name => ({
        toolName: 'build_report', arguments: { report: `group stop ${name}`, steps: 1 },
      }));
      const stopToken = await mint(subject, scope);
      groupRequests.submissions = 0;
      admissionProbe = {
        report: 'group stop', committed: Promise.withResolvers(), release: Promise.withResolvers(), thirdKeys: new Set(),
      };
      const sending = await startAgent({ token: stopToken, groupPath: stoppedGroup, submissions: stoppable });
      const lostId = await sending.running(admissionProbe.committed.promise);
      sending.child.kill('SIGKILL');
      assert.deepEqual(await sending.exit, [null, 'SIGKILL']);
      admissionProbe.release.resolve();
      const { taskId: runningId } = JSON.parse(await readFile(join(stoppedGroup, 'member-0.json'), 'utf8'));
      const { taskId: lostReceiptId } = JSON.parse(await readFile(join(stoppedGroup, 'member-1.json'), 'utf8'));
      assert.equal(lostReceiptId, undefined);
      const running = await store.claim(3, 30_000);
      assert.equal(running.task_id, runningId);

      const stoppedOwner = await connect(subject, scope);
      let witnessId;
      if (throughCLI) {
        const witnessReceipt = join(groupRoot, 'unrelated-cli-receipt');
        const witness = await submitMcpTaskWithReceipt(stoppedOwner, witnessReceipt, {
          toolName: 'build_report', arguments: { report: 'Unrelated group', steps: 1 },
        });
        assert.equal(witness.outcome, 'accepted');
        witnessId = witness.task.taskId;
        await createMcpTaskGroup(join(groupRoot, 'unrelated-cli-group'), [witnessReceipt]);
      }

      stopProbe = {
        ids: new Set([runningId, lostId]), firstId: runningId, hold: true, cancellations: [],
        committed: Promise.withResolvers(), release: Promise.withResolvers(),
      };
      let stopping;
      if (throughCLI) {
        const child = spawn(gregale, groupCLIArgs('stop', stoppedGroup), {
          env: groupCLIEnv(stopToken), stdio: ['ignore', 'ignore', 'inherit'],
        });
        stopping = { child, exit: once(child, 'exit') };
        agents.push(stopping);
        await Promise.race([
          stopProbe.committed.promise,
          stopping.exit.then(() => assert.fail('CLI exited before requesting cancellation')),
        ]);
      } else {
        stopping = await startAgent({ token: stopToken, groupPath: stoppedGroup, stopGroup: true });
        await stopping.running(stopProbe.committed.promise);
      }
      assert.deepEqual(stopProbe.cancellations, [runningId]);
      const stoppedManifest = JSON.parse(await readFile(join(stoppedGroup, 'group.json'), 'utf8'));
      assert.equal(await readFile(join(stoppedGroup, 'stopped'), 'utf8'), stoppedManifest.id);
      await assert.rejects(continueMcpTaskGroupSubmission(stoppedOwner, stoppedGroup, 2, stoppable[2]), { code: 'group_stopped' });

      stopping.child.kill('SIGKILL');
      assert.deepEqual(await stopping.exit, [null, 'SIGKILL']);
      stopProbe.hold = false;
      stopProbe.release.resolve();
      assert.equal(await store.complete(runningId, running.lease_token, result), 'cancelled');

      let stopped;
      if (throughCLI) {
        const renewed = await mint(subject, scope, '6m');
        assert.notEqual(renewed, stopToken);
        stopped = await runGroupCLI('stop', stoppedGroup, renewed);
      } else {
        const retryStop = await startAgent({ token: stopToken, groupPath: stoppedGroup, stopGroup: true });
        [{ stopped }] = await retryStop.waitForEvents(1);
        assert.deepEqual(await retryStop.exit, [0, null]);
      }
      assert.deepEqual(stopped.map(entry => entry.outcome), ['terminal', 'requested', 'not_submitted']);
      assert.equal(stopped[0].task.taskId, runningId);
      assert.equal(stopped[0].task.status, 'cancelled');
      assert.equal(stopped[1].taskId, lostId);
      assert.deepEqual((await stopMcpTaskGroup(stoppedOwner, stoppedGroup)).map(entry => entry.outcome),
        ['terminal', 'terminal', 'not_submitted']);
      assert.deepEqual(stopProbe.cancellations, [runningId, lostId]);
      await assert.rejects(stat(join(stoppedGroup, 'member-2.json')), { code: 'ENOENT' });
      assert.equal(groupRequests.submissions, throughCLI ? 3 : 2);
      if (witnessId) {
        assert.equal((await getMcpTask(stoppedOwner, witnessId)).status, 'working');
        const witnessStatus = await runGroupCLI('status', join(groupRoot, 'unrelated-cli-group'), stopToken);
        assert.equal(witnessStatus[0].task.status, 'working');
        await cancelMcpTask(stoppedOwner, witnessId);
      }
      const cancelledGroup = (await listTaskInbox(stoppedOwner)).tasks.filter(task => task.taskId !== witnessId);
      assert.deepEqual(new Set(cancelledGroup.map(task => task.taskId)), new Set([runningId, lostId]));
      assert.ok(cancelledGroup.every(task => task.status === 'cancelled'));
    }

    const resultGroup = join(groupRoot, 'results');
    const resultReceipts = ['complete', 'paused', 'private', 'failed'].map(name => join(groupRoot, `result-${name}`));
    const resultOwner = await connect('erin', 'mcp:tools reports:read');
    const privateOwner = await connect('frank', 'mcp:tools reports:read');
    const resultClaims = [];
    for (const [index, receipt] of resultReceipts.entries()) {
      const submitted = await submitMcpTaskWithReceipt(index === 2 ? privateOwner : resultOwner, receipt, {
        toolName: 'build_report', arguments: { report: 'Task group result', steps: 1 }, deferFormInput: true,
      });
      assert.equal(submitted.outcome, 'accepted');
      assert.equal(submitted.taskIdRecorded, true);
      const claim = await store.claim(3, 30_000);
      assert.equal(claim.task_id, submitted.task.taskId);
      resultClaims.push(claim);
    }
    await createMcpTaskGroup(resultGroup, resultReceipts);

    const firstReader = await startAgent({
      token: await mint('erin', scope), groupPath: resultGroup, collectResults: true,
    });
    const [{ results: initialResults }] = await firstReader.waitForEvents(1);
    assert.deepEqual(await firstReader.exit, [0, null]);
    assert.deepEqual(initialResults.map(entry => entry.task?.status ?? entry.outcome),
      ['working', 'working', 'unavailable', 'working']);

    const [completedResult, pausedResult, privateResult, failedResult] = resultClaims;
    const retained = { content: [{ type: 'text', text: 'Result retained across the agent restart' }], isError: false };
    const retainedError = { code: -32603, message: 'Retained Task execution error' };
    const hiddenPayload = { content: [{ type: 'text', text: 'Other principal secret output' }] };
    async function settleGroupMembers() {
      assert.equal(await store.complete(completedResult.task_id, completedResult.lease_token, retained), 'completed');
      await requestInput(pausedResult);
      assert.equal(await store.complete(privateResult.task_id, privateResult.lease_token, hiddenPayload), 'completed');
    }

    if (gregale) {
      const before = await readFile(join(resultGroup, 'group.json'));
      const files = await readdir(resultGroup);
      const opened = [Promise.withResolvers(), Promise.withResolvers()];
      taskWatch = { opens: 0, opened, reconciling: Promise.withResolvers(), release: Promise.withResolvers() };
      const watching = runGroupCLI('watch', resultGroup, await mint('erin', scope, '6m'));
      const earlyExit = watching.then(() => assert.fail('CLI group watch exited before both streams opened'));
      async function waitForStream(attempt) {
        const stream = await Promise.race([opened[attempt].promise, earlyExit]);
        const signal = AbortSignal.timeout(2_000);
        while (!stream.headersSent) await delay(5, undefined, { signal });
        return stream;
      }
      try {
        const stream = await waitForStream(0);
        // A metadata change on a still-working Task proves that the CLI
        // consumed the subscription acknowledgement before the disconnect.
        assert.equal((await pool.query(
          'UPDATE gregale_mcp_tasks SET updated_at = clock_timestamp() WHERE namespace = $1 AND task_id = $2',
          [schema, failedResult.task_id],
        )).rowCount, 1);
        await Promise.race([taskWatch.reconciling.promise, earlyExit]);
        const closed = once(stream, 'close');
        stream.destroy();
        await closed;
        await settleGroupMembers();
        taskWatch.release.resolve();
        await waitForStream(1);
        assert.equal(await store.fail(failedResult.task_id, failedResult.lease_token, retainedError), 'failed');

        const events = await watching;
        assert.equal(taskWatch.opens, 2);
        assert.deepEqual(events.slice(0, 4).map(event => [event.receiptIndex, event.task?.status ?? event.outcome]), [
          [0, 'working'], [1, 'working'], [2, 'unavailable'], [3, 'working'],
        ]);
        assert.deepEqual(events.slice(4).filter(event => event.task?.status !== 'working')
          .map(event => [event.receiptIndex, event.task?.status, event.task?.nextAction]), [
          [0, 'completed', 'read_result'], [1, 'input_required', 'provide_input'], [3, 'failed', 'none'],
        ]);
        assert.equal(new Set(events.map(event => JSON.stringify(event))).size, events.length);
        for (const { task } of events) {
          if (!task) continue;
          for (const field of ['result', 'error', 'inputRequests', 'statusMessage']) {
            assert.equal(Object.hasOwn(task, field), false);
          }
        }
        assert.deepEqual(await readFile(join(resultGroup, 'group.json')), before);
        assert.deepEqual((await readdir(resultGroup)).sort(), files.sort());
      } finally {
        taskWatch.release.resolve();
        taskWatch = undefined;
      }
    } else {
      await settleGroupMembers();
      assert.equal(await store.fail(failedResult.task_id, failedResult.lease_token, retainedError), 'failed');
    }

    const restartedReader = await startAgent({
      token: await mint('erin', scope), groupPath: resultGroup, collectResults: true,
    });
    const [{ results: collected }] = await restartedReader.waitForEvents(1);
    assert.deepEqual(await restartedReader.exit, [0, null]);
    assert.deepEqual(collected.map(entry => [entry.receiptIndex, entry.task?.status ?? entry.outcome]), [
      [0, 'completed'], [1, 'input_required'], [2, 'unavailable'], [3, 'failed'],
    ]);
    assert.equal(collected[0].task.taskId, completedResult.task_id);
    assert.deepEqual(collected[0].task.result, retained);
    assert.equal(collected[1].task.nextAction, 'provide_input');
    assert.equal(Object.hasOwn(collected[1].task, 'inputRequests'), false);
    assert.equal(Object.hasOwn(collected[2], 'task'), false);
    assert.deepEqual(collected[3].task.error, retainedError);
    assert.equal(JSON.stringify(collected).includes(hiddenPayload.content[0].text), false);

    if (gregale) {
      const groupToken = await mint('erin', scope, '6m');
      const before = await readFile(join(resultGroup, 'group.json'));
      const metadata = await runGroupCLI('status', resultGroup, groupToken);
      assert.deepEqual(metadata.map(({ receiptIndex, outcome, task }) =>
        [receiptIndex, outcome, task?.status, task?.nextAction]), [
        [0, 'available', 'completed', 'read_result'],
        [1, 'available', 'input_required', 'provide_input'],
        [2, 'unavailable', undefined, undefined],
        [3, 'available', 'failed', 'none'],
      ]);
      assert.equal(metadata[0].task.taskId, completedResult.task_id);
      assert.deepEqual(metadata[2], { receiptIndex: 2, outcome: 'unavailable' });
      for (const { task } of metadata) {
        if (!task) continue;
        for (const field of ['result', 'error', 'inputRequests', 'statusMessage']) {
          assert.equal(Object.hasOwn(task, field), false);
        }
      }
      const payloads = await runGroupCLI('status', resultGroup, groupToken, '--include-results');
      assert.deepEqual(payloads[0].task.result, retained);
      assert.deepEqual(payloads[1], metadata[1]);
      assert.deepEqual(payloads[2], metadata[2]);
      assert.deepEqual(payloads[3].task.error, retainedError);
      assert.deepEqual(await readFile(join(resultGroup, 'group.json')), before);
    }

    const renewableGroup = join(groupRoot, 'renewed-credential');
    const renewable = ['one', 'two', 'three', 'four'].map(name => ({
      toolName: 'build_report', arguments: { report: `group credential renewal ${name}`, steps: 1 },
    }));
    const expiresAt = Math.floor(Date.now() / 1_000) + 5;
    const expiring = await mint('gina', scope, expiresAt);
    admissionProbe = {
      report: 'group credential renewal', committed: Promise.withResolvers(), release: Promise.withResolvers(),
      thirdKeys: new Set(),
    };
    const originalSender = await startAgent({ token: expiring, groupPath: renewableGroup, submissions: renewable });
    const lostOnRenewal = await originalSender.running(admissionProbe.committed.promise);
    originalSender.child.kill('SIGKILL');
    assert.deepEqual(await originalSender.exit, [null, 'SIGKILL']);
    admissionProbe.release.resolve();

    const ownerPlan = JSON.parse(await readFile(join(renewableGroup, 'group.json'), 'utf8'));
    assert.equal(ownerPlan.version, 3);
    assert.match(ownerPlan.ownerBinding, /^[0-9a-f]{64}$/);
    const confirmed = JSON.parse(await readFile(join(renewableGroup, 'member-0.json'), 'utf8'));
    await assert.rejects(stat(join(renewableGroup, 'member-2.json')), { code: 'ENOENT' });
    await delay(Math.max(0, expiresAt * 1_000 + 25 - Date.now()));
    const expired = await fetch(endpoint, {
      method: 'POST', headers: { Authorization: `Bearer ${expiring}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 'expired-token', method: 'ping' }),
    });
    assert.equal(expired.status, 401);
    await expired.body?.cancel();

    const differentOwner = await connect('henry', scope);
    const reducedScope = await connect('gina', 'mcp:tools');
    const beforeRejected = groupRequests.submissions;
    for (const invalid of [differentOwner, reducedScope]) {
      await assert.rejects(continueMcpTaskGroupSubmission(invalid, renewableGroup, 2, renewable[2]),
        { code: 'rejected', status: 403 });
    }
    assert.equal(groupRequests.submissions, beforeRejected);
    await assert.rejects(stat(join(renewableGroup, 'member-2.json')), { code: 'ENOENT' });

    // The admission endpoint itself must reject the copied proof even when
    // a caller skips the SDK's credential-and-tool preflight.
    const wrongClient = await mint('gina', scope, '5m', 'a-different-oauth-client');
    const wrongs = [await mint('henry', scope), await mint('gina', 'mcp:tools'), wrongClient];
    for (const bearer of wrongs) {
      const method = 'gregale/tasks/submit-bound';
      const rejected = await fetch(endpoint, {
        method: 'POST', headers: {
          Authorization: `Bearer ${bearer}`, 'Content-Type': 'application/json',
          Accept: 'application/json, text/event-stream', 'Mcp-Method': method,
          'Mcp-Name': renewable[2].toolName, 'Mcp-Protocol-Version': '2026-07-28',
        },
        body: JSON.stringify({ jsonrpc: '2.0', id: 'bound-owner-rejection', method, params: {
          name: renewable[2].toolName, arguments: renewable[2].arguments, _meta: {
            'io.modelcontextprotocol/protocolVersion': '2026-07-28',
            'io.modelcontextprotocol/clientCapabilities': { extensions: { 'io.modelcontextprotocol/tasks': {} } },
            'io.gregale/task-submission-key': ownerPlan.members[2].submissionKey,
            'io.gregale/task-group-owner': { groupId: ownerPlan.id, binding: ownerPlan.ownerBinding },
          },
        } }),
      });
      assert.equal(rejected.status, 403);
      await rejected.body?.cancel();
    }

    const renewed = await mint('gina', scope);
    assert.notEqual(renewed, expiring);
    if (gregale) {
      const metadata = await runGroupCLI('status', renewableGroup, renewed);
      assert.deepEqual(metadata.map(entry => [entry.receiptIndex, entry.outcome, entry.task?.taskId]), [
        [0, 'available', confirmed.taskId],
        [1, 'available', lostOnRenewal],
        [2, 'not_submitted', undefined],
        [3, 'not_submitted', undefined],
      ]);
      await assert.rejects(stat(join(renewableGroup, 'member-2.json')), { code: 'ENOENT' });
    }
    const resumedRenewal = await startAgent({
      token: renewed, groupPath: renewableGroup, continuation: { receiptIndex: 2, submission: renewable[2] },
    });
    const [{ recovered: afterRenewal }] = await resumedRenewal.waitForEvents(1);
    assert.deepEqual(afterRenewal.map(entry => [entry.outcome, entry.task?.taskId]), [
      ['accepted', confirmed.taskId], ['accepted', lostOnRenewal], ['prepared', undefined],
      ['prepared', undefined],
    ]);
    resumedRenewal.child.send('continue');
    const [, { continued: admittedAfterRenewal }] = await resumedRenewal.waitForEvents(2);
    assert.deepEqual(await resumedRenewal.exit, [0, null]);
    assert.equal(admittedAfterRenewal.outcome, 'accepted');
    const renewedOwner = await connect('gina', scope);
    const replay = await continueMcpTaskGroupSubmission(renewedOwner, renewableGroup, 2, renewable[2]);
    assert.equal(replay.task.taskId, admittedAfterRenewal.task.taskId);
    const renewedTasks = (await listTaskInbox(renewedOwner)).tasks.map(task => task.taskId);
    assert.equal(renewedTasks.length, 3);
    assert.deepEqual(new Set(renewedTasks), new Set([confirmed.taskId, lostOnRenewal, replay.task.taskId]));
    assert.equal(admissionProbe.thirdKeys.size, 1);

    if (gregale) {
      const completed = await store.claim(3, 30_000);
      const paused = await store.claim(3, 30_000);
      assert.equal(completed.task_id, confirmed.taskId);
      assert.equal(paused.task_id, lostOnRenewal);
      assert.equal(await store.complete(completed.task_id, completed.lease_token, retained), 'completed');
      await requestInput(paused);
      const expiredMember = await pool.query(
        'UPDATE gregale_mcp_tasks SET expires_at = clock_timestamp() WHERE namespace = $1 AND task_id = $2',
        [schema, replay.task.taskId],
      );
      assert.equal(expiredMember.rowCount, 1);
      const beforeRead = groupRequests.submissions;
      const mixed = await runGroupCLI('status', renewableGroup, renewed);
      assert.deepEqual(mixed.map(({ receiptIndex, outcome, task }) =>
        [receiptIndex, outcome, task?.status, task?.nextAction]), [
        [0, 'available', 'completed', 'read_result'],
        [1, 'available', 'input_required', 'provide_input'],
        [2, 'unavailable', undefined, undefined],
        [3, 'not_submitted', undefined, undefined],
      ]);
      const watched = await runGroupCLI('watch', renewableGroup, renewed);
      assert.ok(watched.filter(event => event.outcome === 'available')
        .every(event => /^[0-9a-f]{64}$/.test(event.updateId)));
      assert.deepEqual(watched.map(({ updateId, ...entry }) => entry), mixed);
      assert.equal(groupRequests.submissions, beforeRead);
      await assert.rejects(stat(join(renewableGroup, 'member-3.json')), { code: 'ENOENT' });

      const cliGroup = join(groupRoot, 'cli-submission');
      const cliPlan = join(groupRoot, 'cli-submission.json');
      const submissions = ['one', 'two', 'three'].map(name => ({
        toolName: 'build_report', arguments: { report: 'CLI Task group ' + name, steps: 1 },
        deferFormInput: name !== 'two',
      }));
      await writeFile(cliPlan, JSON.stringify(submissions), { mode: 0o600 });
      const originalToken = await mint('iris', scope);
      const renewedToken = await mint('iris', scope, '6m');
      assert.notEqual(originalToken, renewedToken);
      const beforeSubmit = groupRequests.submissions;
      admissionProbe = {
        report: 'CLI Task group', committed: Promise.withResolvers(), release: Promise.withResolvers(), thirdKeys: new Set(),
      };
      const sender = spawn(gregale, groupCLIArgs('submit', cliGroup, ['--plan', cliPlan]), {
        env: groupCLIEnv(originalToken), stdio: ['ignore', 'ignore', 'inherit'],
      });
      const senderExit = once(sender, 'exit');
      agents.push({ child: sender, exit: senderExit });
      const admittedWithoutReply = await Promise.race([
        admissionProbe.committed.promise,
        senderExit.then(([code, signal]) => assert.fail(`CLI exited before admission: ${code ?? signal}`)),
      ]);
      sender.kill('SIGKILL');
      assert.deepEqual(await senderExit, [null, 'SIGKILL']);
      admissionProbe.release.resolve();

      const manifestBytes = await readFile(join(cliGroup, 'group.json'));
      const manifest = JSON.parse(manifestBytes);
      assert.equal(manifest.version, 3);
      assert.match(manifest.ownerBinding, /^[0-9a-f]{64}$/);
      assert.equal(manifest.members.length, 3);
      const lostAttempt = await readFile(join(cliGroup, 'member-1.json'));
      assert.equal(JSON.parse(lostAttempt).submissionKey, manifest.members[1].submissionKey);
      assert.equal(JSON.parse(lostAttempt).taskId, undefined);
      assert.equal(lostAttempt.includes(originalToken), false);
      assert.equal(manifestBytes.includes(submissions[1].arguments.report), false);
      await assert.rejects(stat(join(cliGroup, 'member-2.json')), { code: 'ENOENT' });
      assert.equal(groupRequests.submissions, beforeSubmit + 2);

      const cliOwner = await connect('iris', scope);
      const beforeContinue = await resumeMcpTaskGroupSubmission(cliOwner, cliGroup);
      assert.deepEqual(beforeContinue.map(entry => entry.outcome), ['accepted', 'accepted', 'prepared']);
      assert.equal(beforeContinue[1].task.taskId, admittedWithoutReply);
      const continued = await runGroupCLI('submit', cliGroup, renewedToken, '--plan', cliPlan);
      assert.deepEqual(continued.map(entry => entry.outcome), ['accepted', 'accepted', 'accepted']);
      assert.equal(continued[1].task.taskId, admittedWithoutReply);
      assert.deepEqual(await readFile(join(cliGroup, 'member-1.json')), lostAttempt);
      assert.deepEqual(await readFile(join(cliGroup, 'group.json')), manifestBytes);
      assert.equal(groupRequests.submissions, beforeSubmit + 3);

      // The SDK verifies the original request fingerprint before returning a
      // confirmed admission, even with a different current bearer.
      const alreadyAdmitted = await continueMcpTaskGroupSubmission(cliOwner, cliGroup, 0, submissions[0]);
      assert.equal(alreadyAdmitted.task.taskId, continued[0].task.taskId);
      const retainedTasks = (await listTaskInbox(cliOwner)).tasks;
      assert.equal(retainedTasks.length, 3);
      assert.deepEqual(new Set(retainedTasks.map(task => task.taskId)),
        new Set(continued.map(entry => entry.task.taskId)));
      const repeated = await runGroupCLI('submit', cliGroup, renewedToken, '--plan', cliPlan);
      assert.deepEqual(repeated, continued);
      assert.equal(groupRequests.submissions, beforeSubmit + 3);

      const reviewTasks = new Set([continued[0].task.taskId, continued[2].task.taskId]);
      for (const { task } of continued) {
        const lease = await store.claim(3, 30_000);
        assert.equal(lease.task_id, task.taskId);
        if (reviewTasks.has(task.taskId)) await requestInput(lease);
      }
      const reviewDir = join(groupRoot, 'cli-input-reviews');
      const beforeReviews = groupRequests.responses;
      const exports = await runGroupCLI('review', cliGroup, renewedToken, '--out-dir', reviewDir);
      assert.deepEqual(exports.map(entry => [entry.receiptIndex, entry.outcome, entry.taskId]), [
        [0, 'written', continued[0].task.taskId],
        [1, 'not_pending', continued[1].task.taskId],
        [2, 'written', continued[2].task.taskId],
      ]);
      assert.equal(groupRequests.responses, beforeReviews);
      assert.equal((await stat(reviewDir)).mode & 0o777, 0o700);
      const reviewFiles = [exports[0].templatePath, exports[2].templatePath];
      assert.deepEqual(reviewFiles, [join(reviewDir, 'member-0-input.json'), join(reviewDir, 'member-2-input.json')]);
      const drafts = [];
      for (const [index, path] of reviewFiles.entries()) {
        assert.equal((await stat(path)).mode & 0o777, 0o600);
        const draft = JSON.parse(await readFile(path, 'utf8'));
        assert.equal(draft.version, 2);
        assert.equal(draft.taskId, exports[index * 2].taskId);
        assert.match(draft.reviewRevision, /^[0-9a-f]{64}$/);
        assert.equal(draft.forms.approval.response, null);
        draft.forms.approval.response = { action: 'accept', content: {} };
        const edited = JSON.stringify(draft, null, 2) + '\n';
        await writeFile(path, edited);
        drafts.push(edited);
      }
      const again = await runGroupCLI('review', cliGroup, renewedToken, '--out-dir', reviewDir);
      assert.deepEqual(again.map(entry => entry.outcome), ['existing', 'not_pending', 'existing']);
      assert.deepEqual(await Promise.all(reviewFiles.map(path => readFile(path, 'utf8'))), drafts);

      const staleTask = exports[2].taskId;
      const irisReviewer = await connect('iris', scope, createMcpTaskReviewFetch());
      const originalReview = await getTaskInputReview(irisReviewer, staleTask);
      assert.equal((await respondToReviewedTaskInput(irisReviewer, originalReview, { approval: { action: 'decline' } })).outcome, 'applied');
      const nextLease = await store.claim(3, 30_000);
      assert.equal(nextLease.task_id, staleTask);
      await requestInput(nextLease, 'confirmation', 'Review the next step');
      const afterChange = await runGroupCLI('review', cliGroup, renewedToken, '--out-dir', reviewDir);
      assert.deepEqual(afterChange.map(entry => entry.outcome), ['existing', 'not_pending', 'stale_template']);
      assert.deepEqual(await Promise.all(reviewFiles.map(path => readFile(path, 'utf8'))), drafts);

      async function answerReview(taskId, path) {
        return promisify(execFile)(gregale, [
          'mcp', 'client', 'respond', '--url', endpoint.href.replace(/^http:/, 'HTTP:'),
          '--token-env', 'MCP_TASK_GROUP_FIXTURE_TOKEN',
          '--task-id', taskId, '--reviewed-input-file', path,
        ], { env: groupCLIEnv(renewedToken), timeout: 5_000 });
      }
      const beforeStaleReply = groupRequests.responses;
      await assert.rejects(answerReview(staleTask, reviewFiles[1]), error =>
        error.code !== 0 && error.stderr.includes('no longer matches the pending forms'));
      assert.equal(groupRequests.responses, beforeStaleReply);
      assert.deepEqual(Object.keys((await getTaskInputReview(irisReviewer, staleTask)).forms), ['confirmation']);

      const { stdout } = await answerReview(exports[0].taskId, reviewFiles[0]);
      const applied = JSON.parse(stdout);
      assert.deepEqual([applied.outcome, applied.taskId, applied.status], ['applied', exports[0].taskId, 'working']);
      const resumedTasks = await readWatchAndInterrupt(cliGroup, renewedToken, 3);
      assert.deepEqual(resumedTasks.map(event => [event.receiptIndex, event.task.status]), [
        [0, 'working'], [1, 'working'], [2, 'input_required'],
      ]);
      assert.deepEqual(await readFile(join(cliGroup, 'group.json')), manifestBytes);
    }
  } finally {
    admissionProbe?.release.resolve();
    stopProbe?.release.resolve();
    for (const agent of agents) agent.child.kill();
    await Promise.allSettled(agents.map(agent => agent.exit));
    taskWatch = undefined;
    await rm(groupRoot, { recursive: true, force: true });
  }
});
