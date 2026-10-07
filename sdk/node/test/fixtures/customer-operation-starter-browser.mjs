import assert from 'node:assert/strict';
import {cpSync, mkdirSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {execFileSync} from 'node:child_process';
import {chromium} from 'playwright-core';

export const appID = '22222222-2222-4222-8222-222222222222';
export const definitionID = '33333333-3333-4333-8333-333333333333';
export const artifactID = '44444444-4444-4444-8444-444444444444';
export const replacementID = '99999999-9999-4999-8999-999999999999';
export const csv = 'id,value\n1,ITEM-1\n2,ITEM-2\n3,ITEM-3\n';
const accountID = '55555555-5555-4555-8555-555555555555';
const owners = {alice: '66666666-6666-4666-8666-666666666666', bob: '77777777-7777-4777-8777-777777777777'};
const prefix = '/v1/platform-tenant-self/customer-operations';

export async function browserStarter(t, template = 'customer-operation-workflow-export') {
  const temp = mkdtempSync(join(tmpdir(), `gregale-${template}-browser-`));
  t.after(() => rmSync(temp, {recursive: true, force: true}));
  const sdk = fileURLToPath(new URL('../../', import.meta.url));
  const source = fileURLToPath(new URL(`../../../../cmd/gregale/templates/${template}/`, import.meta.url));
  const dest = join(temp, 'feature'); cpSync(source, dest, {recursive: true}); mkdirSync(join(dest, 'packages'));
  const [packed] = JSON.parse(execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temp], {cwd: sdk, encoding: 'utf8', timeout: 30000}));
  cpSync(join(temp, packed.filename), join(dest, 'packages/gregale-sdk.tgz'));
  const childEnv = {...process.env}; delete childEnv.NODE_TEST_CONTEXT;
  execFileSync('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], {cwd: dest, env: childEnv, timeout: 60000, stdio: 'pipe'});
  const config = {apiURL: 'https://api.example.test', appID, definitionID, scope: 'default'};
  const {createExportServer} = await import(pathToFileURL(join(dest, 'server.mjs')));
  const server = createExportServer({config});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {server.closeAllConnections(); server.close();});
  const browser = await chromium.launch({headless: true, ...(process.env.GREGALE_OPERATION_CHROMIUM ? {executablePath: process.env.GREGALE_OPERATION_CHROMIUM} : {})});
  t.after(() => browser.close());
  const context = await browser.newContext({acceptDownloads: true});
  t.after(() => context.close());
  const page = await context.newPage(); page.setDefaultTimeout(10000);
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  const api = new CustomerOperationBrowserAPI({progressStage: template === 'customer-operation-workflow-export' ? 'collect' : 'generating'});
  await context.route(config.apiURL+'/**', route => api.route(route));
  const base = `http://127.0.0.1:${server.address().port}`;
  await page.goto(base);
  return {page, api, config, errors, async signin(owner = 'alice') {
    await page.locator('#token').fill(owner);
    await page.locator('#signin button').click();
    await page.locator('#workspace').waitFor({state: 'visible'});
    await page.waitForFunction(() => document.getElementById('notice').textContent || !document.getElementById('signin').hidden);
  }, async start() {
    await page.locator('#count').fill('3'); await page.locator('#submit').click();
  }};
}

export class CustomerOperationBrowserAPI {
  requests = []; submissions = []; downloads = 0; streams = 0;
  lost = undefined;
  failed = false;
  operations = new Map();
  receipts = new Map();

  constructor({progressStage = 'generating'} = {}) {
    this.progressStage = progressStage;
  }

