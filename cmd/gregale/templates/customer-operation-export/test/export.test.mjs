import test from 'node:test';
import assert from 'node:assert/strict';
import {generateExport} from '../export.mjs';
import {createExportServer} from '../server.mjs';
import {GregaleOperationClient, GregaleOperationSession} from '@gregale/sdk-node/operations';
import {GregaleOperations} from '@gregale/sdk-node/operations/runtime';

const id = '11111111-1111-4111-8111-111111111111';
const config = {apiURL: 'https://api.example.test', appID: id, scope: 'default', definitionID: id};

test('handler uses a trusted claim and completes small exports with one progress report', async () => {
  const reports = [];
  const runtime = {runCancellableRequest: (_headers, handler) => handler({signal: new AbortController().signal, throwIfStopped() {}, async checkpoint() {}}), context: () => ({id}), progress: async report => reports.push(report), uploadArtifact: async input => { assert.equal(input.data, 'id,value\n1,10\n2,20\n'); return {id}; }};
  assert.deepEqual(await generateExport(runtime, {}, {count: 2}), {artifact_id: id, rows: 2});
  assert.equal(reports.length, 1); assert.equal(reports[0].stage, 'generating');
  await assert.rejects(generateExport({...runtime, context: () => undefined}, {}, {count: 1}), /Trusted/);
  await assert.rejects(generateExport(runtime, {}, {count: 1001}), /Invalid/);
});

test('installed SDK provides isolated browser and workload entry points', () => {
  assert.equal(typeof GregaleOperationClient, 'function');
  assert.equal(typeof GregaleOperationSession, 'function');
  assert.equal(typeof GregaleOperations, 'function');
});

test('file export uses installed direct upload and recovers a lost acknowledgement without repeating generation', async () => {
  const invocation = '22222222-2222-4222-8222-222222222222', artifactID = '33333333-3333-4333-8333-333333333333';
  const reports = []; let stored, artifact, uploads = 0, lookups = 0;
  const deadline = new Date(Date.now()+60000).toISOString();
  const runtime = new GregaleOperations({apiURL: config.apiURL, identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    const path = new URL(url);
    if (path.hostname === '127.0.0.1') return Response.json({access_token: 'workload'});
    if (path.pathname.endsWith('/control')) return Response.json({operation_id: id, invocation_id: invocation, attempt: 1, cancellation_requested: false,
      observed_at: new Date().toISOString(), deadline_at: deadline, lease_expires_at: deadline, poll_after_ms: 1000});
    if (path.pathname.endsWith('/artifact-upload-receipts')) { lookups++; return Response.json(artifact ? {available: true, artifact} : {available: false}); }
    if (path.pathname.endsWith('/artifact-uploads')) {
      uploads++; stored = Buffer.from(init.body);
      assert.equal(stored.toString(), 'id,value\n1,10\n2,20\n');
      assert.equal(path.searchParams.get('size_bytes'), String(stored.length));
      artifact = {id: artifactID, name: 'export.csv', uri: `operation://${id}/artifacts/${artifactID}`, size_bytes: stored.length, sha256: path.searchParams.get('sha256')};
      throw new TypeError('lost private-upload acknowledgement');
    }
    reports.push(JSON.parse(init.body)); return Response.json({id});
  }});
  const headers = {'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
    'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Capability': 'a'.repeat(64)};
  assert.deepEqual(await generateExport(runtime, headers, {count: 2}), {artifact_id: artifactID, rows: 2});
  assert.equal(reports.length, 1); assert.equal(uploads, 1); assert.equal(lookups, 2);
  assert.match(artifact.sha256, /^sha256:[0-9a-f]{64}$/);
});

test('cancelling during direct upload interrupts transfer and leaves the result unconfirmed', async () => {
  const invocation = '22222222-2222-4222-8222-222222222222';
  const deadline = new Date(Date.now()+60000).toISOString();
  let controls = 0, writes = 0;
  const runtime = new GregaleOperations({apiURL: config.apiURL, identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    if (new URL(url).hostname === '127.0.0.1') return Response.json({access_token: 'workload'});
    if (String(url).endsWith('/control')) return Response.json({operation_id: id, invocation_id: invocation, attempt: 1, cancellation_requested: ++controls > 2,
      observed_at: new Date().toISOString(), deadline_at: deadline, lease_expires_at: deadline, poll_after_ms: 100});
    if (String(url).endsWith('/artifact-upload-receipts')) return Response.json({available: false});
    if (new URL(url).pathname.endsWith('/artifact-uploads')) {
      writes++;
      return new Promise((_resolve, reject) => init.signal.addEventListener('abort', () => reject(init.signal.reason), {once: true}));
    }
    return Response.json({id});
  }});
  const headers = {'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
    'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Capability': 'a'.repeat(64)};
  await assert.rejects(generateExport(runtime, headers, {count: 1}), error => error.code === 'cancellation_requested');
  assert.equal(writes, 1);
});

test('browser assets and public selectors work with the installed SDK', async t => {
  const server = createExportServer({config: {...config, token: 'private'}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.deepEqual(await (await fetch(base+'/config')).json(), config);
  for (const path of ['/', '/app.mjs', '/style.css', '/sdk/operations.js', '/sdk/customer-operations.js', '/sdk/operation-session.js', '/sdk/operation-submission.js', '/sdk/sse.js']) {
    const response = await fetch(base+path); assert.equal(response.status, 200, path);
    const text = await response.text(); assert.ok(text.length > 0);
    if (path.startsWith('/sdk/')) assert.doesNotMatch(text, /from ['"]node:/);
  }
  assert.equal((await fetch(base+'/sdk/operations-runtime.js')).status, 404);
  assert.equal((await fetch(base+'/exports', {method: 'POST', body: '{"count":1}'})).status, 503);
});

test('first deployment can serve health before its immutable definition is configured', async t => {
  const server = createExportServer({config: {...config, definitionID: undefined}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.equal((await fetch(base+'/healthz')).status, 200);
  const bootstrap = await fetch(base+'/config'); assert.equal(bootstrap.status, 503);
  assert.deepEqual(await bootstrap.json(), {code: 'export_unavailable'});
});
