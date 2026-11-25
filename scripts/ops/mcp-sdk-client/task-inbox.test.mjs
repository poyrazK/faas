import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import test from 'node:test';
import { pathToFileURL } from 'node:url';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskSubmissionFetch, listTaskInbox, lookupMcpTaskBySubmissionKey, submitMcpTask, watchTaskInbox,
} from '../../../sdk/node/dist/preview.js';

test('the official SDK watches the authenticated PostgreSQL Task inbox', { timeout: 25_000 }, async t => {
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
  const mint = (subject, scope) => new SignJWT({ scope, client_id: clientId }).setProtectedHeader({ alg: 'ES256' })
    .setIssuer(auth.issuer).setAudience(auth.resource).setSubject(subject).setExpirationTime('5m').sign(privateKey);
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
  const server = createServer((req, res) => {
    if (req.headers['mcp-method'] === 'gregale/tasks/inbox') response = res;
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
  async function connect(subject, scope) {
    const token = await mint(subject, scope);
    const client = new Client({ name: 'gregale-inbox-sdk-interop', version: '1.0.0' }, {
      versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
    });
    clients.push(client);
    await client.connect(new StreamableHTTPClientTransport(endpoint, {
      requestInit: { headers: { Authorization: `Bearer ${token}` } }, onInsufficientScope: 'throw',
      fetch: createMcpTaskSubmissionFetch(),
    }));
    return client;
  }

  const owner = await connect('alice', 'mcp:tools reports:read');
  async function pause(subject) {
    const principal = authInfo(subject);
    const created = await store.create({
      toolName: 'build_report', handlerVersion: mcpTaskHandlers.build_report.version, args: { report: 'private SDK fixture', steps: 1 },
      authInfo: principal, authMode: 'external-oauth', inputMethods: ['elicitation/create:form'],
    });
    const claim = await store.claim(3, 30_000);
    assert.equal(claim.task_id, created.task_id);
    assert.equal((await store.requestInputs({
      taskID: claim.task_id, leaseToken: claim.lease_token,
      requests: { approval: { method: 'elicitation/create', params: {
        mode: 'form', message: 'Private SDK fixture prompt', requestedSchema: { type: 'object', properties: {} },
      } } },
    })).ready, false);
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
});
