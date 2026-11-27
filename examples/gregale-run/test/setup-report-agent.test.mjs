import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { setupReportAgent } from '../scripts/setup-report-agent.mjs';

const app = { id: '11111111-1111-4111-8111-111111111111', slug: 'report-test',
  type: 'function', runtime: 'node24', canonical_url: 'https://report-test.example.test' };
const appPath = `/v1/apps/${app.slug}`;
const endpointID = '22222222-2222-4222-8222-222222222222';
const bucketID = '33333333-3333-4333-8333-333333333333';
const deploymentID = '44444444-4444-4444-8444-444444444444';
const triggerID = '55555555-5555-4555-8555-555555555555';
const dsn = process.env.REPORT_INTAKE_TEST_DATABASE_URL;

async function fixture(t, sampleAPI) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'gregale-report-setup-test-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const bundle = path.join(dir, 'bundle');
  execFileSync(process.execPath, [fileURLToPath(new URL('../scripts/hosted-pilot.mjs', import.meta.url)), bundle, app.slug]);
  const dbSQL = await readFile(path.join(bundle, 'report-db.sql'), 'utf8');
  const state = {
    bucket: { id: bucketID, state: 'ready', public: false },
    config: new Map(), tasks: new Map(), endpoint: null, trigger: null, group: null, latest: null,
    mutations: [], deploys: [],
  };
  t.mock.method(globalThis, 'fetch', async (url, { method, body, headers, redirect }) => {
    const sampleResponse = await sampleAPI?.(url, method, body, new Headers(headers));
    if (sampleResponse) return sampleResponse;
    assert.equal(url.origin, 'https://api.example.test');
    assert.equal(headers.authorization, 'Bearer setup-test-token');
    assert.equal(redirect, 'error');
    const pathname = url.pathname;
    const data = body ? JSON.parse(body) : undefined;
    if (method === 'GET') {
      if (pathname === appPath) return Response.json(app);
      if (pathname === '/v1/executions/capabilities') {
        return Response.json({ admission_available: true, task_pipelines: true, artifact_deliveries: true,
          file_limits: { max_file_bytes: 1048576 } });
      }
      if (pathname === `${appPath}/buckets`) return Response.json({ items: [state.bucket] });
      if (pathname === `${appPath}/upload-routes`) {
        return Response.json({ items: [{ name: 'reports', bucket_id: bucketID, enabled: true, max_bytes: 32768,
          allowed_content_types: ['text/html'] }] });
      }
      if (pathname === `${appPath}/deployments/latest`) {
        return state.latest ? Response.json(state.latest) : new Response(null, { status: 404 });
      }
      if (pathname === `${appPath}/realtime/endpoints`) return Response.json(state.endpoint ? [state.endpoint] : []);
      if (pathname === `${appPath}/env-diff`) return Response.json({ rows: [...state.config.values()] });
      if (pathname === `/v1/triggers`) {
        assert.equal(url.searchParams.get('app_id'), app.id);
        assert.equal(url.searchParams.get('kind'), 'realtime');
        return Response.json(state.trigger ? [state.trigger] : []);
      }
      if (pathname === `${appPath}/realtime/endpoints/${endpointID}/channels/agent-inbox/groups`) {
        return Response.json(state.group ? [state.group] : []);
      }
    }
    if (method === 'POST' && pathname === `${appPath}/realtime/endpoints`) {
      assert.equal(state.endpoint, null);
      const { callback_auth_token: callbackToken, ...endpoint } = data;
      assert.match(callbackToken, /^[0-9a-f]{64}$/);
      state.mutations.push('endpoint');
      state.endpoint = { ...endpoint, id: endpointID, app_id: app.id };
      return Response.json(state.endpoint);
    }
    if (method === 'PUT' && pathname.startsWith(`${appPath}/`)) {
      const [resource, key] = pathname.slice(appPath.length + 1).split('/');
      assert.ok(!state.config.has(key));
      state.mutations.push(key);
      if (resource === 'secrets') {
        assert.equal(key, 'DATABASE_URL');
        assert.deepEqual(data, { value: 'postgres://example.test/report', secret_class: 'ephemeral' });
      } else {
        assert.equal(resource, 'env');
      }
      state.config.set(key, { key, kind: resource === 'secrets' ? 'secret' : 'env',
        cells: { default: resource === 'secrets' ? { present: true } : { present: true, value: data.value } } });
      return Response.json({ key });
    }
    if (method === 'POST' && pathname === '/v1/run-tasks') {
      assert.deepEqual(data.agent_endpoint_ids, [endpointID]);
      assert.equal(data.definition.limits.timeout_ms, 20_000);
      const prior = state.tasks.get(data.name);
      if (prior) assert.deepEqual(data, prior);
      else {
        state.mutations.push(data.name);
        state.tasks.set(data.name, data);
      }
      return Response.json({ name: data.name, version: 1, agent_endpoint_ids: [endpointID], status: 'active',
        digest: `sha256:${data.name}` }, { status: 201 });
    }
    assert.fail(`unexpected ${method} ${url}`);
  });
  let locked = false;
  const options = {
    app: app.slug, route: 'reports', prefix: 'pilot', apiURL: 'https://api.example.test',
    token: 'setup-test-token', databaseURL: 'postgres://example.test/report', bundle,
    db: { async query(sql, params) {
      if (sql.includes('pg_try_advisory_lock')) {
        assert.deepEqual(params, [`report-starter:${app.id}`]);
        assert.equal(locked, false);
        locked = true;
        return { rows: [{ locked: true }] };
      }
      if (sql.includes('pg_advisory_unlock')) {
        assert.equal(locked, true);
        locked = false;
        return { rows: [] };
      }
      assert.equal(locked, true);
      assert.equal(sql, dbSQL);
      assert.match(sql, /CREATE TABLE IF NOT EXISTS report_requests/);
      return { rows: [] };
    } },
    async deploy(key) {
      assert.equal(state.config.get('REPORT_ENDPOINT_ID').cells.default.value, endpointID);
      assert.equal(state.tasks.size, 2);
      state.deploys.push(key);
      state.latest = { id: deploymentID, reason: key, status: 'live', traffic_percent: 100 };
      state.trigger = { id: triggerID, slug: 'agent-inbox', enabled: true, delivery: 'record', batch_size_max: 1,
        config: { endpoint_id: endpointID, channel: 'agent-inbox', group: 'slices', max_concurrency: 4 } };
      state.group = { name: 'slices', lease_seconds: 120, ordering_metadata_key: 'thread_id',
        deduplication_metadata_key: 'message_id', dead_letter_policy: 'block', trigger_id: triggerID };
    },
  };
  return { options, state };
}

