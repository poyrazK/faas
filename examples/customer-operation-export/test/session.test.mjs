// ADR-521: history replaces a customer-owned operation ID table.
import test from 'node:test';
import assert from 'node:assert/strict';
import {ExportSession} from '../public/session.mjs';
import {generateExport} from '../export.mjs';
import {createExportServer} from '../server.mjs';
const id = '11111111-1111-1111-1111-111111111111';
const settings = {appID: id, scope: 'default', definitionID: id};
const snapshot = {id, state: 'succeeded', generation: 1, latest_sequence: 6, result: {csv: 'id,value\n1,10\n'}, completion_delivery: {state: 'failed', attempts: 2}};

test('new session discovers retained export and downloads without starting work', async () => {
  let starts = 0, selected;
  const client = {list: async opts => { assert.equal(opts.scope, 'default'); return {operations: [snapshot]}; }, get: async key => { assert.equal(key, id); return snapshot; }, start: () => { starts++; }, async *subscribe() {}};
  const session = new ExportSession({...settings, client, onChange: update => { if (update.operation) selected = update.operation; }});
  const history = await session.history(); await session.open(history[0].id);
  const download = await session.download();
  assert.equal(await download.blob.text(), snapshot.result.csv); assert.equal(selected.state, 'succeeded'); assert.equal(selected.completion_delivery.state, 'failed'); assert.equal(starts, 0);
  session.close();
});

test('lost submission response retries the same key and input, double clicks share one submission', async () => {
  const keys = []; let resolve;
  const client = {start: async (_definition, input, key) => { assert.equal(input.count, 1); keys.push(key); if (keys.length === 1) throw Error('connection lost'); return new Promise(r => { resolve = r; }); }, get: async () => snapshot, list: async () => ({operations: [snapshot]}), async *subscribe() {}};
  const session = new ExportSession({...settings, client});
  await assert.rejects(session.start(1), /connection lost/); await assert.rejects(session.start(2), /same input/);
  const first = session.start(1), second = session.start(1); assert.equal(first, second);
  resolve({id}); await first; assert.equal(keys.length, 2); assert.equal(keys[0], keys[1]); session.close();
});

test('signout aborts old streams and prevents delayed submission from opening work', async () => {
  let finish, reads = 0;
  const client = {start: async () => new Promise(resolve => { finish = resolve; }), get: async () => { reads++; return snapshot; }};
  const session = new ExportSession({...settings, client}); const pending = session.start(1);
  session.close(); finish({id}); await assert.rejects(pending, /Session closed/); assert.equal(reads, 0);
});

test('accepted submission stays accepted when the follow-up status read fails', async () => {
  let starts = 0, notice;
  const client = {start: async () => { starts++; return {id}; }, get: async () => { throw Error('status temporarily unavailable'); }};
  const session = new ExportSession({...settings, client, onChange: update => { notice = update.error; }});
  assert.deepEqual(await session.start(1), {id}); assert.equal(starts, 1); assert.match(notice, /Export accepted/); session.close();
});

test('signout fences a late download even when a transport ignores abort', async () => {
  let finish;
  const session = new ExportSession({...settings, client: {get: async () => new Promise(resolve => { finish = resolve; })}});
  session.selected = id; const download = session.download(); session.close(); finish(snapshot);
  await assert.rejects(download, /Session changed/);
});

test('ordinary handler uses trusted context and rejects invalid input', async () => {
  const reports = []; const runtime = {runCancellableRequest: (_headers, handler) => handler({signal: new AbortController().signal, throwIfStopped() {}}), context: () => ({id}), progress: async report => reports.push(report)};
  assert.deepEqual(await generateExport(runtime, {}, {count: 2}), {csv: 'id,value\n1,10\n2,20\n', rows: 2});
  assert.deepEqual(reports.map(r => r.report_id), ['generating']);
  await assert.rejects(generateExport(runtime, {}, {count: 1001}));
  await assert.rejects(generateExport({...runtime, context: () => undefined}, {}, {count: 1}), /Trusted/);
});

test('sample server exposes only public selectors and bounded allowlisted assets', async t => {
  const server = createExportServer({config: {...settings, apiURL: 'https://api.example.com'}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.deepEqual(await (await fetch(base+'/config')).json(), {...settings, apiURL: 'https://api.example.com'});
  const page = await fetch(base+'/'); assert.equal(page.status, 200); assert.equal(page.headers.get('cache-control'), 'no-store'); assert.match(await page.text(), /Your exports/);
  assert.equal((await fetch(base+'/sdk/operations-runtime.js')).status, 404);
  const sdk = await fetch(base+'/sdk/operations.js'); assert.equal(sdk.status, 200);
  assert.match(await sdk.text(), /export class GregaleOperationClient/);
  assert.equal((await fetch(base+'/exports', {method: 'POST', body: '{}'})).status, 503);
});
