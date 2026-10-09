import test from 'node:test';
import assert from 'node:assert/strict';
import {finishExport} from '../artifact.mjs';
import {exportStep, createWorkflowExportServer} from '../server.mjs';
import {GregaleWorkflowOperations} from '@gregale/sdk-node/operations/runtime';

const id = '11111111-1111-4111-8111-111111111111', run = '22222222-2222-4222-8222-222222222222';
const artifactID = '33333333-3333-4333-8333-333333333333';
const headers = {'X-Gregale-Customer-Operation-Id': id, 'X-Gregale-Operation-Execution-Kind': 'workflow',
  'X-Gregale-Operation-Workflow-Run-Id': run, 'X-Gregale-Operation-Workflow-Step': 'finish',
  'X-Gregale-Operation-Generation': '1', 'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Workflow-Capability': artifactID};
const input = exportStep('/finish', exportStep('/transform', exportStep('/collect', {count: 3})));

test('final action uploads bounded CSV without a writer and returns its verified artifact reference', async () => {
  let uploaded;
  const runtime = {runCancellableRequest: (proof, handler) => { assert.equal(proof, headers); return handler({checkpoint: async () => {}, throwIfStopped() {}}); },
    context: () => ({id, step: 'finish'}), uploadArtifact: async declaration => {uploaded = declaration; return {id: artifactID};}};
  assert.deepEqual(await finishExport(runtime, headers, input), {rows: 3, artifact_id: artifactID});
  assert.deepEqual(uploaded, {report_id: 'export-csv', name: 'export.csv', data: 'id,value\n1,ITEM-1\n2,ITEM-2\n3,ITEM-3\n', maxBytes: 4096});
  await assert.rejects(finishExport({...runtime, context: () => ({step: 'collect'})}, headers, input), /Trusted final/);
});

test('installed SDK recovers a lost upload reply and reuses private bytes on approved resume', async () => {
  let artifact, writes = 0, lookups = 0;
  const deadline = new Date(Date.now()+60000).toISOString();
  const runtime = new GregaleWorkflowOperations({apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (raw, init) => {
    const url = new URL(String(raw)), proof = new Headers(init?.headers);
    if (url.hostname === '127.0.0.1') return Response.json({access_token: 'workload'});
    assert.equal(proof.get('X-Faas-Invocation-Id'), null); assert.equal(init.credentials, 'omit');
    if (url.pathname.endsWith('/control')) return Response.json({operation_id: id, workflow_run_id: run, workflow_step: 'finish',
      generation: Number(proof.get('X-Gregale-Operation-Generation')), attempt: Number(proof.get('X-Gregale-Operation-Attempt')),
      cancellation_requested: false, observed_at: new Date().toISOString(), deadline_at: deadline, lease_expires_at: deadline, poll_after_ms: 1000});
    if (url.pathname.endsWith('/artifact-upload-receipts')) {lookups++; return Response.json(artifact ? {available: true, artifact} : {available: false});}
    writes++; assert.equal(Buffer.from(init.body).toString(), input.csv);
    artifact = {id: artifactID, uri: `operation://${id}/artifacts/${artifactID}`, name: url.searchParams.get('name'), size_bytes: Buffer.byteLength(input.csv), sha256: url.searchParams.get('sha256')};
    throw new TypeError('lost upload acknowledgement');
  }});
  assert.deepEqual(await finishExport(runtime, headers, input), {rows: 3, artifact_id: artifactID});
  const resumed = {...headers, 'X-Gregale-Operation-Generation': '2', 'X-Gregale-Operation-Attempt': '2'};
  assert.deepEqual(await finishExport(runtime, resumed, input), {rows: 3, artifact_id: artifactID});
  assert.equal(writes, 1); assert.equal(lookups, 3);
});

test('cancellation interrupts direct transfer and prevents a successful final result', async () => {
  let controls = 0, writes = 0;
  const deadline = new Date(Date.now()+60000).toISOString();
  const runtime = new GregaleWorkflowOperations({apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (raw, init) => {
    const url = new URL(String(raw));
    if (url.hostname === '127.0.0.1') return Response.json({access_token: 'workload'});
    if (url.pathname.endsWith('/control')) return Response.json({operation_id: id, workflow_run_id: run, workflow_step: 'finish', generation: 1, attempt: 1,
      cancellation_requested: ++controls > 3, observed_at: new Date().toISOString(), deadline_at: deadline, lease_expires_at: deadline, poll_after_ms: 100});
    if (url.pathname.endsWith('/artifact-upload-receipts')) return Response.json({available: false});
    writes++;
    return new Promise((_resolve,reject) => init.signal.addEventListener('abort', () => reject(init.signal.reason), {once: true}));
  }});
  await assert.rejects(finishExport(runtime, headers, input), error => error.code === 'cancellation_requested');
  assert.equal(writes, 1);
});

test('server health works before workload identity is configured and invalid workflow requests fail', async t => {
  const server = createWorkflowExportServer({config: {apiURL: 'https://api.example.test', appID: id, scope: 'default', definitionID: id}, runtime: {runCancellableRequest: async () => {throw new Error('Missing trusted claim');}}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {server.closeAllConnections(); server.close();});
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.equal((await fetch(base+'/healthz')).status, 200);
  assert.equal((await fetch(base+'/finish', {method: 'POST', body: '{}'})).status, 400);
  assert.equal((await fetch(base+'/exports', {method: 'POST', body: '{}'})).status, 404);
});
