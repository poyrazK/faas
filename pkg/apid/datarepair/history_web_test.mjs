import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

function dom() {
  const elements = new Map();
  const element = () => ({value: '', textContent: '', children: [], elements: [], disabled: false, hidden: false, dataset: {},
    replaceChildren(...children) { this.children = children; }, append(...children) { this.children.push(...children); },
    addEventListener(name, fn) { this[`on${name}`] = fn; }, querySelector() { return this.button ||= element(); },
    querySelectorAll(tag) { return this.children.flatMap(child => [...(child.tag === tag ? [child] : []), ...child.querySelectorAll(tag)]); }});
  const document = {getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); },
    createElement(tag) { return {...element(), tag}; }};
  document.getElementById('row-filter').value = 'all';
  return document;
}

function load(context, file) {
  vm.runInContext(readFileSync(new URL(`./web/${file}.js`, import.meta.url), 'utf8'), context);
}

function browser(request, select = () => {}, scope) {
  const document = dom(), context = vm.createContext({document, URLSearchParams});
  load(context, 'history');
  const Browser = vm.runInContext('RepairHistoryBrowser', context);
  return {document, view: new Browser({request, select, locked: () => false, scope})};
}

const scope = {account_id: 'account', app_id: 'app', environment: 'production', platform_tenant_id: 'customer'};
const parent = {id: 'parent', digest: 'parent-digest', created_at: '2026-10-01T12:00:00Z', expires_at: '2026-10-01T12:15:00Z',
  candidates: 460, conflicts: 40, unavailable: 0, approved_at: '2026-10-01T12:01:00Z', approved_by: '<img src=x>', replacement_plan_id: 'child',
  apply_resolution: {resolved_at: '2026-10-01T12:02:00Z', repair_operation_id: 'attempt', resolved_by: 'reviewer'}, payload_purged_at: '2026-10-17T10:00:00Z'};

test('history keeps closure, receipt, lineage and unavailable execution distinct; buttons only load saved IDs', async () => {
  const selected = [], queries = [];
  const {document, view} = browser(async (source, params) => {
    queries.push(params.toString());
    return {scope, source_operation_id: source, checked_at: '2026-10-17T12:00:00Z', items: [parent], next_cursor: 'opaque'};
  }, (...args) => selected.push(args));
  await view.show('source', scope);
  assert.equal(queries[0], '', 'standalone history must not accept caller-selected scope');
  const row = document.getElementById('history-results').children[0];
  assert.equal(row.children[2].textContent, `Approved ${parent.approved_at}; by <img src=x>`);
  assert.match(row.children[3].textContent, /Replaced by child/);
  assert.match(row.children[4].textContent, /Closed without commit/);
  assert.match(row.children[5].textContent, /Payload purged.*expired/);
  const buttons = row.children[7].children;
  buttons[0].onclick(); buttons[1].onclick();
  assert.deepEqual(selected.map(args => args.slice(0, 3)), [['parent', 'parent-digest', 'source'], ['child', '', 'source']]);
  await view.load(true);
  assert.equal(queries[1], 'cursor=opaque');

  const child = {...parent, id: 'child', digest: 'child-digest', candidates: 459, conflicts: 41, apply_resolution: null, replacement_plan_id: '',
    applied_at: '2026-10-17T11:00:00Z', repair_operation_id: 'repair', execution: {availability: 'not_retained'}};
  const committed = browser(async source => ({scope, source_operation_id: source, checked_at: '2026-10-17T12:00:00Z', items: [child]}));
  await committed.view.show('source');
  const cells = committed.document.getElementById('history-results').children[0].children;
  assert.match(cells[4].textContent, /Committed.*repair repair/);
  assert.equal(cells[6].textContent, 'Hosted execution history unavailable');
});

test('reset discards late responses and scope mismatch clears navigation', async () => {
  let finish;
  const pending = browser(() => new Promise(resolve => { finish = resolve; }));
  const loading = pending.view.show('source');
  pending.view.reset();
  finish({scope, source_operation_id: 'source', checked_at: 'now', items: [parent], next_cursor: 'stale'});
  await loading;
  assert.equal(pending.document.getElementById('history-results').children.length, 0);
  assert.equal(pending.view.next, '');

  const mismatch = browser(async source => ({scope: {...scope, platform_tenant_id: 'other'}, source_operation_id: source, items: [parent], next_cursor: 'unsafe'}), undefined, () => scope);
  await mismatch.view.show('source');
  assert.match(mismatch.document.getElementById('history-message').textContent, /did not match/);
  assert.equal(mismatch.document.getElementById('history-results').children.length, 0);
  assert.equal(mismatch.document.getElementById('history-next').disabled, true);
});