  update(id, changes) {
    const record = this.operations.get(id);
    Object.assign(record.operation, changes);
    record.operation.latest_sequence++;
  }
  succeeded(id, resultID = artifactID) {
    const completionStage = this.progressStage === 'collect' ? 'finish' : this.progressStage;
    this.update(id, {state: 'succeeded', result: {rows: 3, artifact_id: resultID}, progress: {stage: completionStage, completed: 3, total: 3},
      artifacts: [{id: artifactID, name: 'export.csv', uri: `operation://${id}/artifacts/${artifactID}`, size_bytes: Buffer.byteLength(csv), sha256: 'sha256:'+'a'.repeat(64)}], completion_delivery: {state: 'failed', attempts: 1}});
  }
  receipt(id) { return {id, status_url: `${prefix}/${id}`, events_url: `${prefix}/${id}/events`}; }
  async route(route) {
    const req = route.request(), url = new URL(req.url()), path = url.pathname;
    const headers = await req.allHeaders();
    const owner = headers.authorization?.replace('Bearer ', '');
    const body = req.postDataJSON();
    this.requests.push({owner, method: req.method(), path, body});
    const responseHeaders = {'Access-Control-Allow-Origin': headers.origin ?? '*', 'Access-Control-Allow-Headers': 'Authorization, Content-Type, Idempotency-Key, Last-Event-ID', 'Access-Control-Allow-Methods': 'GET, POST, OPTIONS'};
    const respond = (value, status = 200, extra = {}) => route.fulfill({status, headers: {...responseHeaders, 'Content-Type': 'application/json', ...extra}, body: JSON.stringify(value)});
    if (req.method() === 'OPTIONS') return respond({});
    if (!owners[owner]) return respond({status: 401, code: 'unauthorized'}, 401);
    if (path === prefix+'/identity') return respond({account_id: accountID, platform_tenant_id: owners[owner]});
    if (path === prefix+'/submissions/lookup') {
      assert.equal(body.app_id, appID); assert.equal(body.scope, 'default'); assert.equal(body.name, 'customer-export');
      assert.deepEqual(body.expected_identity, {account_id: accountID, platform_tenant_id: owners[owner]});
      const id = this.receipts.get(owner+':'+body.idempotency_key);
      return respond(id ? {state: 'accepted', receipt: this.receipt(id), accepted_at: this.operations.get(id).operation.created_at} : {state: 'unresolved'});
    }
    if (path === prefix && req.method() === 'GET') {
      assert.equal(url.searchParams.get('app_id'), appID); assert.equal(url.searchParams.get('scope'), 'default'); assert.equal(url.searchParams.get('name'), 'customer-export');
      return respond({operations: [...this.operations.values()].filter(record => record.owner === owner).map(record => record.operation)});
    }
    if (path === prefix && req.method() === 'POST') {
      assert.deepEqual(body.expected_identity, {account_id: accountID, platform_tenant_id: owners[owner]});
      assert.deepEqual(body.expected_scope, {app_id: appID, scope: 'default', name: 'customer-export'});
      this.submissions.push({owner, key: headers['idempotency-key'], ...body});
      if (this.lost === 'before_commit' && !this.failed) {this.failed = true; return route.abort('failed');}
      let id = this.receipts.get(owner+':'+headers['idempotency-key']);
      if (!id) {
        id = `11111111-1111-4111-8111-${String(this.operations.size+1).padStart(12, '0')}`;
        this.receipts.set(owner+':'+headers['idempotency-key'], id);
        const now = new Date().toISOString();
        this.operations.set(id, {owner, operation: {id, name: 'customer-export', generation: 1, state: 'running', progress: {stage: this.progressStage, completed: 0, total: 3}, cancellation_requested: false, completion_delivery: {state: 'pending', attempts: 0}, latest_sequence: 1, created_at: now, updated_at: now, expires_at: new Date(Date.now()+86400000).toISOString()}});
      }
      if (this.lost === 'after_commit' && !this.failed) {this.failed = true; return route.abort('failed');}
      return respond(this.receipt(id), 202);
    }
    const parts = path.slice(prefix.length+1).split('/'), record = this.operations.get(parts[0]);
    if (!record || record.owner !== owner) return respond({status: 404, code: 'not_found'}, 404);
    if (parts.length === 1) return respond(record.operation);
    if (parts[1] === 'events') {
      this.streams++;
      return route.fulfill({status: 200, headers: {...responseHeaders, 'Content-Type': 'text/event-stream'}, body: `event: snapshot\ndata: ${JSON.stringify(record.operation)}\n\n`});
    }
    if (parts[1] === 'cancel') {
      assert.equal(body.expected_generation, record.operation.generation);
      this.update(parts[0], {state: 'requires_reconciliation', cancellation_requested: true});
      return respond(record.operation);
    }
    if (parts[1] === 'artifacts' && parts[2] === artifactID && record.operation.state === 'succeeded') {
      this.downloads++;
      return route.fulfill({status: 200, headers: {...responseHeaders, 'Content-Type': 'text/csv'}, body: csv});
    }
    return respond({status: 404, code: 'not_found'}, 404);
  }
}
