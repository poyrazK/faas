import test from 'node:test';
import assert from 'node:assert/strict';
import {createExportServer, validateExportConfig, exportStep} from '../server.mjs';
import {workflowProgress} from '../public/progress.mjs';

const id = '11111111-1111-4111-8111-111111111111';
const config = {apiURL: 'https://api.example.test', appID: id, scope: 'default', definitionID: id};

async function serve(t, options) {
  const server = createExportServer(options);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {server.closeAllConnections(); server.close();});
  return `http://127.0.0.1:${server.address().port}`;
}

test('public bootstrap and browser assets expose selectors without workload or account authority', async t => {
  const base = await serve(t, {config: {...config, token: 'private', identityEndpoint: 'http://127.0.0.1/identity'}});
  const response = await fetch(base+'/config');
  assert.deepEqual(await response.json(), config);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.match(response.headers.get('content-security-policy'), /frame-ancestors 'none'/);
  for (const path of ['/', '/app.mjs', '/progress.mjs', '/style.css', '/sdk/operations.js', '/sdk/customer-operations.js', '/sdk/operation-session.js', '/sdk/operation-submission.js', '/sdk/operation-contract.js', '/sdk/sse.js']) {
    const response = await fetch(base+path); assert.equal(response.status, 200, path);
    if (path.startsWith('/sdk/')) assert.doesNotMatch(await response.text(), /from ['"]node:/);
  }
  for (const path of ['/sdk/workflow-operations-runtime.js', '/sdk/operations-runtime.js', '/artifact.mjs', '/RECOVERY.md']) assert.equal((await fetch(base+path)).status, 404);
  assert.equal((await fetch(base+'/finish', {method: 'POST', body: '{}'})).status, 503);
  assert.equal((await fetch(base+'/exports', {method: 'POST', body: '{"count":3}'})).status, 404);
});

test('health precedes definition binding and invalid public selectors fail early', async t => {
  const base = await serve(t, {config: {...config, definitionID: undefined}});
  assert.equal((await fetch(base+'/healthz')).status, 200);
  const response = await fetch(base+'/config');
  assert.equal(response.status, 503);
  assert.deepEqual(await response.json(), {code: 'export_unavailable'});
  for (const patch of [{apiURL: 'http://api.example.test'}, {appID: 'unbound'}, {scope: ''}, {definitionID: 'unbound'}]) assert.throws(() => validateExportConfig({...config, ...patch}));
});

test('workflow progress distinguishes retained prefix, uncertain finish and notification outcome', () => {
  const pending = {state: 'requires_reconciliation', progress: {stage: 'finish', completed: 2, total: 3}};
  assert.deepEqual(workflowProgress(pending).map(step => step.state), ['complete', 'complete', 'needs_review']);
  assert.deepEqual(workflowProgress({...pending, state: 'running'}).map(step => step.state), ['complete', 'complete', 'running']);
  assert.deepEqual(workflowProgress({...pending, state: 'succeeded', completion_delivery: {state: 'failed'}}).map(step => step.state), ['complete', 'complete', 'complete']);
  assert.deepEqual(workflowProgress({state: 'accepted'}).map(step => step.state), ['waiting', 'waiting', 'waiting']);
});

test('sample step bounds reject malformed rows before they become private CSV bytes', () => {
  for (const count of [0, 101, 1.5, '3']) assert.throws(() => exportStep('/collect', {count}));
  for (const rows of [[{id: 101, value: 'item-1'}], [{id: 1, value: '=formula'}], Array(101).fill({id: 1, value: 'item-1'})]) assert.throws(() => exportStep('/finish', {rows}));
  const collected = exportStep('/collect', {count: 2});
  const transformed = exportStep('/transform', collected);
  assert.deepEqual(exportStep('/finish', transformed), {rows: 2, csv: 'id,value\n1,ITEM-1\n2,ITEM-2\n'});
});
