import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function browser(request, scope) {
  const elements = new Map();
  const element = () => ({value: '', textContent: '', children: [], disabled: false, elements: [{disabled: false}],
    append(...children) { this.children.push(...children); }, replaceChildren(...children) { this.children = children; }});
  const document = {getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); }, createElement() { return element(); }};
  const context = vm.createContext({document, URLSearchParams});
  vm.runInContext(readFileSync(new URL('./web/doctor.js', import.meta.url), 'utf8'), context);
  const Doctor = vm.runInContext('RepairDoctor', context);
  document.getElementById('doctor-table').value = 'public.products';
  return {document, view: new Doctor({request, scope, locked: () => false})};
}

const scope = {account_id: 'account', app_id: 'app', environment: 'production', platform_tenant_id: 'customer'};
function report(hosted = false) {
  const findings = ['connection', 'postgres', 'installation', 'functions', 'enrollment', 'fingerprint', 'triggers', 'permissions', 'policy', ...(hosted ? ['binding'] : [])]
    .map(id => ({id, status: 'ready', summary: '<img src=x>', action: 'Review separately', objects: []}));
  return {version: 1, scope, table: 'public.products', checked_at: '2026-10-17T12:00:00Z', status: 'ready', findings, policy: {mode: 'named', digest: 'a'.repeat(64)}};
}

test('doctor displays bounded configuration findings as text without plan authority', async () => {
  const queries = [];
  const {view, document} = browser(async params => { queries.push(params.toString()); return report(); });
  await view.load();
  assert.deepEqual(queries, ['table=public.products']);
  assert.match(document.getElementById('doctor-message').textContent, /Configuration ready.*Check application writers separately.*rehearsal remain separate/);
  const rows = document.getElementById('doctor-results').children;
  assert.equal(rows.length, 9);
  assert.equal(rows[0].children[2].textContent, '<img src=x>');
  assert.equal(elementsWithAuthority(document).length, 0);
});

function elementsWithAuthority(document) {
  return document.getElementById('doctor-results').children.flatMap(row => row.children.filter(cell => cell.onclick || cell.href));
}

test('doctor discards late reports after token, table or scope reset', async () => {
  let finish;
  const {view, document} = browser(() => new Promise(resolve => { finish = resolve; }));
  const loading = view.load();
  view.reset();
  finish(report()); await loading;
  assert.equal(document.getElementById('doctor-results').children.length, 0);
  assert.equal(document.getElementById('doctor-message').textContent, '');
  assert.equal(document.getElementById('doctor-form').elements[0].disabled, false);
});

test('hosted doctor binds scope and rejects mismatches, empty findings and false aggregates', async () => {
  for (const mode of ['valid', 'scope', 'table', 'empty', 'aggregate', 'duplicate', 'capture_profile']) {
    const data = report(true);
    if (mode === 'scope') data.scope = {...scope, platform_tenant_id: 'other'};
    if (mode === 'table') data.table = 'public.other';
    if (mode === 'empty') data.findings = [];
    if (mode === 'aggregate') data.findings[0].status = 'unknown';
    if (mode === 'duplicate') data.findings[1] = data.findings[0];
    if (mode === 'capture_profile') data.profile = 'capture';
    let query;
    const {view, document} = browser(async params => { query = params; return data; }, () => scope);
    await view.load();
    assert.equal(query.get('environment'), scope.environment); assert.equal(query.get('customer'), scope.platform_tenant_id);
    assert.equal(document.getElementById('doctor-results').children.length, mode === 'valid' ? 10 : 0);
    assert.match(document.getElementById('doctor-message').textContent, mode === 'valid' ? /Configuration ready/ : /Readiness unknown/);
  }
});

test('unknown and blocked reports stay explicit and missing hosted selection makes no request', async () => {
  for (const status of ['blocked', 'unknown']) {
    const data = report(); data.status = status; data.findings[0].status = status;
    const {view, document} = browser(async () => data);
    await view.load();
    assert.match(document.getElementById('doctor-message').textContent, new RegExp(`Configuration ${status}`));
  }
  const {view, document} = browser(() => { throw new Error('unexpected request'); }, () => null);
  await view.load();
  assert.match(document.getElementById('doctor-message').textContent, /Choose a configured environment/);
});
