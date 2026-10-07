import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {GregaleOperationClient, GregaleOperationSession, createBrowserOperationReceiptStore} from '@gregale/sdk-node/operations';
import {runExportJob} from '../job.mjs';
import {generateExport} from '../export.mjs';
import {createExportServer} from '../server.mjs';
import {MAX_EXPORT_BYTES} from '../contract.mjs';
import {fixture, ids, config, env} from './fixture.mjs';

const run = f => runExportJob({apiURL: config.apiURL, env, fetchImpl: f.fetchImpl});
function receiptStore() {
  const values = new Map(); let pending = Promise.resolve();
  return createBrowserOperationReceiptStore({storage: {getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key)},
    locks: {request: (_key, action) => { const result = pending.then(action); pending = result.catch(() => {}); return result; }}});
}
const session = (f, store) => new GregaleOperationSession({...config, name: 'customer-export', receiptStore: store,
  client: new GregaleOperationClient({apiURL: config.apiURL, credential: () => 'customer-token', fetch: f.fetchImpl})});

test('installed Job SDK uploads once, prepares private files and replays lost acknowledgements', async t => {
  const f = await fixture(t, {lostPreparation: true, lostResult: true});
  const result = await run(f);
  assert.deepEqual(result, {rows: 2, artifact_id: ids.artifact});
  assert.equal(f.state.writes, 1); assert.equal(f.state.receipts, 2); assert.equal(f.state.preparations.length, 1);
  assert.deepEqual(f.state.results[0], f.state.results[1]); assert.equal(f.state.progress.length, 1);
  assert.equal(f.state.retained.toString(), 'id,value\n1,10\n2,20\n');
  assert.equal(f.state.artifact.uri, `operation://${ids.operation}/artifacts/${ids.artifact}`);
  assert.equal(f.snapshot().state, 'running'); assert.equal(f.snapshot().result, undefined); assert.equal(f.snapshot().artifacts, undefined);
  const client = new GregaleOperationClient({apiURL: config.apiURL, credential: () => 'customer-token', fetch: f.fetchImpl});
  await assert.rejects(client.download(ids.operation, ids.artifact), /404/);
  f.confirm(); f.state.delivery = {state: 'failed', attempts: 2};
  assert.equal(await (await client.download(ids.operation, ids.artifact)).text(), f.state.retained.toString());
  const other = new GregaleOperationClient({apiURL: config.apiURL, credential: () => 'other-customer', fetch: f.fetchImpl});
  await assert.rejects(other.download(ids.operation, ids.artifact), /404/);
});

test('browser duplicate clicks and lost acceptance keep one submission identity; reload restores it', async t => {
  const f = await fixture(t, {lostSubmission: true}), store = receiptStore();
  const first = session(f, store); t.after(() => first.close());
  await assert.rejects(first.start({count: 2}), /acknowledgement lost/);
  const accepted = first.start({count: 2}); assert.equal(accepted, first.start({count: 2})); await accepted;
  assert.equal(f.state.submissions.length, 1); // Durable receipt lookup discovers the lost acceptance.
  await run(f); f.confirm(); f.state.delivery = {state: 'failed', attempts: 2}; first.close();
  const reopened = session(f, store); t.after(() => reopened.close());
  assert.equal((await reopened.resume()).state, 'accepted');
  await reopened.history();
  const completed = await reopened.result(); assert.equal(completed.state, 'succeeded'); assert.equal(completed.completion_delivery.state, 'failed');
  assert.equal((await reopened.download(completed.result.artifact_id)).blob.size, f.state.retained.length);
  const writes = f.state.writes, submissions = f.state.submissions.length;
  f.state.delivery = {state: 'succeeded', attempts: 3}; await reopened.refresh();
  assert.equal((await reopened.result()).state, 'succeeded'); assert.equal(f.state.writes, writes); assert.equal(f.state.submissions.length, submissions);
});

test('cancellation before entry produces no upload or success receipt', async t => {
  const f = await fixture(t, {cancelled: true}); await assert.rejects(run(f));
  assert.equal(f.state.writes, 0); assert.equal(f.state.results.length, 0);
});