test('starter resumes after a lost deployment acknowledgement without creating new resources', async t => {
  const { options, state } = await fixture(t);
  await assert.rejects(setupReportAgent({ ...options, deploy: async key => {
    await options.deploy(key);
    throw new Error('deployment acknowledgement lost');
  } }), /acknowledgement lost/);
  const finished = await setupReportAgent(options);
  const repeated = await setupReportAgent(options);
  assert.equal(finished.deployment_id, deploymentID);
  assert.deepEqual(finished.tasks.map(task => task.name), ['pilot-sales', 'pilot-merge']);
  assert.deepEqual(repeated.tasks, finished.tasks);
  assert.equal(state.deploys.length, 1);
  assert.deepEqual(state.mutations, ['endpoint', 'DATABASE_URL', 'REPORT_APP', 'REPORT_ENDPOINT_ID', 'REPORT_UPLOAD_ROUTE',
    'REPORT_TASK_PREFIX', 'GREGALE_API_URL', 'pilot-sales', 'pilot-merge']);
});

test('starter refuses a publicly served bucket and a conflicting live inbox', async t => {
  const { options, state } = await fixture(t);
  state.bucket.serve_at = 'reports.example.test';
  await assert.rejects(setupReportAgent(options), /ready private app bucket/);
  assert.deepEqual(state.mutations, []);
  delete state.bucket.serve_at;
  const separateAPI = { key: 'GREGALE_API_URL', kind: 'env',
    cells: { default: { present: true, value: 'http://relay.example.test' } } };
  state.config.set(separateAPI.key, separateAPI);
  await setupReportAgent(options);
  assert.equal(state.config.get(separateAPI.key), separateAPI);
  state.group.ordering_metadata_key = 'customer_id';
  await assert.rejects(setupReportAgent(options), /conflicting slices consumer group/);
  assert.equal(state.deploys.length, 1);
});