test('hosted retained view loads without a hosted operation and permits only receipt verification', async () => {
  const document = dom(), calls = [], id = randomUUID(), source = randomUUID();
  const p = {id, digest: 'a'.repeat(64), scope, source_operation_id: source, source_release: 'bad-import', items: [], candidates: 459, conflicts: 41, unavailable: 0,
    checks_sha256: 'b'.repeat(64), applied_at: '2026-10-17T11:00:00Z', repair_operation_id: randomUUID(), expires_at: '2026-10-01T12:15:00Z', payload_purged_at: '2026-10-17T10:00:00Z'};
  const location = {pathname: '/v1/apps/products/data-repair/review', search: '?' + new URLSearchParams({source, plan: id, environment: scope.environment, customer: scope.platform_tenant_id})};
  const context = vm.createContext({document, location, URLSearchParams, crypto: {randomUUID}, history: {replaceState(_a, _b, value) { location.search = value; }},
    fetch: async (path, options) => {
      calls.push({path, options});
      const data = path.endsWith('/source-scopes') ? {scopes: [scope]} : path.includes('/verify?') ? {
        plan_id: id, plan_digest: p.digest, scope, source_operation_id: source, repair_operation_id: p.repair_operation_id, applied_at: p.applied_at,
        checked_at: '2026-10-17T12:00:00Z', restored: 459, excluded: 41, available: true, checked: 459, unchanged: 459, edited: 0, missing: 0, issues: [], rows: [],
      } : {plan: p, execution: {availability: 'not_retained'}};
      return {ok: true, json: async () => data};
    }});
  for (const file of ['sources', 'doctor', 'history', 'checks', 'validation', 'verification', 'replacement', 'resolution', 'managed']) load(context, file);
  await new Promise(resolve => setImmediate(resolve));
  assert.match(document.getElementById('phase').textContent, /read-only.*execution history is unavailable/);
  assert.match(document.getElementById('verification-receipt').textContent, /459 records restored at commit/);
  for (const button of ['apply', 'approve', 'reject', 'rehearse', 'cancel-rehearsal', 'replace', 'resolve', 'reconcile', 'validate']) {
    assert.equal(document.getElementById(button).disabled, true, `${button} gained authority from history`);
  }
  assert.equal(document.getElementById('verify').disabled, false);
  assert.equal(calls.length, 2);
  assert.equal(calls.every(call => call.options.method === 'GET'), true);
  await document.getElementById('verify').onclick();
  assert.equal(calls.length, 3);
  assert.match(calls[2].path, new RegExp(`/sources/${source}/plans/${id}/verify\\?`));
  assert.deepEqual(JSON.parse(calls[2].options.body), {digest: p.digest});
  assert.match(document.getElementById('verification-message').textContent, /459 unchanged/);
  await document.getElementById('refresh').onclick();
  assert.equal(calls.length, 4);
  assert.equal(calls[3].options.method, 'GET');
  assert.equal(document.getElementById('apply').disabled, true);
});

test('standalone history inspection cannot resume an old approval or invent an apply identity', async () => {
  const document = dom(), calls = [], visited = [], id = randomUUID(), source = randomUUID();
  const p = {id, digest: 'a'.repeat(64), scope, source_operation_id: source, items: [], candidates: 1, conflicts: 0, unavailable: 0,
    checks_sha256: 'b'.repeat(64), approved_at: '2026-10-17T11:00:00Z', expires_at: '2099-10-17T12:15:00Z'};
  const original = '?plan=older-plan&repair=original-uncertain-apply';
  const location = {search: original};
  const context = vm.createContext({document, location, URLSearchParams, crypto: {randomUUID}, setInterval() {}, history: {
    replaceState(_a, _b, value) { location.search = value; }, pushState(_a, _b, value) { visited.push(location.search); location.search = value; }},
    fetch: async (path, options) => { calls.push({path, options}); return {ok: true, json: async () => p}; }});
  for (const file of ['sources', 'doctor', 'history', 'checks', 'validation', 'verification', 'replacement', 'resolution', 'app']) load(context, file);
  vm.runInContext(`historyBrowser.select('${id}', '${p.digest}', '${source}')`, context);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.method, 'GET');
  assert.equal(calls[0].path, `/v1/data-repair/plans/${id}`);
  assert.match(document.getElementById('status').textContent, /Read-only history view/);
  for (const action of ['apply', 'approve', 'rehearse', 'replace', 'resolve', 'validate']) assert.equal(document.getElementById(action).disabled, true);
  assert.equal(document.getElementById('repair-id').value, '');
  assert.deepEqual(visited, [original], 'history inspection erased an earlier uncertain apply URL');
});