test('an interrupted platform transfer retries bytes without repeating business work', async t => {
  const f = await fixture(t, {failTransfer: true});
  assert.deepEqual(await run(f), {rows: 2, artifact_id: ids.artifact});
  assert.equal(f.state.writes, 2); assert.equal(f.state.progress.length, 1);
  assert.deepEqual(f.state.preparations[0], f.state.preparations[1]);
});

test('an existing direct upload receipt is reused without sending bytes', async t => {
  const f = await fixture(t), bytes = Buffer.from('id,value\n1,10\n2,20\n');
  f.state.artifact = {id: ids.artifact, name: 'export.csv', uri: `operation://${ids.operation}/artifacts/${ids.artifact}`, size_bytes: bytes.length, sha256: 'sha256:' + createHash('sha256').update(bytes).digest('hex')};
  assert.deepEqual(await run(f), {rows: 2, artifact_id: ids.artifact});
  assert.equal(f.state.writes, 0); assert.equal(f.state.preparations.length, 0);
});

test('invalid input fails before progress or uploads', async t => {
  const f = await fixture(t);
  for (const input of [{count: 1001}, {count: 0}, {count: 1, extra: true}]) {
    await assert.rejects(runExportJob({apiURL: config.apiURL, env: {...env, GREGALE_CUSTOMER_OPERATION_INPUT: JSON.stringify(input)}, fetchImpl: f.fetchImpl}), /Invalid export input/);
  }

  assert.equal(f.state.progress.length, 0); assert.equal(f.state.writes, 0);
});

test('maximum export yields and checks authority between bounded chunks', async () => {
  let checkpoints = 0, prepared;
  const scope = {operation: {appID: ids.app, id: ids.operation, platformTenantID: ids.tenant, runID: ids.run, generation: 1}, signal: new AbortController().signal,
    throwIfStopped() {}, async checkpoint() { checkpoints++; }, async progress() {}, async uploadArtifact(report) { prepared = report; return {id: ids.artifact}; }};
  assert.equal((await generateExport({count: 1000}, scope)).rows, 1000);
  assert.equal(checkpoints, 11); assert.ok(Buffer.byteLength(prepared.data) <= MAX_EXPORT_BYTES);
  assert.ok(prepared.data.endsWith('1000,10000\n'));
});

test('native control denies mismatched ownership and stale generation before upload', async t => {
  const f = await fixture(t);
  for (const override of [{app_id: ids.account}, {scope: 'other'}, {generation: 2}, {platform_tenant_id: 'invalid'}]) {
    f.state.controlOverride = override; await assert.rejects(run(f));
  }
  assert.equal(f.state.writes, 0); assert.equal(f.state.results.length, 0);
});

test('public server exposes only selectors and browser assets, and waits for deployment setup', async t => {
  const f = await fixture(t);
  assert.deepEqual(await (await fetch(f.base + 'config')).json(), config);
  for (const path of ['job.mjs', 'upload.mjs', 'contract.mjs', 'package.json', 'sdk/job-operations-runtime.js', 'sdk/operations-runtime.js']) {
    assert.equal((await fetch(f.base + path)).status, 404);
  }
  assert.equal((await fetch(f.base + 'exports', {method: 'POST', body: '{"count":2}'})).status, 404);
  const unconfigured = createExportServer({config: {...config, definitionID: undefined}});
  await new Promise(resolve => unconfigured.listen(0, '127.0.0.1', resolve));
  t.after(() => { unconfigured.closeAllConnections(); unconfigured.close(); });
  const base = `http://127.0.0.1:${unconfigured.address().port}`;
  assert.equal((await fetch(base + '/healthz')).status, 200); assert.equal((await fetch(base + '/config')).status, 503);
});

test('export memory bound and private API ownership stay outside browser configuration', async t => {
  const f = await fixture(t);
  const publicJSON = JSON.stringify(await (await fetch(f.base + 'config')).json());
  assert.ok(!/capability|storage|bucket|upload/i.test(publicJSON));
  assert.equal((await fetch(f.base + '_job/export', {method: 'POST', body: 'csv'})).status, 404);
  assert.ok(MAX_EXPORT_BYTES < 1024 * 1024);
});