test('verification downloads the expected private report and reuses the same CSV request', { skip: !dsn }, async t => {
  const require = createRequire(new URL('../../../cmd/gregale/templates/agent-stateless-node/package.json', import.meta.url));
  const { Client } = require('pg');
  const db = new Client({ connectionString: dsn });
  await db.connect();
  const schema = `report_setup_${randomUUID().replaceAll('-', '')}`;
  t.after(async () => {
    try { await db.query(`DROP SCHEMA ${schema} CASCADE`); }
    finally { await db.end(); }
  });
  await db.query(`CREATE SCHEMA ${schema}`);
  await db.query(`SET search_path=${schema}`);
  const requestID = randomUUID(), fileID = randomUUID(), deliveryID = randomUUID();
  const receiptURL = `${appPath}/run-artifact-deliveries/reports/${deliveryID}`;
  const signedURL = 'https://storage.example.test/report?signature=private-test-value';
  const summary = { orders: 4, units: 8, revenue_cents: 6996, currency: 'USD' };
  let publications = 0, downloads = 0;
  const { options, state } = await fixture(t, async (url, method, body, headers) => {
    if (url.href === signedURL) {
      assert.equal(headers.get('authorization'), null);
      assert.equal(headers.get('x-storage-binding'), 'private-download');
      downloads++;
      return new Response('<!doctype html><p>4 orders · 8 units · 6996 USD cents</p>', {
        headers: { 'content-type': 'application/octet-stream', 'content-disposition': 'attachment' },
      });
    }
    if (!/execution-files|retained-messages|download-url/.test(url.pathname)) return;
    assert.equal(headers.get('authorization'), 'Bearer setup-test-token');
    const data = JSON.parse(body ?? '{}');
    if (url.pathname === '/v1/execution-files') return Response.json({ id: fileID, status: 'ready' });
    if (url.pathname === `/v1/execution-files/${fileID}/agent-grants`) {
      assert.equal(data.task_name, 'pilot-sales');
      assert.equal(data.step_label, 'csv-report');
      return Response.json({ grant_token: 'agf1.setup-verification', expires_at: new Date(Date.now() + 300_000).toISOString() });
    }
    if (url.pathname.endsWith('/agent-inbox/retained-messages')) {
      const threadID = data.metadata.thread_id;
      assert.deepEqual(JSON.parse(Buffer.from(data.data_base64, 'base64')), { upload_ref: requestID });
      const { rowCount } = await db.query('SELECT 1 FROM report_upload_grants WHERE thread_id=$1', [threadID]);
      assert.equal(rowCount, 1);
      publications++;
      await db.query(`INSERT INTO agent_messages (thread_id, message_id, role, content)
        VALUES ($1, 'done', 'tool', $2), ($1, 'assistant:done', 'assistant', '{"final":true}')`, [
        threadID, { report: { outcome: 'succeeded', summary, delivery: { id: deliveryID, status: 'completed', receipt_url: receiptURL } } },
      ]);
      return Response.json({ ok: true });
    }
    if (url.pathname === `${receiptURL}/download-url`) {
      assert.equal(method, 'POST');
      return Response.json({ url: signedURL, method: 'GET', headers: { 'x-storage-binding': 'private-download' } });
    }
    assert.fail(`unexpected sample request ${method} ${url.pathname}`);
  });
  for (let attempt = 0; attempt < 2; attempt++) {
    const { verification } = await setupReportAgent({ ...options, db, requestID });
    const { elapsed_ms, ...result } = verification;
    assert.ok(elapsed_ms >= 0);
    assert.deepEqual(result, { request_id: requestID, ...summary });
  }
  assert.equal(publications, 1);
  assert.equal(downloads, 2);
  assert.equal(state.deploys.length, 1);
});
