const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const { webcrypto, randomUUID } = require('node:crypto');
const ids = { account: randomUUID(), app: randomUUID(), tenant: randomUUID(), org: randomUUID(), op: randomUUID(), artifact: randomUUID() };
const quote = 'a'.repeat(64), changedQuote = 'b'.repeat(64);
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.value = ''; this.textContent = ''; this.hidden = false; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; this.textContent = ''; }
  reportValidity() { return true; }
  click() { this.clicked = true; }
}
const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(resolve => setImmediate(resolve)); };
const waitFor = async condition => { const deadline = Date.now() + 5000; while (!condition() && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 1)); assert.ok(condition(), 'Expected asynchronous browser action to complete'); };
const fixture = (currentQuote = quote) => ({
  access: { role: 'owner', policy_revision: quote, permissions: { view_work: true, run_work: true, view_billing: true } }, remaining_starts: { export: 10 },
  account_id: ids.account, app_id: ids.app, quotes: { export: currentQuote }, customer: { tenant_id: ids.tenant, plan: 'starter', suspended: false },
  catalog: { spec: { currency: 'EUR', features: { export: { label: '<script>Export</script>' } }, plans: { starter: { label: 'Starter', features: { export: { monthly_starts: 10, unit_price_millicents: currentQuote === quote ? 25000 : 50000 } } } } } },
  bindings: { export: { available: true, input_schema: { type: 'object', properties: { count: { type: 'integer', minimum: 1, maximum: 1000 } }, required: ['count'] } } },
  usage: { period: '2026-10', currency: 'EUR', lines: [] }, operations: [],
});
function browser(custom = async () => undefined, storage = new Map(), url = 'https://portal.example/v1/product-control/portal') {
  const elements = new Map(), calls = [], confirms = [], timers = [];
  const get = id => { if (!elements.has(id)) elements.set(id, new Element(id)); return elements.get(id); };
  const document = { getElementById: get, createElement: tag => new Element(tag) };
  const tails = new Map();
  const locks = { request: (key, task) => { const next = (tails.get(key) || Promise.resolve()).then(task); tails.set(key, next.catch(() => {})); return next; } };
  const response = body => ({ ok: true, status: 200, json: async () => body });
  const fetch = async (path, options) => {
    calls.push({ path, options }); const supplied = await custom(path, options); if (supplied) return supplied;
    if (path.endsWith('/workspaces')) return response({ workspaces: [{ organization_id: ids.org, tenant_id: ids.tenant, name: 'Alice' }], receipt_max_bytes: 8192, receipt_max_entries: 64, replay_seconds: 86400, help_text_max_bytes: 2000, help_page_size: 20, notification_page_size: 20, work_history_page_size: 25, work_history_cursor_max_bytes: 2048, activity_page_size: 25, activity_cursor_max_bytes: 2048 });
    if (path.endsWith('/email-preferences')) return response({ work_outcomes: false, help_replies: false, version: 0, mode: 'disabled', available: false, delivery_page_size: 25 });
    if (path.endsWith('/email-recoveries') || path.endsWith('/email-opt-outs')) return response({ receipts: [] });
    if (path.endsWith('/email-deliveries')) return response({ deliveries: [] });
    if (path.includes('/activity?')) return response({ account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, filter: new URL(path, 'https://portal.example').searchParams.get('filter'), events: [], observed_at: new Date().toISOString(), history_scope: 'retained_product_control_records' });
    if (path.includes('/notifications?')) return response({ account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, items: [], unread_count: 0, observed_at: new Date().toISOString(), window_start: new Date(Date.now()-86400000).toISOString(), read_state_scope: 'workspace' });
    if (path.includes('/help?')) return response({ requests: [], observed_at: new Date().toISOString() });
    if (path.endsWith('/submissions/lookup')) return response({ state: 'unresolved' });
    if (path.endsWith('/details')) return response(runDetailsFixture(path.split('/').at(-2)));
    if (path.endsWith(`/operations/${ids.op}`)) return response({ id: ids.op, name: 'export', state: 'succeeded', result: { rows: 8 }, artifacts: [{ id: ids.artifact, name: 'export.csv' }] });
    return response(path.endsWith('/product') ? fixture() : { authenticated: true });
  };
  const BrowserURL = URL;
  const context = vm.createContext({ document, fetch, crypto: { subtle: webcrypto.subtle, randomUUID }, TextEncoder, URL: BrowserURL, URLSearchParams, AbortController,
    location: { href: url }, history: { replaceState() {} }, navigator: { locks }, localStorage: { get length() { return storage.size; }, key: index => [...storage.keys()][index] ?? null, getItem: k => storage.get(k) ?? null, setItem: (k, v) => storage.set(k, v), removeItem: k => storage.delete(k) },
    setTimeout: (task, ms) => { timers.push({ task, ms }); return timers.length; }, clearTimeout() {}, window: { confirm: text => { confirms.push(text); return true; } } });
  for (const name of ['portal-core.js', 'portal-plans.js', 'portal-team.js', 'portal-guidance.js', 'portal-help.js', 'portal-notifications.js', 'portal-activity.js', 'portal-run.js', 'portal-history.js', 'portal-email.js', 'portal.js']) vm.runInContext(fs.readFileSync(`${__dirname}/${name}`, 'utf8'), context);
  const signIn = async () => { get('app').value = 'exports'; get('email').value = 'alice@example.com'; get('password').value = 'private-password'; await get('connect').onsubmit({ preventDefault() {} }); await settle(); };
  const form = () => get('features').children[0].children.find(el => el.tag === 'form');
  const submit = async count => { const f = form(); f.children[0].children[0].value = String(count); await f.onsubmit({ preventDefault() {} }); await settle(); };
  return { get, calls, confirms, timers, signIn, submit, storage, context };
}

test('password recovery uses the current product without submitting credentials or mail', async () => {
  const b = browser();
  b.get('app').value = ' exports ';
  b.get('app').oninput();
  assert.equal(b.get('account-recovery').href, '/v1/product-control/customer/exports/account-recovery');
  b.get('email').value = 'PRIVATE_EMAIL@example.test';
  b.get('password').value = 'PRIVATE_PASSWORD';
  let prevented = false;
  b.get('account-recovery').onclick({ preventDefault() { prevented = true; } });
  assert.equal(prevented, false);
  assert.equal(b.get('account-recovery').href, '/v1/product-control/customer/exports/account-recovery');
  assert.equal(b.get('password').value, '');
  assert.match(b.get('message').textContent, /new tab.*return here/);
  assert.equal(b.calls.length, 0);
  assert.equal(b.storage.size, 0);
  b.get('app').value = 'replacement';
  b.get('app').oninput();
  b.get('account-recovery').onclick({ preventDefault() { prevented = true; } });
  assert.equal(b.get('account-recovery').href, '/v1/product-control/customer/replacement/account-recovery');
});

test('password recovery blocks empty and hostile product selectors before navigation', () => {
  const b = browser();
  for (const value of ['', '../other', 'exports?redirect_to=https://evil.example', 'exports/../../auth', '<script>']) {
    b.get('app').value = value;
    let prevented = false;
    b.get('account-recovery').onclick({ preventDefault() { prevented = true; } });
    assert.equal(prevented, true);
    assert.match(b.get('message').textContent, /product name/);
  }
  assert.equal(b.calls.length, 0);
});

const teamActor = randomUUID(), teamMember = randomUUID();
const teamResponse = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
function teamBrowser(custom = async () => undefined, storage = new Map(), url) {
  const state = { receipts: new Map(), head: quote, role: 'member', posts: 0, manual: false };
  state.record = confirmation => {
    const request = confirmation.request;
    const result = request.action === 'invite' ? { id: randomUUID(), email: request.email, before_role: '', after_role: request.role, delivered: !state.manual } : { user_id: request.target_id, email: 'member@example.test', before_role: state.role, after_role: request.role || '' };
    const receipt = { id: request.id, request, result, action: request.action, actor_id: teamActor, actor_role: 'manager', created_at: '2026-10-16T12:00:00Z', review_hash: confirmation.expected_hash };
    state.receipts.set(request.id, receipt); state.posts++; if (request.action === 'set_role') state.role = request.role;
    return receipt;
  };
  const b = browser(async (path, options) => {
    const supplied = await custom(path, options, state); if (supplied) return supplied;
    if (path.endsWith('/product')) { const data = fixture(); data.access = { role: 'manager', policy_revision: state.head, permissions: { manage_team: true } }; data.usage = null; data.bindings = {}; return teamResponse(data); }
    if (path.endsWith('/team')) return teamResponse({ actor_id: teamActor, actor_role: 'manager', revision: state.head, invitation_default_seconds: 604800, members: [{ user_id: teamActor, email: 'manager@example.test', role: 'manager', product_permissions: { manage_team: true }, can_manage: false }, { user_id: teamMember, email: '<script>Member</script>', role: state.role, product_permissions: { view_work: true }, can_manage: true }], invitations: [], assignable_roles: [{ role: 'member', product_permissions: { view_work: true }, workspace_permissions: ['org:read'] }, { role: 'operator', product_permissions: { run_work: true, view_work: true }, workspace_permissions: ['org:read', 'doc:write'] }] });
    if (path.includes('/team/actions?')) return teamResponse({ actions: [...state.receipts.values()] });
    if (path.includes('/team/actions/')) { const receipt = state.receipts.get(path.split('/').at(-1)); return teamResponse(receipt || { detail: 'Not recorded' }, receipt ? 200 : 404); }
    if (path.endsWith('/team/preview')) { const request = JSON.parse(options.body); return teamResponse({ request, subject: request.email || 'member@example.test', before_role: request.action === 'invite' ? '' : state.role, after_role: request.role || '', before_permissions: { view_work: true }, after_permissions: { run_work: true }, before_workspace_permissions: ['org:read'], after_workspace_permissions: ['org:read', 'doc:write'], hash: quote, auth_snapshot: changedQuote }); }
    if (path.endsWith('/team/confirm')) { const confirmation = JSON.parse(options.body), receipt = state.record(confirmation); return teamResponse({ receipt, replayed: false, ...(state.manual && receipt.action === 'invite' ? { invitation_url: `https://auth.example.test/ui/invitation#token=${'s'.repeat(43)}` } : {}) }); }
  }, storage, url);
  b.state = state;
  b.reviewRole = async () => { const row = b.get('team-members').children[1]; row.children[0].value = 'operator'; await row.children[1].onclick(); await settle(); };
  b.invite = async () => { b.get('team-email').value = ' FRIEND@Example.Test '; b.get('team-invite-role').value = 'member'; await b.get('team-invite-form').onsubmit({ preventDefault() {} }); await settle(); };
  b.confirmTeam = async () => { await b.get('team-confirm').onclick(); await settle(); };
  return b;
}

test('team-only portal shows approved roles and both permission layers before explicit confirmation', async () => {
  const b = teamBrowser(); await b.signIn();
  assert.equal(b.get('team-section').hidden, false); assert.equal(b.get('features-section').hidden, true); assert.equal(b.get('billing-section').hidden, true);
  assert.equal(b.get('team-members').children[0].children.length, 0);
  assert.equal(b.get('team-members').children[1].textContent.startsWith('<script>Member</script>'), true);
  assert.deepEqual(b.get('team-invite-role').children.map(option => option.value), ['member', 'operator']);
  await b.reviewRole(); assert.equal(b.state.posts, 0); assert.equal(b.get('team-confirm').hidden, false);
  const review = JSON.stringify(b.get('team-review').children); assert.match(review, /Run work/); assert.match(review, /doc:write/);
  await b.confirmTeam(); assert.equal(b.state.posts, 1); assert.match(b.confirms[0], /workspace/); assert.match(b.get('team-status').textContent, /member → operator/);
  assert.equal(b.calls.filter(call => call.path.endsWith('/start')).length, 0);
  for (const call of b.calls) { assert.equal(call.options.credentials, 'same-origin'); assert.equal(call.options.headers.Authorization, undefined); }
});

test('manual team invitation links appear once and never enter browser recovery storage or history', async () => {
  const b = teamBrowser(); b.state.manual = true; await b.signIn(); await b.invite(); await b.confirmTeam();
  const link = b.get('team-link').children[1]; assert.match(link.value, /^https:\/\/auth.example.test\/ui\/invitation#token=/); assert.equal(link.readOnly, true);
  assert.ok(![...b.storage.values()].join('').includes('friend@example.test')); assert.ok(![...b.storage.values()].join('').includes('token='));
  assert.ok(!JSON.stringify(b.get('team-history').children).includes('token='));
  await b.get('disconnect').onclick(); assert.equal(b.get('team-link').children.length, 0); assert.equal(b.get('team-email').value, '');
});

test('team invitations report configured provider delivery', async () => {
  const b = teamBrowser(); await b.signIn(); await b.invite(); await b.confirmTeam();
  assert.match(b.get('team-link').children[0].textContent, /configured email provider/);
});

test('lost team acknowledgements recover by GET and never replay a membership mutation', async () => {
  const storage = new Map();
  const first = teamBrowser(async (path, options, state) => { if (path.endsWith('/team/confirm')) { state.record(JSON.parse(options.body)); throw new Error('lost response'); } }, storage);
  await first.signIn(); await first.reviewRole(); await first.confirmTeam();
  assert.match(first.get('message').textContent, /reference is saved/);
  const saved = JSON.parse([...storage.values()].find(raw => raw.includes('"hash"'))); assert.deepEqual(Object.keys(saved).sort(), ['hash', 'id']);
  const recorded = [...first.state.receipts.values()][0];
  const second = teamBrowser(async path => path.endsWith(`/team/actions/${recorded.id}`) ? teamResponse(recorded) : undefined, storage, 'https://portal.example/v1/product-control/portal?app=exports');
  await settle(); assert.equal(second.state.posts, 0); assert.match(second.get('team-status').textContent, /member → operator/);
  assert.equal(second.calls.filter(call => call.path.endsWith('/team/confirm')).length, 0);
});

test('a missing team receipt reuses its UUID only after a fresh review and confirmation', async () => {
  const b = teamBrowser(async path => { if (path.endsWith('/team/confirm')) throw new Error('lost before acceptance'); });
  await b.signIn(); await b.reviewRole(); await b.confirmTeam();
  const first = JSON.parse(b.calls.find(call => call.path.endsWith('/team/preview')).options.body);
  await b.get('team-check').onclick(); assert.match(b.get('team-status').textContent, /not recorded/);
  await b.reviewRole(); const reviews = b.calls.filter(call => call.path.endsWith('/team/preview'));
  assert.equal(JSON.parse(reviews.at(-1).options.body).id, first.id); assert.equal(b.calls.filter(call => call.path.endsWith('/team/confirm')).length, 1);
});

test('a recovered manual invitation explains revoke and reissue without reconstructing its token', async () => {
  const b = teamBrowser(async (path, options, state) => { if (path.endsWith('/team/confirm')) { state.manual = true; state.record(JSON.parse(options.body)); throw new Error('lost'); } });
  await b.signIn(); await b.invite(); await b.confirmTeam(); await b.get('team-check').onclick();
  assert.match(b.get('team-link').children[0].textContent, /revoke this invitation/); assert.equal(b.get('team-link').children.length, 1);
});

test('team review conflicts and storage failures never automatically resubmit', async () => {
  const b = teamBrowser(async path => path.endsWith('/team/confirm') ? teamResponse({ detail: 'Workspace changed' }, 409) : undefined);
  await b.signIn(); await b.reviewRole(); await b.confirmTeam(); assert.match(b.get('message').textContent, /No action was automatically resubmitted/);
  assert.equal(b.get('team-confirm').hidden, true); assert.equal(b.calls.filter(call => call.path.endsWith('/team/confirm')).length, 1);
  const blocked = teamBrowser(); await blocked.signIn(); await blocked.reviewRole();
  blocked.context.localStorage.setItem = () => { throw new Error('storage full'); };
  await blocked.confirmTeam(); assert.equal(blocked.state.posts, 0); assert.match(blocked.get('message').textContent, /storage full/);
});

test('team confirmation detects another tab replacing recovery metadata', async () => {
  const b = teamBrowser(); await b.signIn(); await b.reviewRole();
  const key = `gregale.product.team.v1:${JSON.stringify([ids.account, ids.app, ids.tenant])}`;
  b.storage.set(key, JSON.stringify({ id: randomUUID(), hash: changedQuote }));
  await b.confirmTeam(); assert.equal(b.state.posts, 0); assert.match(b.get('message').textContent, /another tab/);
});

test('team input changes invalidate a visible review', async () => {
  const b = teamBrowser(); await b.signIn(); await b.invite(); assert.equal(b.get('team-confirm').hidden, false);
  b.get('team-email').value = 'different@example.test'; b.get('team-invite-form').oninput();
  assert.equal(b.get('team-confirm').hidden, true); await b.confirmTeam(); assert.equal(b.state.posts, 0);
});

test('logout fences team reads that were already in flight', async () => {
  let resolveTeam, delay = false;
  const b = teamBrowser(path => path.endsWith('/team') && delay ? new Promise(resolve => { resolveTeam = resolve; }) : undefined);
  await b.signIn(); delay = true; const loading = b.get('team-load').onclick(); await settle();
  await b.get('disconnect').onclick();
  resolveTeam(teamResponse({ members: [{ email: 'private@example.test' }], invitations: [], assignable_roles: [] })); await loading;
  assert.equal(b.get('team-members').children.length, 0); assert.equal(b.get('workspace').hidden, true);
});

test('Auth rejection fences older pending team responses and clears private data', async () => {
  let resolveOld, calls = 0, failing = false;
  const b = teamBrowser(path => {
    if (!path.endsWith('/team') || !failing) return;
    if (++calls === 1) return new Promise(resolve => { resolveOld = resolve; });
    return teamResponse({ detail: 'Workspace permission ended' }, 403);
  });
  await b.signIn(); failing = true; const old = b.get('team-load').onclick(); await settle();
  await b.get('team-load').onclick(); resolveOld(teamResponse({ members: [{ email: 'private@example.test' }], invitations: [], assignable_roles: [] })); await old;
  assert.equal(b.get('team-members').children.length, 0); assert.equal(b.get('team-link').children.length, 0); assert.match(b.get('message').textContent, /permission ended/);
});

test('a new product policy invalidates the team review and requires reloading current roles', async () => {
  const b = teamBrowser(); await b.signIn(); await b.reviewRole(); b.state.head = changedQuote;
  await b.timers.find(timer => timer.ms === 4000).task(); await settle();
  assert.equal(b.get('team-confirm').hidden, true); assert.equal(b.get('team-members').children.length, 0); assert.match(b.get('team-status').textContent, /Load the current team/);
});
test('customer login uses cookies, guided input, confirmed price, and durable recovery', async () => {
  let attempts = 0;
  const b = browser(async (path) => {
    if (path.endsWith('/start')) { if (++attempts === 1) throw new Error('lost response'); return { ok: true, status: 202, json: async () => ({ id: ids.op }) }; }
  });
  await b.signIn(); assert.equal(b.get('password').value, ''); assert.equal(b.get('workspace').hidden, false);
  assert.equal(b.get('features').children[0].children[0].textContent, '<script>Export</script>');
  await b.submit(8); assert.match(b.get('message').textContent, /request is saved/);
  await b.submit(9); assert.match(b.get('message').textContent, /original request details/); assert.equal(attempts, 1);
  await b.submit(8); assert.equal(attempts, 2);
  const starts = b.calls.filter(c => c.path.endsWith('/start'));
  assert.equal(starts[0].options.body, starts[1].options.body);
  assert.equal(starts[0].options.headers['Idempotency-Key'], starts[1].options.headers['Idempotency-Key']);
  assert.deepEqual(JSON.parse(starts[0].options.body), { feature: 'export', input: { count: 8 }, expected_quote: quote });
  assert.match(b.confirms[0], /EUR 0.25/);
  const raw = [...b.storage.values()].join(''); assert.ok(!raw.includes('private-password')); assert.ok(!raw.includes('"count"'));
  for (const call of b.calls) { assert.equal(call.options.credentials, 'same-origin'); assert.equal(call.options.redirect, 'error'); assert.equal(call.options.headers.Authorization, undefined); assert.ok(!call.path.includes('password')); }
  assert.match(b.get('work').children[0].textContent, /Completed/);
});
test('changed terms require a second visible confirmation without automatic resubmission', async () => {
  let starts = 0, changed = false;
  const b = browser(async path => {
    if (path.endsWith('/start')) { starts++; if (!changed) { changed = true; return { ok: false, status: 409, json: async () => ({ code: 'product_quote_changed', detail: 'Confirm changed price' }) }; } return { ok: true, status: 202, json: async () => ({ id: ids.op }) }; }
    if (path.endsWith('/product')) return { ok: true, status: 200, json: async () => fixture(changed ? changedQuote : quote) };
  });
  await b.signIn(); await b.submit(8); assert.equal(starts, 1); assert.match(b.get('message').textContent, /confirm again/);
  await b.submit(8); assert.equal(starts, 2); assert.match(b.confirms.at(-1), /EUR 0.50/);
  assert.equal(JSON.parse(b.calls.filter(c => c.path.endsWith('/start'))[1].options.body).expected_quote, changedQuote);
});
// ADR-20261021154511: included starts come first at no charge, then the unit price.
test('included starts show the included runs left and confirm a free start before the priced ones', async () => {
  const included = remaining => {
    const value = fixture(); value.remaining_starts = { export: remaining };
    value.catalog.spec.plans.starter.features.export = { included_starts: 3, monthly_starts: 10, unit_price_millicents: 20000 };
    return value;
  };
  let remaining = 8;
  const b = browser(async path => {
    if (path.endsWith('/start')) { remaining--; return { ok: true, status: 202, json: async () => ({ id: ids.op }) }; }
    if (path.endsWith('/product')) return { ok: true, status: 200, json: async () => included(remaining) };
  });
  await b.signIn();
  assert.equal(b.get('features').children[0].children[1].textContent, '1 of 3 included runs left, then EUR 0.20 per successful run · 8 of 10 starts remaining this month.');
  await b.submit(1); assert.match(b.confirms[0], /included in your plan at no charge/);
  await waitFor(() => b.get('features').children[0].children[1].textContent.startsWith('0 of 3 included runs left'));
  await b.submit(2); assert.match(b.confirms.at(-1), /EUR 0.20 will be recorded only if it succeeds/);
});
test('reload discovers a lost acknowledgement without sending any work', async () => {
  const storage = new Map(), first = browser(async path => { if (path.endsWith('/start')) throw new Error('lost'); }, storage);
  await first.signIn(); await first.submit(8);
  const second = browser(async path => { if (path.endsWith('/submissions/lookup')) return { ok: true, status: 200, json: async () => ({ state: 'accepted', receipt: { id: ids.op } }) }; }, storage, 'https://portal.example/v1/product-control/portal?app=exports');
  await settle();
  assert.equal(second.calls.filter(c => c.path.endsWith('/start')).length, 0);
  assert.match(second.get('work').children[0].textContent, /Completed/);
  assert.equal(JSON.parse([...storage.values()][0]).operationID, ids.op);
});
test('logout fences a workspace response still in flight and keeps recovery records', async () => {
  let resolveView;
  const b = browser(path => path.endsWith('/product') ? new Promise(resolve => { resolveView = resolve; }) : undefined);
  const signingIn = b.signIn(); await settle();
  await b.get('disconnect').onclick();
  resolveView({ ok: true, status: 200, json: async () => fixture() }); await signingIn; await settle();
  assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('features').children.length, 0);
});
test('polling updates progress and shows reconciliation without offering a replacement', async () => {
  let state = 'running';
  const b = browser(async path => {
    if (path.endsWith('/product')) { const data = fixture(); data.operations = [{ id: ids.op, name: 'export', state, progress: { stage: 'generating', completed: 3, total: 8 } }]; return { ok: true, status: 200, json: async () => data }; }
  });
  await b.signIn(); assert.match(b.get('work').children[0].textContent, /generating: 3\/8/);
  state = 'requires_reconciliation'; await b.timers.find(t => t.ms === 4000).task(); await settle();
  assert.match(textTree(b.get('work')), /do not run a replacement/);
});
test('a session restore failure cannot clear a newer successful customer login', async () => {
  let resolveOld, first = true;
  const b = browser(path => {
    if (path.endsWith('/workspaces') && first) { first = false; return new Promise(resolve => { resolveOld = resolve; }); }
  }, new Map(), 'https://portal.example/v1/product-control/portal?app=exports');
  await settle(); await b.signIn();
  resolveOld({ ok: false, status: 503, json: async () => ({ detail: 'old restore failed' }) }); await settle();
  assert.equal(b.get('workspace').hidden, false); assert.equal(b.get('message').textContent, '');
});
test('private file downloads use the scoped cookie route and logout fences pending bytes', async () => {
  let resolveBlob, createdURLs = 0;
  class DownloadURL extends URL { static createObjectURL() { createdURLs++; return 'blob:private-result'; } static revokeObjectURL() {} }
  const b = browser(async path => {
    if (path.includes('/artifacts/')) return { ok: true, status: 200, blob: () => new Promise(resolve => { resolveBlob = resolve; }) };
    if (path.endsWith('/product')) { const data = fixture(); data.operations = [{ id: ids.op, name: 'export', state: 'succeeded' }]; return { ok: true, status: 200, json: async () => data }; }
  });
  b.context.URL = DownloadURL;
  await b.signIn(); await b.get('work').children[0].children.find(e => e.textContent === 'View result and files').onclick(); await settle();
  assert.equal(b.get('result').children[0].textContent, '{\n  "rows": 8\n}');
  const downloading = b.get('result').children[1].onclick(); await settle();
  const request = b.calls.find(c => c.path.includes('/artifacts/'));
  assert.equal(request.path, `/v1/product-control/customer/exports/operations/${ids.op}/artifacts/${ids.artifact}`);
  await b.get('disconnect').onclick(); resolveBlob({ size: 10 }); await downloading;
  assert.equal(createdURLs, 0); assert.equal(b.get('result').hidden, true);
});
test('cancellation confirms the current execution generation and never resubmits work', async () => {
  const b = browser(async path => {
    if (path.endsWith('/product')) { const data = fixture(); data.operations = [{ id: ids.op, name: 'export', state: 'running' }]; return { ok: true, status: 200, json: async () => data }; }
    if (path.endsWith(`/operations/${ids.op}`)) return { ok: true, status: 200, json: async () => ({ id: ids.op, name: 'export', state: 'running', generation: 2 }) };
    if (path.endsWith('/cancel')) return { ok: true, status: 200, json: async () => ({ id: ids.op, name: 'export', state: 'running', generation: 2, cancellation_requested: true }) };
  });
  await b.signIn(); await b.get('work').children[0].children.find(e => e.textContent === 'Cancel').onclick();
  const cancel = b.calls.find(call => call.path.endsWith('/cancel'));
  assert.deepEqual(JSON.parse(cancel.options.body), { expected_generation: 2 });
  assert.ok(b.get('work').children[0].children.some(e => e.textContent === 'Cancellation requested'));
  assert.equal(b.calls.filter(call => call.path.endsWith('/start')).length, 0);
});
test('guided choice fields preserve boolean values in the request contract', async () => {
  const b = browser(async path => {
    if (path.endsWith('/product')) { const data = fixture(); data.bindings.export.input_schema = { type: 'object', properties: { approved: { type: 'boolean', enum: [true] } }, required: ['approved'] }; return { ok: true, status: 200, json: async () => data }; }
    if (path.endsWith('/start')) return { ok: true, status: 202, json: async () => ({ id: ids.op }) };
  });
  await b.signIn();
  const form = b.get('features').children[0].children.find(el => el.tag === 'form');
  form.children[0].children[0].value = 'true'; await form.onsubmit({ preventDefault() {} });
  const start = b.calls.find(c => c.path.endsWith('/start'));
  assert.deepEqual(JSON.parse(start.options.body).input, { approved: true });
});

test('billing displays immutable statements and signed credit notes with explicit pagination', async () => {
  const documentID = randomUUID(), creditID = randomUUID();
  const b = browser(async path => {
    if (!path.includes('/billing?')) return;
    const query = new URL(path, 'https://portal.example').searchParams;
    assert.equal(query.get('period'), '2026-09');
    const next = query.get('cursor') === creditID;
    return { ok: true, status: 200, json: async () => ({
      review: { period: '2026-09', currency: 'EUR', finalized: { amount_millicents: 25000 } },
      documents: { documents: next ? [{ id: documentID, kind: 'statement', currency: 'EUR', amount_millicents: 25000, snapshot: { lines: [{ feature: 'export', succeeded: 1, waived: 0, amount_millicents: 25000 }] } }] : [{ id: creditID, kind: 'credit_note', currency: 'EUR', amount_millicents: -5000, reason: '<script>service credit</script>' }], next_cursor: next ? '' : creditID },
    }) };
  });
  await b.signIn(); b.get('billing-period').value = '2026-09';
  await b.get('billing-form').onsubmit({ preventDefault() {} });
  assert.match(b.get('billing-summary').textContent, /Finalized charges EUR 0.25/);
  assert.match(b.get('billing-documents').children[0].textContent, /Credit note · EUR -0.05/);
  assert.equal(b.get('billing-documents').children[0].children[0].textContent, '<script>service credit</script>');
  assert.equal(b.get('billing-more').hidden, false);
  b.get('billing-period').value = '2026-10';
  await b.get('billing-more').onclick();
  assert.match(b.get('billing-documents').children[0].textContent, /Finalized statement/);
  assert.equal(b.get('billing-more').hidden, true);
  assert.ok(b.calls.filter(c => c.path.includes('/billing?')).every(c => c.options.method === undefined));
});

// adr: 20261021154511 — the month's plan fee shows in provisional charges and on finalized statements, with its proration.
test('billing shows the month plan fee and its proration', async () => {
  const fee = { plan: 'pro', revision: 'a'.repeat(64), monthly_fee_millicents: 2900000, amount_millicents: 1029032, prorated_from: '2026-10-21T00:00:00Z' };
  const b = browser(async path => {
    if (!path.includes('/billing?')) return;
    const finalized = new URL(path, 'https://portal.example').searchParams.get('period') === '2026-09';
    return { ok: true, status: 200, json: async () => ({
      review: finalized ? { period: '2026-09', currency: 'EUR', finalized: { amount_millicents: 2900000 } } : { period: '2026-10', currency: 'EUR', amount_millicents: 1129032, unresolved: 0, closed: false, fees: [fee] },
      documents: { documents: finalized ? [{ id: randomUUID(), kind: 'statement', currency: 'EUR', amount_millicents: 2900000, snapshot: { lines: [], fees: [{ ...fee, amount_millicents: 2900000, prorated_from: undefined }] } }] : [], next_cursor: '' },
    }) };
  });
  await b.signIn(); b.get('billing-period').value = '2026-10';
  await b.get('billing-form').onsubmit({ preventDefault() {} });
  assert.match(b.get('billing-summary').textContent, /Provisional charges EUR 11.29032 including the monthly fee for plan pro, EUR 10.29032 prorated from 2026-10-21/);
  b.get('billing-period').value = '2026-09';
  await b.get('billing-form').onsubmit({ preventDefault() {} });
  assert.equal(b.get('billing-documents').children[0].children[0].textContent, 'the monthly fee for plan pro, EUR 29.00');
});

// adr: 20261018212918 — exported statements show the provider's payment status and only its own https invoice page.
test('billing shows exported statement invoices and links only to the provider https page', async () => {
  const statement = (id, invoice) => ({ id, kind: 'statement', currency: 'EUR', amount_millicents: 50000, invoice });
  const b = browser(async path => {
    if (!path.includes('/billing?')) return;
    return { ok: true, status: 200, json: async () => ({
      review: { period: '2026-09', currency: 'EUR', finalized: { amount_millicents: 50000 } },
      documents: { documents: [
        statement(randomUUID(), { payment_status: 'open', overdue: true, currency: 'EUR', amount_minor: 1250, amount_remaining_minor: 1250, amount_remaining_millicents: 1250000, due_at: '2026-10-15T00:00:00Z', hosted_invoice_url: 'https://invoice.stripe.com/i/acct_1/in_2' }),
        statement(randomUUID(), { payment_status: 'paid', overdue: false, currency: 'EUR', amount_minor: 50, amount_remaining_minor: 0, amount_remaining_millicents: 0, hosted_invoice_url: 'javascript:alert(1)' }),
        statement(randomUUID(), { overdue: false, currency: 'EUR', amount_minor: 50 }),
      ], next_cursor: '' },
    }) };
  });
  await b.signIn(); b.get('billing-period').value = '2026-09';
  await b.get('billing-form').onsubmit({ preventDefault() {} });
  const [overdue, paid, unsynced] = b.get('billing-documents').children;
  assert.equal(overdue.children[0].textContent, 'Invoice overdue · due 2026-10-15 · EUR 12.50 remaining');
  const pay = overdue.children[1];
  assert.equal(pay.tag, 'a'); assert.equal(pay.textContent, 'View and pay invoice'); assert.equal(pay.href, 'https://invoice.stripe.com/i/acct_1/in_2');
  assert.equal(pay.target, '_blank'); assert.equal(pay.rel, 'noopener noreferrer');
  assert.equal(paid.children[0].textContent, 'Invoice paid');
  assert.equal(paid.children.length, 1, 'a non-https invoice link must not render');
  assert.equal(unsynced.children[0].textContent, 'Invoice issued; payment status not available yet');
  assert.equal(unsynced.children.length, 1);
});

test('an overdue invoice hold pauses new runs and links only to the provider https page', async () => {
  const held = hosted_invoice_url => async path => {
    if (!path.endsWith('/product')) return;
    const view = fixture(); view.overdue_hold = { due_at: '2026-10-15T00:00:00Z', hosted_invoice_url };
    return { ok: true, status: 200, json: async () => view };
  };
  const b = browser(held('https://invoice.stripe.com/i/acct_1/in_2'));
  await b.signIn();
  const status = b.get('overdue-status');
  assert.equal(status.hidden, false);
  assert.equal(status.children[0].textContent, 'New runs are paused until your invoice due 2026-10-15 is paid. Work already running continues.');
  const pay = status.children[1];
  assert.equal(pay.tag, 'a'); assert.equal(pay.textContent, 'View and pay invoice'); assert.equal(pay.href, 'https://invoice.stripe.com/i/acct_1/in_2');
  assert.equal(pay.target, '_blank'); assert.equal(pay.rel, 'noopener noreferrer');
  assert.equal(b.get('account-status').textContent, 'Your workspace is active.', 'a hold is not the product team pausing the workspace');
  const run = b.get('features').children[0].children.find(el => el.tag === 'form').children.at(-1);
  assert.equal(run.disabled, true);

  const unsafe = browser(held('javascript:alert(1)'));
  await unsafe.signIn();
  assert.equal(unsafe.get('overdue-status').children.length, 1, 'a non-https invoice link must not render');

  const clear = browser();
  await clear.signIn();
  assert.equal(clear.get('overdue-status').hidden, true);
});

test('billing details load and save through the product team billing provider for roles that manage billing', async () => {
  let saved;
  const withManageBilling = manage => async (path, options) => {
    if (path.endsWith('/product')) { const view = fixture(); view.access.permissions.manage_billing = manage; return { ok: true, status: 200, json: async () => view }; }
    if (!path.endsWith('/billing-details')) return;
    if (options?.method === 'PUT') { saved = JSON.parse(options.body); return { ok: true, status: 200, json: async () => ({ provider: 'paddle', ...saved }) }; }
    return { ok: true, status: 200, json: async () => ({ provider: 'paddle', address: { country_code: 'DE', postal_code: '10115', city: 'Berlin', first_line: 'Unter den Linden 1' } }) };
  };
  const denied = browser(withManageBilling(false));
  await denied.signIn();
  assert.equal(denied.get('billing-details-section').hidden, true);

  const b = browser(withManageBilling(true));
  await b.signIn();
  assert.equal(b.get('billing-details-section').hidden, false);
  await b.get('billing-details-load').onclick();
  assert.equal(b.get('billing-details-form').hidden, false);
  assert.equal(b.get('billing-country').value, 'DE'); assert.equal(b.get('billing-city').value, 'Berlin'); assert.equal(b.get('billing-business').value, '');
  assert.equal(b.get('billing-details-status').textContent, 'Invoices are addressed to these details.');
  b.get('billing-country').value = ' gb '; b.get('billing-postal').value = 'SW1A 1AA'; b.get('billing-city').value = 'London'; b.get('billing-line1').value = '10 Downing St';
  await b.get('billing-details-form').onsubmit({ preventDefault() {} });
  assert.deepEqual(saved, { address: { country_code: 'GB', postal_code: 'SW1A 1AA', region: '', city: 'London', first_line: '10 Downing St', second_line: '' } }, 'an empty business name keeps the current business');
  assert.equal(b.get('billing-details-status').textContent, 'Billing details saved. New invoices use them.');
  b.get('billing-business').value = 'Acme Ltd'; b.get('billing-tax-id').value = 'GB123';
  await b.get('billing-details-form').onsubmit({ preventDefault() {} });
  assert.deepEqual(saved.business, { name: 'Acme Ltd', tax_identifier: 'GB123' });
  const put = b.calls.filter(call => call.path.endsWith('/billing-details') && call.options?.method === 'PUT');
  assert.equal(put.length, 2); assert.equal(put[0].path, '/v1/product-control/customer/exports/billing-details');
});

test('billing responses cannot restore another workspace after logout', async () => {
  let resolveBilling;
  const b = browser(path => path.includes('/billing?') ? new Promise(resolve => { resolveBilling = resolve; }) : undefined);
  await b.signIn(); b.get('billing-period').value = '2026-09';
  const loading = b.get('billing-form').onsubmit({ preventDefault() {} }); await settle();
  await b.get('disconnect').onclick();
  resolveBilling({ ok: true, status: 200, json: async () => ({ review: { currency: 'EUR', finalized: { amount_millicents: 25000 } }, documents: { documents: [{ id: 'private-statement', kind: 'statement' }] } }) });
  await loading;
  assert.equal(b.get('workspace').hidden, true);
  assert.equal(b.get('billing-documents').children.length, 0);
  assert.equal(b.get('billing-more').hidden, true);
});

test('a stale billing month response cannot replace a newer selection', async () => {
  let resolveOld;
  const billing = period => ({ review: { period, currency: 'EUR', closed: false, amount_millicents: 0, unresolved: 0 }, documents: { documents: [] } });
  const b = browser(path => {
    if (path.includes('/billing?period=2026-08')) return new Promise(resolve => { resolveOld = resolve; });
    if (path.includes('/billing?period=2026-09')) return { ok: true, status: 200, json: async () => billing('2026-09') };
  });
  await b.signIn(); b.get('billing-period').value = '2026-08';
  const old = b.get('billing-form').onsubmit({ preventDefault() {} }); await settle();
  b.get('billing-period').value = '2026-09'; await b.get('billing-form').onsubmit({ preventDefault() {} });
  resolveOld({ ok: true, status: 200, json: async () => billing('2026-08') }); await old;
  assert.match(b.get('billing-summary').textContent, /2026-09/);
});

// ADR-1708: permission projections remove actions and fence revoked access.
test('viewers can follow results without paid-run or billing controls', async () => {
  const b = browser(async path => {
    if (path.endsWith('/product')) {
      const data = fixture(); data.access = { role: 'member', permissions: { view_work: true, run_work: false, view_billing: false } };
      delete data.usage; data.quotes = {}; data.bindings = {}; data.catalog.spec.features = {};
      data.operations = [{ id: ids.op, name: 'export', state: 'running' }];
      return { ok: true, status: 200, json: async () => data };
    }
  });
  await b.signIn();
  assert.equal(b.get('features-section').hidden, true); assert.equal(b.get('features').children.length, 0);
  assert.equal(b.get('usage-section').hidden, true); assert.equal(b.get('billing-section').hidden, true);
  assert.equal(b.get('work-section').hidden, false); assert.deepEqual(b.get('work').children[0].children.map(e => e.textContent), ['Run details', 'Get help with this run']);
  assert.match(b.get('access-status').textContent, /member.*View work/);
  await b.get('billing-form').onsubmit({ preventDefault() {} });
  assert.equal(b.calls.filter(c => c.path.includes('/billing?') || c.path.endsWith('/start') || c.path.endsWith('/cancel')).length, 0);
});
test('operators can run and recover work without receiving billing usage', async () => {
  const b = browser(async path => {
    if (path.endsWith('/product')) {
      const data = fixture(); data.access = { role: 'operator', permissions: { view_work: true, run_work: true, view_billing: false } }; delete data.usage;
      return { ok: true, status: 200, json: async () => data };
    }
    if (path.endsWith('/start')) return { ok: true, status: 202, json: async () => ({ id: ids.op }) };
  });
  await b.signIn(); await b.submit(3);
  assert.equal(b.get('features-section').hidden, false); assert.equal(b.get('billing-section').hidden, true);
  assert.equal(b.calls.filter(c => c.path.endsWith('/start')).length, 1);
  assert.match(b.confirms[0], /EUR 0.25/);
  assert.match(b.get('work').children[0].textContent, /Completed/);
});
test('billing-only access skips work recovery and exposes billing controls', async () => {
  const storage = new Map(), first = browser(async path => { if (path.endsWith('/start')) throw new Error('lost response'); }, storage);
  await first.signIn(); await first.submit(4);
  const b = browser(async path => {
    if (path.endsWith('/product')) {
      const data = fixture(); data.access = { role: 'billing', permissions: { view_work: false, run_work: false, view_billing: true } }; data.catalog.spec.features = {}; data.bindings = {}; data.quotes = {};
      return { ok: true, status: 200, json: async () => data };
    }
  }, storage);
  await b.signIn();
  assert.equal(b.get('work-section').hidden, true); assert.equal(b.get('features-section').hidden, true);
  assert.equal(b.get('billing-section').hidden, false); assert.equal(b.get('usage-section').hidden, false);
  assert.equal(b.calls.filter(c => c.path.endsWith('/submissions/lookup') || c.path.includes('/operations/')).length, 0);
});
test('a permission change closes stale controls and fences pending billing bytes', async () => {
  let reduced = false, resolveBilling;
  const b = browser(async path => {
    if (path.endsWith('/product')) {
      const data = fixture();
      if (reduced) { data.access = { role: 'member', permissions: { view_work: true, run_work: false, view_billing: false } }; delete data.usage; }
      return { ok: true, status: 200, json: async () => data };
    }
    if (path.includes('/billing?')) return { ok: true, status: 200, json: () => new Promise(resolve => { resolveBilling = resolve; }) };
  });
  await b.signIn(); b.get('billing-period').value = '2026-09';
  const billing = b.get('billing-form').onsubmit({ preventDefault() {} }); await settle();
  reduced = true; await b.get('refresh').onclick();
  assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('login').hidden, false);
  assert.equal(b.get('features').children.length, 0); assert.match(b.get('message').textContent, /permissions changed/);
  resolveBilling({ review: { finalized: { amount_millicents: 99999 } }, documents: { documents: [] } }); await billing;
  assert.equal(b.get('billing-documents').children.length, 0); assert.doesNotMatch(b.get('billing-summary').textContent, /99999/);
});
test('missing permission metadata grants no browser actions', async () => {
  const b = browser(async path => {
    if (path.endsWith('/product')) { const data = fixture(); delete data.access; return { ok: true, status: 200, json: async () => data }; }
  });
  await b.signIn();
  for (const id of ['features-section', 'work-section', 'usage-section', 'billing-section']) assert.equal(b.get(id).hidden, true);
  assert.equal(b.get('features').children.length, 0); assert.equal(b.get('work').children.length, 0);
});
test('view-only recovery never offers a retry the current role cannot perform', async () => {
  const storage = new Map(), first = browser(async path => { if (path.endsWith('/start')) throw new Error('lost response'); }, storage);
  await first.signIn(); await first.submit(2);
  const b = browser(async path => {
    if (path.endsWith('/product')) {
      const data = fixture(); data.access = { role: 'member', permissions: { view_work: true, run_work: false, view_billing: false } }; delete data.usage; data.catalog.spec.features = {}; data.quotes = {}; data.bindings = {};
      return { ok: true, status: 200, json: async () => data };
    }
  }, storage);
  await b.signIn();
  assert.match(b.get('message').textContent, /current role cannot retry/);
  assert.equal(b.get('features').children.length, 0); assert.equal(b.calls.filter(c => c.path.endsWith('/start')).length, 0);
});

// ADR-1709: plan changes are explicit and recoverable independently of paid work.
const jsonResponse = data => ({ ok: true, status: 200, json: async () => data });
const missingPlan = () => ({ ok: false, status: 404, json: async () => ({ detail: 'Not recorded' }) });
const planTerms = plan => ({ plan, label: plan === 'pro' ? '<script>Pro</script>' : 'Starter', revision: quote, currency: 'EUR', features: [{ name: 'export', label: 'Export', monthly_starts: plan === 'pro' ? 100 : 10, unit_price_millicents: plan === 'pro' ? 10000 : 25000, starts: 1, remaining_starts: plan === 'pro' ? 99 : 9 }] });
const planChoices = () => ({ current: planTerms('starter'), offers: [planTerms('pro')], period: '2026-10', head_revision: quote, suspended: false });
const planPreview = (request, hash = quote) => ({ request, hash, before: planTerms('starter'), after: planTerms(request.plan), period: '2026-10' });
const planReceipt = preview => ({ id: preview.request.id, preview, actor_role: 'manager', effective_at: '2026-10-16T12:00:00Z' });
const planView = (plan = 'starter', manage = true) => {
  const data = fixture(); data.access = { role: manage ? 'manager' : 'billing', permissions: { manage_plan: manage, view_work: false, run_work: false, view_billing: !manage } };
  data.customer.plan = plan; data.catalog.spec.plans.pro = { label: '<script>Pro</script>', features: {} }; delete data.usage;
  data.catalog.spec.features = {}; data.quotes = {}; data.bindings = {}; return data;
};
const planKey = `gregale.product.plan.v1:${JSON.stringify([ids.account, ids.app, ids.tenant])}`;
function planAPI(custom = () => undefined) {
  return async (path, options) => {
    const supplied = await custom(path, options); if (supplied) return supplied;
    if (path.endsWith('/product')) return jsonResponse(planView());
    if (path.endsWith('/plans')) return jsonResponse(planChoices());
    if (path.endsWith('/plans/preview')) return jsonResponse(planPreview(JSON.parse(options.body)));
    if (path.includes('/plans/changes?')) return jsonResponse({ changes: [] });
    if (path.includes('/plans/changes/')) return missingPlan();
  };
}
const reviewPlan = async b => { b.get('plan-options').value = 'pro'; await b.get('plan-review-form').onsubmit({ preventDefault() {} }); await settle(); };
const textTree = el => `${el.textContent} ${el.children.map(textTree).join(' ')}`;

// ADR-1715: help submission is explicit, scoped and recoverable without retaining messages locally.
function helpBrowser(custom = () => undefined, storage = new Map(), url, rows = new Map()) {
  const state = { rows, posts: 0 };
  state.record = request => { const now = new Date().toISOString(); const record = { account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, request, context: { observed_at: now, revision: quote, policy_revision: quote, period: now.slice(0, 7), plan: 'starter', suspended: false, reason: request.kind === 'run' ? 'run_reported' : 'workspace_reported' }, status: 'open', response: '', version: 1, created_at: now, updated_at: now }; state.rows.set(request.id, record); return record; };
  const b = browser(async (path, options) => {
    const supplied = await custom(path, options, state); if (supplied) return supplied;
    if (path.includes('/help?')) return jsonResponse({ requests: [...state.rows.values()], observed_at: new Date().toISOString() });
    if (path.includes('/help/')) return teamResponse(state.rows.get(path.split('/').at(-1)) || { detail: 'Not found' }, state.rows.has(path.split('/').at(-1)) ? 200 : 404);
    if (path.endsWith('/help') && options.method === 'POST') { state.posts++; return jsonResponse(state.record(JSON.parse(options.body))); }
  }, storage, url);
  b.helpState = state;
  b.sendHelp = async (kind = 'workspace', message = '<script>Private help question</script>') => { b.get('help-kind').value = kind; b.get('help-kind').onchange(); b.get('help-message').value = message; b.get('help-feature').value = 'export'; b.get('help-run').value = ids.op; await b.get('help-form').onsubmit({ preventDefault() {} }); await settle(); };
  return b;
}

test('customer help submits only explicit references and renders messages and owner responses as text', async () => {
  const b = helpBrowser(); await b.signIn(); assert.equal(b.get('help-section').hidden, false);
  await b.sendHelp('run'); assert.equal(b.helpState.posts, 1);
  const request = JSON.parse(b.calls.find(c => c.path.endsWith('/help') && c.options.method === 'POST').options.body);
  assert.deepEqual(Object.keys(request).sort(), ['id', 'kind', 'message', 'operation_id']); assert.equal(request.operation_id, ids.op);
  assert.match(textTree(b.get('help-detail')), /<script>Private help question<\/script>/);
  const record = b.helpState.rows.get(request.id); record.status = 'resolved'; record.response = '<img src=x> We reviewed the request.'; record.version = 2;
  await b.get('help-refresh').onclick(); assert.match(textTree(b.get('help-detail')), /Resolved.*<img src=x>/);
  assert.ok(![...b.storage.values()].join('').includes('Private help question'));
  assert.equal(b.calls.filter(c => c.path.endsWith('/start') || c.path.endsWith('/cancel') || c.path.endsWith('/plans/confirm')).length, 0);
});

test('a lost help acknowledgement recovers by GET after reload without another submission', async () => {
  const storage = new Map();
  const first = helpBrowser((path, options, state) => { if (path.endsWith('/help') && options.method === 'POST') { state.posts++; state.record(JSON.parse(options.body)); throw new Error('lost acknowledgement'); } }, storage);
  await first.signIn(); await first.sendHelp(); assert.equal(first.helpState.posts, 1); assert.equal(first.get('help-check').hidden, false);
  const saved = JSON.parse([...storage.values()][0]); assert.deepEqual(Object.keys(saved).sort(), ['hash', 'id']);
  const second = helpBrowser(() => undefined, storage, 'https://portal.example/v1/product-control/portal?app=exports', first.helpState.rows); await waitFor(() => storage.size === 0);
  assert.equal(second.helpState.posts, 0); assert.match(textTree(second.get('help-detail')), /Private help question/); assert.equal(storage.size, 0);
  assert.ok(second.calls.some(call => call.path.endsWith(`/help/${saved.id}`)));
});

test('an unrecorded help request requires its original message and keeps its ID on explicit retry', async () => {
  let lost = true;
  const b = helpBrowser((path, options) => { if (lost && path.endsWith('/help') && options.method === 'POST') { lost = false; throw new Error('lost before acceptance'); } });
  await b.signIn(); await b.sendHelp(); const original = JSON.parse(b.calls.find(call => call.options.method === 'POST' && call.path.endsWith('/help')).options.body);
  await b.get('help-check').onclick(); assert.match(b.get('help-status').textContent, /not found/);
  await b.sendHelp('workspace', 'different details'); assert.equal(b.helpState.posts, 0);
  await b.sendHelp(); const submitted = [...b.helpState.rows.values()][0].request; assert.equal(submitted.id, original.id); assert.equal(submitted.message, original.message);
});

test('help refuses submission without durable metadata or after another tab replaces its reference', async () => {
  const b = helpBrowser(); await b.signIn(); b.context.localStorage.setItem = () => { throw new Error('storage full'); };
  await b.sendHelp(); assert.equal(b.helpState.posts, 0); assert.match(b.get('help-status').textContent, /storage full/);
  const second = helpBrowser(); await second.signIn();
  const key = `gregale.product.help.v1:${JSON.stringify([ids.account, ids.app, ids.tenant, ids.org])}`;
  second.storage.set(key, JSON.stringify({ id: randomUUID(), hash: quote, message: 'not permitted locally' }));
  await second.sendHelp(); assert.equal(second.helpState.posts, 0); assert.match(second.get('help-status').textContent, /saved help reference is invalid/);
});

test('a fresh help request rejected before acceptance can be corrected without a stuck recovery reference', async () => {
  let reject = true;
  const b = helpBrowser((path, options) => reject && path.endsWith('/help') && options.method === 'POST' ? teamResponse({ detail: 'Run reference unavailable' }, 404) : undefined);
  await b.signIn(); await b.sendHelp('run'); assert.equal(b.storage.size, 0); assert.equal(b.get('help-check').hidden, true);
  reject = false; await b.sendHelp('workspace', 'A corrected question'); assert.equal(b.helpState.posts, 1);
});

test('help scopes every response and clears old details after a failed or foreign history read', async () => {
  let foreign = false;
  const b = helpBrowser((path, _options, state) => { if (foreign && path.includes('/help?')) { const record = structuredClone([...state.rows.values()][0]); record.tenant_id = randomUUID(); return jsonResponse({ requests: [record] }); } });
  await b.signIn(); await b.sendHelp(); foreign = true; await b.get('help-refresh').onclick();
  assert.equal(b.get('help-history').children.length, 0); assert.equal(b.get('help-detail').children.length, 0); assert.match(b.get('help-status').textContent, /could not be checked/);
});

test('logout fences help creation and preserves only its non-secret recovery reference', async () => {
  let resolvePost;
  const b = helpBrowser((path, options, state) => { if (path.endsWith('/help') && options.method === 'POST') { const record = state.record(JSON.parse(options.body)); return new Promise(resolve => { resolvePost = () => resolve(jsonResponse(record)); }); } });
  await b.signIn(); const sending = b.sendHelp(); await waitFor(() => typeof resolvePost === 'function'); await b.get('disconnect').onclick(); resolvePost(); await sending;
  assert.equal(b.get('help-section').hidden, true); assert.equal(b.get('help-detail').children.length, 0); assert.equal(b.get('help-message').value, ''); assert.equal(b.storage.size, 1);
  assert.ok(![...b.storage.values()].join('').includes('Private help question'));
});

test('late help history cannot overwrite a newer observation or survive revoked access', async () => {
  let resolveOld, delay = false, revoked = false;
  const b = helpBrowser(path => { if (path.includes('/help?') && delay) { delay = false; return new Promise(resolve => { resolveOld = resolve; }); } if (revoked && path.includes('/help?')) return teamResponse({ detail: 'Access ended' }, 403); });
  await b.signIn(); await b.sendHelp(); delay = true; const old = b.get('help-refresh').onclick(); await settle();
  await b.get('help-refresh').onclick(); resolveOld(jsonResponse({ requests: [] })); await old; assert.match(textTree(b.get('help-history')), /Private help question/);
  revoked = true; await b.get('help-refresh').onclick(); assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('help-detail').children.length, 0);
});

test('feature and run help links prefill the form without sending a request', async () => {
  const data = guidanceView({ starts: 10 }); data.operations = [{ id: ids.op, name: 'export', state: 'failed' }];
  const b = helpBrowser(path => path.endsWith('/product') ? jsonResponse(data) : undefined); await b.signIn();
  await b.get('guidance-list').children[0].children.find(el => el.tag === 'button').onclick(); assert.equal(b.get('help-kind').value, 'feature'); assert.equal(b.get('help-feature').value, 'export');
  await b.get('work').children[0].children.at(-1).onclick(); assert.equal(b.get('help-kind').value, 'run'); assert.equal(b.get('help-run').value, ids.op); assert.equal(b.helpState.posts, 0);
});

// ADR-1714: current-month guidance shares the portal's scoped allowance facts.
function guidanceView({ starts = 8, limit = 10, run = true, billing = true, manage = false, suspended = false, available = true, action = 'ask_workspace_admin' } = {}) {
  const data = fixture(), observed = new Date(), reset = new Date(Date.UTC(observed.getUTCFullYear(), observed.getUTCMonth() + 1, 1));
  const remaining = Math.max(0, limit - starts), allowance = limit === 0 ? 'no_allowance' : starts >= limit ? 'exhausted' : starts * 100 >= limit * 80 ? 'near_limit' : 'available';
  const state = suspended ? 'access_paused' : !run ? 'permission_required' : !available ? 'temporarily_unavailable' : !remaining ? 'allowance_exhausted' : allowance === 'near_limit' ? 'near_limit' : 'available';
  data.customer.revision = quote; data.customer.suspended = suspended; data.catalog.revision = quote;
  data.access.permissions = { view_work: true, run_work: run, view_billing: billing, manage_plan: manage };
  data.remaining_starts = run || billing ? { export: remaining } : {};
  data.catalog.spec.plans.starter.features.export.monthly_starts = limit;
  data.bindings.export.available = available;
  if (!run) { data.catalog.spec.features = {}; data.bindings = {}; data.quotes = {}; }
  if (!billing) data.usage = null;
  data.guidance = { account_id: data.account_id, app_id: data.app_id, tenant_id: data.customer.tenant_id, revision: quote, policy_revision: quote,
    observed_at: observed.toISOString(), period: observed.toISOString().slice(0, 7), resets_at: reset.toISOString(), near_limit_percent: 80,
    run_permission: run, suspended, plan_action: action, features: run || billing ? [{ feature: 'export', label: '<script>Export</script>', starts, limit, remaining, allowance_state: allowance, state }] : [] };
  return data;
}

test('usage guidance shows near-limit progress, exhausted resets, zero grants and carried overuse', async () => {
  for (const [options, expected] of [[{}, /at least 80%/], [{ starts: 10 }, /next allowance period starts.*UTC/], [{ starts: 0, limit: 0 }, /plan includes no starts/], [{ starts: 12, limit: 5 }, /12 of 5 starts used · 0 remaining/]]) {
    const data = guidanceView(options), b = browser(path => path.endsWith('/product') ? jsonResponse(data) : undefined); await b.signIn();
    assert.equal(b.get('guidance-section').hidden, false); assert.match(textTree(b.get('guidance-list')), expected);
    assert.match(b.get('guidance-period').textContent, /Allowances reset .* UTC/);
    const card = b.get('guidance-list').children[0]; assert.equal(card.children[0].textContent, '<script>Export</script>');
    const meter = card.children.find(el => el.tag === 'label')?.children[0];
    if (options.limit === 0) assert.equal(meter, undefined); else { assert.equal(meter.max, data.guidance.features[0].limit); assert.equal(meter.value, Math.min(data.guidance.features[0].starts, meter.max)); }
    assert.equal(b.calls.filter(c => c.path.endsWith('/start')).length, 0);
  }
});

test('paused, unavailable and role-limited guidance does not suggest a plan change', async () => {
  for (const [options, expected] of [[{ suspended: true }, /paused/], [{ available: false }, /temporarily unavailable/], [{ run: false }, /role cannot run/], [{ run: false, billing: false }, /role does not allow/]]) {
    const data = guidanceView({ ...options, manage: true, action: 'review_plans' }), b = browser(planAPI(path => path.endsWith('/product') ? jsonResponse(data) : undefined)); await b.signIn();
    assert.match(textTree(b.get('guidance-status')) + textTree(b.get('guidance-list')), expected);
    assert.equal(b.get('guidance-actions').children.length, 0);
    if (!options.run && options.billing === false) assert.equal(b.get('guidance-list').children.length, 0);
  }
});

test('guidance reviews only offered authorized plans and never confirms or starts automatically', async () => {
  for (const [manage, action, button] of [[true, 'review_plans', true], [false, 'review_plans', false], [true, 'contact_product_team', false]]) {
    const data = guidanceView({ manage, action }), b = browser(planAPI(path => path.endsWith('/product') ? jsonResponse(data) : undefined)); await b.signIn();
    const buttons = b.get('guidance-actions').children.filter(el => el.tag === 'button'); assert.equal(buttons.length, Number(button));
    if (button) {
      const before = b.calls.filter(c => c.path.endsWith('/plans')).length;
      await buttons[0].onclick(); await settle(); assert.equal(b.calls.filter(c => c.path.endsWith('/plans')).length, before + 1);
      await b.get('disconnect').onclick(); const calls = b.calls.length; await buttons[0].onclick(); assert.equal(b.calls.length, calls);
    }
    assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm') || c.path.endsWith('/plans/preview') || c.path.endsWith('/start')).length, 0);
  }
});

test('guidance clears invalid scope or failed refresh instead of retaining an old warning', async () => {
  for (const change of [data => { data.guidance.tenant_id = randomUUID(); }, data => { data.guidance.features[0].remaining++; }, data => { data.guidance.resets_at = data.guidance.observed_at; }]) {
    const data = guidanceView(); change(data); const b = browser(path => path.endsWith('/product') ? jsonResponse(data) : undefined); await b.signIn();
    assert.match(b.get('guidance-status').textContent, /could not be checked/); assert.equal(b.get('guidance-list').children.length, 0);
  }
  let fail = false;
  const b = browser(path => { if (path.endsWith('/product')) { if (fail) throw new Error('read failed'); return jsonResponse(guidanceView()); } });
  await b.signIn(); fail = true; await b.get('refresh').onclick(); assert.match(b.get('guidance-status').textContent, /could not be checked/); assert.equal(b.get('guidance-list').children.length, 0);
  await b.get('disconnect').onclick(); assert.equal(b.get('guidance-section').hidden, true);
});

test('a late poll cannot replace guidance from a newer refresh', async () => {
  let delay = false, resolveOld;
  const b = browser(path => { if (path.endsWith('/product')) { if (delay) { delay = false; return new Promise(resolve => { resolveOld = resolve; }); } return jsonResponse(guidanceView({ starts: resolveOld ? 10 : 8 })); } });
  await b.signIn(); delay = true; const old = b.timers.find(timer => timer.ms === 4000).task(); await settle();
  await b.get('refresh').onclick(); resolveOld(jsonResponse(guidanceView({ starts: 8 }))); await old; await settle();
  assert.match(textTree(b.get('guidance-list')), /10 of 10 starts used/); assert.doesNotMatch(textTree(b.get('guidance-list')), /at least 80%/);
});

test('an exhausted allowance preserves lookup recovery for an uncertain saved submission', async () => {
  const storage = new Map();
  const first = browser(path => path.endsWith('/product') ? jsonResponse(guidanceView({ starts: 9 })) : path.endsWith('/start') ? Promise.reject(new Error('lost acknowledgement')) : undefined, storage);
  await first.signIn(); await first.submit(8); assert.equal(first.calls.filter(c => c.path.endsWith('/start')).length, 1);
  const second = browser(path => path.endsWith('/product') ? jsonResponse(guidanceView({ starts: 10 })) : path.endsWith('/submissions/lookup') ? jsonResponse({ state: 'accepted', receipt: { id: ids.op } }) : undefined, storage, 'https://portal.example/v1/product-control/portal?app=exports');
  await settle(); assert.match(textTree(second.get('guidance-list')), /monthly allowance is used up/); assert.match(textTree(second.get('work')), /Completed/);
  assert.equal(second.calls.filter(c => c.path.endsWith('/start')).length, 0); assert.equal(JSON.parse([...storage.values()][0]).operationID, ids.op);
});

test('plan managers compare exact prices and carried usage, then explicitly confirm', async () => {
  let accepted;
  const b = browser(planAPI((path, options) => {
    if (path.endsWith('/plans/confirm')) { const value = JSON.parse(options.body); accepted = planReceipt(planPreview(value.request, value.expected_hash)); return jsonResponse(accepted); }
    if (path.endsWith('/product') && accepted) return jsonResponse(planView('pro'));
    if (path.includes('/plans/changes?') && accepted) return jsonResponse({ changes: [accepted] });
  }));
  await b.signIn();
  assert.equal(b.get('plans-section').hidden, false); assert.equal(b.get('features-section').hidden, true); assert.equal(b.get('billing-section').hidden, true);
  assert.equal(b.calls.filter(c => c.options.method === 'POST' && c.path.includes('/plans')).length, 0);
  await reviewPlan(b);
  assert.match(textTree(b.get('plan-comparison')), /EUR 0.25.*10 starts\/month.*1 used.*9 remaining/);
  assert.match(textTree(b.get('plan-comparison')), /EUR 0.10.*100 starts\/month.*99 remaining/);
  assert.match(textTree(b.get('plan-comparison')), /Monthly fee.*No monthly fee.*No monthly fee/);
  assert.equal(b.get('plan-options').children[0].textContent, '<script>Pro</script>');
  assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 0);
  await b.get('plan-confirm').onclick();
  assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 1); assert.match(b.confirms[0], /Consumed starts are not reset/);
  assert.match(b.get('plan-status').textContent, /Recorded Starter → <script>Pro<\/script>/);
  assert.match(textTree(b.get('plan-history')), /Reference/); assert.equal(b.storage.has(planKey), false);
  assert.equal(b.calls.filter(c => c.path.endsWith('/start')).length, 0);
});

test('a lost plan-change response is recovered on reload by reading its receipt', async () => {
  const storage = new Map(); let accepted;
  const first = browser(planAPI((path, options) => {
    if (path.endsWith('/plans/confirm')) { const value = JSON.parse(options.body); accepted = planReceipt(planPreview(value.request, value.expected_hash)); throw new Error('lost response'); }
  }), storage);
  await first.signIn(); await reviewPlan(first); await first.get('plan-confirm').onclick();
  const pending = JSON.parse(storage.get(planKey));
  assert.deepEqual(Object.keys(pending).sort(), ['expected_hash', 'request']); assert.equal(pending.request.id, accepted.id);
  assert.ok(!storage.get(planKey).includes('unit_price')); assert.ok(!storage.get(planKey).includes('password'));
  assert.match(first.get('message').textContent, /reference is saved/);
  const restored = browser(planAPI(path => {
    if (path.includes(`/plans/changes/${accepted.id}`)) return jsonResponse(accepted);
    if (path.endsWith('/product')) return jsonResponse(planView('pro'));
  }), storage, 'https://portal.example/v1/product-control/portal?app=exports');
  await settle();
  assert.match(restored.get('plan-status').textContent, /Recorded/); assert.equal(storage.has(planKey), false);
  assert.equal(restored.calls.filter(c => c.path.endsWith('/plans/confirm') || c.path.endsWith('/plans/preview')).length, 0);
});

test('an unrecorded plan change needs a new review and confirmation with the saved reference', async () => {
  let attempts = 0, changed = false; const bodies = [];
  const b = browser(planAPI((path, options) => {
    if (path.endsWith('/plans/preview')) return jsonResponse(planPreview(JSON.parse(options.body), changed ? changedQuote : quote));
    if (path.endsWith('/plans/confirm')) { bodies.push(JSON.parse(options.body)); if (++attempts === 1) throw new Error('connection lost'); return jsonResponse(planReceipt(planPreview(bodies.at(-1).request, bodies.at(-1).expected_hash))); }
  }));
  await b.signIn(); await reviewPlan(b); await b.get('plan-confirm').onclick();
  await b.get('plan-check').onclick(); assert.match(b.get('plan-status').textContent, /not recorded.*same reference/); assert.equal(attempts, 1);
  changed = true; await reviewPlan(b); assert.equal(attempts, 1); await b.get('plan-confirm').onclick();
  assert.equal(attempts, 2); assert.equal(bodies[0].request.id, bodies[1].request.id); assert.notEqual(bodies[0].expected_hash, bodies[1].expected_hash);
});

test('plan confirmation refuses to replace recovery details changed by another tab', async () => {
  const b = browser(planAPI()); await b.signIn(); await reviewPlan(b);
  const other = { request: { id: randomUUID(), plan: 'pro', revision: quote }, expected_hash: quote };
  b.storage.set(planKey, JSON.stringify(other)); await b.get('plan-confirm').onclick();
  assert.match(b.get('message').textContent, /another tab/); assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 0);
  assert.equal(b.storage.get(planKey), JSON.stringify(other));
});

test('plan confirmation never posts when durable recovery storage fails', async () => {
  const b = browser(planAPI()); await b.signIn(); await reviewPlan(b);
  b.context.localStorage.setItem = () => { throw new Error('storage unavailable'); };
  await b.get('plan-confirm').onclick(); assert.match(b.get('message').textContent, /storage unavailable/);
  assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 0);
});

test('plan review bytes are fenced after logout and permission revocation', async () => {
  for (const action of ['logout', 'revocation']) {
    let resolvePreview, revoked = false;
    const b = browser(planAPI(path => {
      if (path.endsWith('/plans/preview')) return new Promise(resolve => { resolvePreview = resolve; });
      if (path.endsWith('/product')) return jsonResponse(planView('starter', !revoked));
    }));
    await b.signIn(); b.get('plan-options').value = 'pro';
    const loading = b.get('plan-review-form').onsubmit({ preventDefault() {} }); await settle();
    if (action === 'logout') await b.get('disconnect').onclick(); else { revoked = true; await b.get('refresh').onclick(); }
    const request = JSON.parse(b.calls.find(c => c.path.endsWith('/plans/preview')).options.body);
    resolvePreview(jsonResponse(planPreview(request))); await loading;
    assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('plan-comparison').children.length, 0); assert.equal(b.get('plan-confirm').hidden, true);
    assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 0);
  }
});

test('plan history uses explicit cursors and a prior recorded confirmation reports its actual terms', async () => {
  const id = randomUUID(), prior = planReceipt(planPreview({ id, plan: 'starter', revision: quote }));
  const saved = { request: { id, plan: 'pro', revision: changedQuote }, expected_hash: changedQuote };
  const storage = new Map([[planKey, JSON.stringify(saved)]]);
  const b = browser(planAPI(path => {
    if (path.endsWith(`/plans/changes/${id}`)) return jsonResponse(prior);
    if (path.includes('/plans/changes?')) return jsonResponse({ changes: [prior], next_cursor: path.endsWith('cursor=') ? 'next-page' : '' });
  }), storage);
  await b.signIn(); assert.match(b.get('plan-status').textContent, /Recorded Starter → Starter/); assert.equal(storage.has(planKey), false);
  assert.equal(b.get('plan-history-more').hidden, false); await b.get('plan-history-more').onclick();
  assert.equal(b.get('plan-history-more').hidden, true); assert.ok(b.calls.some(c => c.path.endsWith('cursor=next-page')));
  assert.equal(b.calls.filter(c => c.path.endsWith('/plans/confirm')).length, 0);
});

// ADR-1717: the inbox observes retained sources; acknowledgement changes only shared read metadata.
function notificationBrowser(custom = () => undefined) {
  const id = randomUUID();
  const state = { items: [{ id: `help/${id}`, reference_id: id, kind: 'help_replied', help_kind: 'workspace', occurred_at: new Date().toISOString(), revision: quote, read: false, read_version: 0 }] };
  state.page = (items = state.items, extra = {}) => ({ account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, items: items.map(row => ({ ...row })), unread_count: state.items.filter(row => !row.read).length, observed_at: new Date().toISOString(), window_start: new Date(Date.now() - 30 * 86400000).toISOString(), read_state_scope: 'workspace', ...extra });
  state.mark = request => { const row = state.items.find(item => item.id === request.id); row.read = request.read; row.read_version++; return { ...row }; };
  const b = helpBrowser(async (path, options) => {
    const supplied = await custom(path, options, state); if (supplied) return supplied;
    if (path.includes('/notifications?')) return jsonResponse(state.page(state.items.filter(item => !path.includes('filter=unread') || !item.read)));
    if (path.endsWith('/notifications/read')) return jsonResponse(state.mark(JSON.parse(options.body)));
  });
  b.notificationState = state;
  b.markUpdate = async (index = 0) => { await b.get('notifications-list').children[index].children.at(-1).onclick(); await settle(); };
  return b;
}

test('inbox marks shared read and unread state only after an explicit choice', async () => {
  const b = notificationBrowser(); await b.signIn();
  assert.equal(b.get('notifications-section').hidden, false); assert.match(b.get('notifications-open').textContent, /1 unread/);
  assert.match(b.get('notifications-window').textContent, /shared within this workspace/);
  assert.equal(b.calls.filter(c => c.path.endsWith('/notifications/read')).length, 0);
  await b.markUpdate(); assert.match(textTree(b.get('notifications-list')), /Read · Help request updated/);
  let body = JSON.parse(b.calls.find(c => c.path.endsWith('/notifications/read')).options.body);
  assert.deepEqual(body, { id: b.notificationState.items[0].id, revision: quote, expected_version: 0, read: true });
  assert.match(b.get('notifications-open').textContent, /0 unread/);
  await b.markUpdate(); body = JSON.parse(b.calls.filter(c => c.path.endsWith('/notifications/read')).at(-1).options.body);
  assert.equal(body.expected_version, 1); assert.equal(body.read, false); assert.match(b.get('notifications-open').textContent, /1 unread/);
  assert.equal(b.storage.size, 0); assert.equal(b.calls.filter(c => c.path.endsWith('/start') || c.path.endsWith('/cancel') || c.path.endsWith('/plans/confirm')).length, 0);
});

test('a newer source becomes unread on polling and uses its current revision', async () => {
  const b = notificationBrowser(); await b.signIn(); await b.markUpdate();
  const row = b.notificationState.items[0]; row.revision = changedQuote; row.kind = 'help_resolved'; row.read = false;
  await b.timers.find(timer => timer.ms === 4000).task(); await settle();
  assert.match(textTree(b.get('notifications-list')), /Unread · Help request resolved/);
  await b.markUpdate(); const body = JSON.parse(b.calls.filter(c => c.path.endsWith('/notifications/read')).at(-1).options.body);
  assert.equal(body.revision, changedQuote); assert.equal(body.expected_version, 1);
});

test('lost and conflicting read acknowledgements refresh by GET without resubmission', async () => {
  for (const failure of ['lost', 'conflict']) {
    const b = notificationBrowser((path, options, state) => {
      if (!path.endsWith('/notifications/read')) return;
      if (failure === 'lost') { state.mark(JSON.parse(options.body)); throw new Error('lost response'); }
      state.items[0].revision = changedQuote; state.items[0].kind = 'help_resolved'; return teamResponse({ detail: 'Update changed' }, 409);
    });
    await b.signIn(); await b.markUpdate();
    assert.match(b.get('notifications-status').textContent, /Read status was refreshed/);
    assert.equal(b.calls.filter(c => c.path.endsWith('/notifications/read')).length, 1);
    assert.match(textTree(b.get('notifications-list')), failure === 'lost' ? /Read · Help request updated/ : /Unread · Help request resolved/);
    assert.equal(b.storage.size, 0);
  }
});

test('late inbox reads cannot restore stale unread state after an acknowledgement', async () => {
  let resolveOld, delay = false;
  const b = notificationBrowser((path, options, state) => {
    if (path.includes('/notifications?') && delay) { delay = false; const old = state.page(); return new Promise(resolve => { resolveOld = () => resolve(jsonResponse(old)); }); }
  });
  await b.signIn(); delay = true; const old = b.get('notifications-refresh').onclick(); await settle();
  await b.markUpdate(); resolveOld(); await old;
  assert.match(b.get('notifications-open').textContent, /0 unread/); assert.match(textTree(b.get('notifications-list')), /Read ·/);
});

test('inbox pagination is explicit and unread filtering starts a fresh page', async () => {
  const b = notificationBrowser((path, options, state) => {
    if (!path.includes('/notifications?')) return;
    const older = path.includes('cursor=older');
    return jsonResponse(state.page(older || path.includes('filter=unread') ? [] : state.items, { next_cursor: older || path.includes('filter=unread') ? '' : 'older' }));
  });
  await b.signIn(); assert.equal(b.get('notifications-more').hidden, false);
  await b.get('notifications-more').onclick(); assert.equal(b.get('notifications-more').hidden, true);
  const count = b.calls.filter(c => c.path.includes('/notifications?')).length;
  await b.timers.find(timer => timer.ms === 4000).task(); await settle();
  assert.equal(b.calls.filter(c => c.path.includes('/notifications?')).length, count);
  b.get('notifications-filter').value = 'unread'; await b.get('notifications-filter').onchange();
  assert.ok(b.calls.some(c => c.path.endsWith('/notifications?filter=unread&cursor=')));
  assert.match(textTree(b.get('notifications-list')), /No unread updates/);
});

test('inbox links open the existing help and run readers without marking or executing', async () => {
  const b = notificationBrowser();
  const record = b.helpState.record({ id: b.notificationState.items[0].reference_id, kind: 'workspace', message: 'Original question' }); record.response = '<script>Latest response</script>'; record.status = 'resolved'; record.version = 2;
  b.notificationState.items.push({ id: `work/${ids.op}`, reference_id: ids.op, kind: 'work_succeeded', feature: '<img src=x>', occurred_at: new Date().toISOString(), revision: changedQuote, read: false, read_version: 0 });
  await b.signIn(); await b.get('notifications-list').children[0].children.at(-2).onclick();
  assert.match(textTree(b.get('help-detail')), /<script>Latest response<\/script>/);
  await b.get('notifications-list').children[1].children.at(-2).onclick();
  assert.ok(b.calls.some(c => c.path.endsWith(`/operations/${ids.op}`))); assert.match(textTree(b.get('notifications-list')), /Feature: <img src=x>/);
  assert.equal(b.calls.filter(c => c.path.endsWith('/notifications/read') || c.path.endsWith('/start') || c.path.endsWith('/cancel')).length, 0);
});

test('inbox rejects wrong workspace and unauthorized notification projections', async () => {
  for (const mutate of [page => { page.tenant_id = randomUUID(); }, page => { page.items[0].kind = 'unrecognized'; }, page => { page.items[0].reference_id = randomUUID(); }, page => { page.items[0].help_kind = 'private'; }]) {
    const b = notificationBrowser((path, options, state) => { if (path.includes('/notifications?')) { const page = state.page(); mutate(page); return jsonResponse(page); } });
    await b.signIn(); assert.equal(b.get('notifications-list').children.length, 0); assert.equal(b.get('notifications-open').textContent, 'Updates');
    assert.match(b.get('notifications-status').textContent, /could not be checked/);
  }
  const b = notificationBrowser(path => { if (path.endsWith('/product')) { const v = fixture(); v.access.permissions = { manage_team: true }; return jsonResponse(v); } });
  b.notificationState.items[0].help_kind = 'feature'; await b.signIn(); assert.equal(b.get('notifications-list').children.length, 0);
});

test('logout fences pending notification reads and acknowledgements', async () => {
  for (const operation of ['read', 'acknowledge']) {
    let resolvePending, delay = false;
    const b = notificationBrowser((path, options, state) => {
      if (delay && (operation === 'read' ? path.includes('/notifications?') : path.endsWith('/notifications/read'))) return new Promise(resolve => { resolvePending = () => resolve(jsonResponse(operation === 'read' ? state.page() : state.mark(JSON.parse(options.body)))); });
    });
    await b.signIn(); delay = true;
    const pending = operation === 'read' ? b.get('notifications-refresh').onclick() : b.markUpdate();
    await waitFor(() => !!resolvePending); await b.get('disconnect').onclick(); resolvePending(); await pending;
    assert.equal(b.get('notifications-section').hidden, true); assert.equal(b.get('notifications-list').children.length, 0); assert.equal(b.get('notifications-open').textContent, 'Updates'); assert.equal(b.get('workspace').hidden, true);
  }
});

test('revoked access and changed permissions clear the inbox', async () => {
  for (const change of ['revoked', 'permissions']) {
    let changed = false;
    const b = notificationBrowser(path => {
      if (changed && change === 'revoked' && path.includes('/notifications?')) return teamResponse({ detail: 'Access ended' }, 403);
      if (changed && change === 'permissions' && path.endsWith('/product')) { const v = fixture(); v.access.permissions = { view_work: true }; return jsonResponse(v); }
    });
    await b.signIn(); changed = true;
    if (change === 'revoked') await b.get('notifications-refresh').onclick(); else await b.get('refresh').onclick();
    assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('notifications-list').children.length, 0);
  }
});

test('a rejected notification acknowledgement clears access without automatically replaying its POST', async () => {
  const b = notificationBrowser(path => path.endsWith('/notifications/read') ? teamResponse({ detail: 'Session ended' }, 401) : undefined);
  await b.signIn(); await b.markUpdate();
  assert.equal(b.calls.filter(c => c.path.endsWith('/notifications/read')).length, 1);
  assert.equal(b.calls.filter(c => c.path.endsWith('/session/refresh')).length, 0);
  assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('notifications-list').children.length, 0);
});

test('personal email preferences save only categories and version and show truthful local status', async () => {
  let prefs = { work_outcomes: false, help_replies: false, version: 0, mode: 'local', available: true, delivery_page_size: 25 };
  const b = browser((path, options) => {
    if (path.endsWith('/email-preferences')) {
      if (options.method === 'PUT') { const body = JSON.parse(options.body); assert.deepEqual(Object.keys(body).sort(), ['expected_version', 'help_replies', 'work_outcomes']); prefs = { ...prefs, ...body, version: prefs.version + 1 }; }
      return { ok: true, status: 200, json: async () => prefs };
    }
    if (path.endsWith('/email-deliveries')) return { ok: true, status: 200, json: async () => ({ deliveries: [{ notification_id: `work/${ids.op}`, status: 'captured', attempts: 1, updated_at: new Date().toISOString() }] }) };
  });
  await b.signIn(); assert.equal(b.get('email-section').hidden, false); assert.equal(b.get('email-work').checked, false); assert.equal(b.get('email-help').checked, false);
  assert.match(b.get('email-mode').textContent, /No email is sent/); assert.match(textTree(b.get('email-deliveries')), /Captured locally/);
  b.get('email-help').checked = true; await b.get('email-preferences-form').onsubmit({ preventDefault() {} });
  assert.equal(prefs.help_replies, true); assert.equal(prefs.work_outcomes, false); assert.equal(prefs.expected_version, 0);
  assert.equal(b.calls.filter(c => c.path.endsWith('/email-preferences') && c.options.method === 'PUT').length, 1);
  assert.equal(b.calls.filter(c => c.path.endsWith('/notifications/read')).length, 0); assert.equal(b.storage.size, 0);
});

test('uncertain email preference saves reload state and never resubmit automatically', async () => {
  let saved = false;
  const b = browser((path, options) => {
    if (path.endsWith('/email-preferences')) {
      if (options.method === 'PUT') { saved = true; throw new Error('Response lost'); }
      return { ok: true, status: 200, json: async () => ({ work_outcomes: saved, help_replies: false, version: saved ? 1 : 0, mode: 'local', available: true, delivery_page_size: 25 }) };
    }
  });
  await b.signIn(); b.get('email-work').checked = true; await b.get('email-preferences-form').onsubmit({ preventDefault() {} });
  assert.match(b.get('email-status').textContent, /Response lost.*Preferences reloaded/); assert.equal(b.get('email-work').checked, true);
  assert.equal(b.calls.filter(c => c.path.endsWith('/email-preferences') && c.options.method === 'PUT').length, 1);
});

test('email responses arriving after sign-out cannot restore personal settings', async () => {
  let release, delayed = false;
  const b = browser((path) => {
    if (delayed && path.endsWith('/email-preferences')) return new Promise(resolve => { release = () => resolve({ ok: true, status: 200, json: async () => ({ work_outcomes: true, help_replies: true, version: 9, mode: 'local', available: true, delivery_page_size: 25 }) }); });
  });
  await b.signIn(); delayed = true; const pending = b.get('email-refresh').onclick(); await waitFor(() => release); await b.get('disconnect').onclick(); release(); await pending;
  assert.equal(b.get('email-section').hidden, true); assert.equal(b.get('email-work').checked, false); assert.equal(b.get('email-help').checked, false); assert.equal(b.get('email-deliveries').children.length, 0);
});

test('provider delivery reports are distinct from acceptance and saving cannot clear a complaint pause', async () => {
  const at = new Date().toISOString();
  let saves = 0;
  const b = browser((path, options) => {
    if (path.endsWith('/email-preferences')) {
      if (options.method === 'PUT') saves++;
      return { ok: true, status: 200, json: async () => ({ work_outcomes: true, help_replies: true, version: 1 + saves, mode: 'resend', available: true, pause_reason: 'complaint', delivery_page_size: 25 }) };
    }
    if (path.endsWith('/email-deliveries')) return { ok: true, status: 200, json: async () => ({ deliveries: [{ notification_id: `help/${ids.op}`, status: 'complained', attempts: 1, updated_at: at, reported_at: at }] }) };
  });
  await b.signIn();
  assert.match(b.get('email-mode').textContent, /receiving-server delivery reports do not confirm inbox placement or reading/);
  assert.match(b.get('email-status').textContent, /paused after a complaint/);
  assert.match(textTree(b.get('email-deliveries')), /Provider reported a complaint/);
  assert.match(textTree(b.get('email-deliveries')), /Provider reported /);
  await b.get('email-preferences-form').onsubmit({ preventDefault() {} });
  assert.equal(saves, 1);
  assert.match(b.get('email-status').textContent, /remain paused/);
  assert.equal(b.storage.size, 0);
});

test('notification recovery repeats an uncertain request unchanged and never replays cancelled messages', async () => {
  const at = new Date().toISOString(); let recovered = false, posted;
  const b = browser((path, options) => {
    if (path.endsWith('/email-preferences')) return { ok: true, status: 200, json: async () => ({ work_outcomes: true, help_replies: true, version: recovered ? 2 : 1, generation: recovered ? 2 : 1, pause_reason: recovered ? '' : 'hard_bounce', paused_at: recovered ? undefined : at, recovery_blocked_reason: recovered ? undefined : 'verified_replacement_required', mode: 'resend', available: true, delivery_page_size: 25 }) };
    if (path.endsWith('/email-recovery')) {
      const request = JSON.parse(options.body);
      if (!posted) { posted = request; recovered = true; throw new Error('Response lost'); }
      assert.deepEqual(request, posted);
      return { ok: true, status: 200, json: async () => ({ id: request.id, expected_version: 1, previous_generation: 1, version: 2, generation: 2, work_outcomes: true, help_replies: true, recovered_at: at }) };
    }
  });
  await b.signIn(); assert.equal(b.get('email-recover').hidden, false);
  assert.match(b.get('email-recovery-guidance').textContent, /different sign-in email.*cancelled messages stay cancelled/);
  await b.get('email-recover').onclick();
  assert.match(b.get('email-status').textContent, /Response lost.*may have completed/);
  assert.equal(b.get('email-save').disabled, true);
  assert.equal(b.calls.filter(c => c.path.endsWith('/email-recovery')).length, 1);
  await b.get('email-recover').onclick();
  assert.match(b.get('email-status').textContent, /Recovery confirmed.*Only future eligible updates/);
  assert.equal(b.get('email-recover').hidden, true); assert.equal(b.storage.size, 0);
});

test('complaint review and legacy evidence never offer automatic notification recovery', async () => {
  for (const [reason, blocked, message] of [['complaint', 'complaint_review', /requires review/], ['hard_bounce', 'recipient_evidence_unavailable', /no retained address evidence/]]) {
    const b = browser(path => path.endsWith('/email-preferences') ? { ok: true, status: 200, json: async () => ({ work_outcomes: true, help_replies: false, version: 3, generation: 2, pause_reason: reason, recovery_blocked_reason: blocked, mode: 'resend', available: true, delivery_page_size: 25 }) } : undefined);
    await b.signIn(); assert.equal(b.get('email-recover').hidden, true); assert.match(b.get('email-recovery-guidance').textContent, message);
    await b.get('email-recover').onclick(); assert.equal(b.calls.filter(c => c.path.endsWith('/email-recovery')).length, 0);
  }
});

test('notification recovery arriving after sign-out cannot restore personal history', async () => {
  let release;
  const b = browser((path, options) => {
    if (path.endsWith('/email-preferences')) return { ok: true, status: 200, json: async () => ({ work_outcomes: true, help_replies: false, version: 1, generation: 1, pause_reason: 'hard_bounce', recovery_blocked_reason: 'verified_replacement_required', mode: 'resend', available: true, delivery_page_size: 25 }) };
    if (path.endsWith('/email-recovery')) return new Promise(resolve => { release = () => { const r = JSON.parse(options.body); resolve({ ok: true, status: 200, json: async () => ({ ...r, previous_generation: 1, version: 2, generation: 2, recovered_at: new Date().toISOString() }) }); }; });
  });
  await b.signIn(); const pending = b.get('email-recover').onclick(); await waitFor(() => release);
  await b.get('disconnect').onclick(); release(); await pending;
  assert.equal(b.get('email-section').hidden, true); assert.equal(b.get('email-recover').hidden, true); assert.equal(b.get('email-recoveries').children.length, 0);
});

// The activity timeline is read-only and uses a different projection from the inbox.
const activityEvent = (kind, reference_id = randomUUID(), details = {}, at = '2026-10-16T12:00:00.000001Z') => ({ id: `${kind}/${reference_id}`, kind, reference_id, occurred_at: at, ...details });
function activityBrowser(custom = () => undefined) {
  const planID = randomUUID(), helpID = randomUUID();
  const state = { permissions: { view_work: true, run_work: true, view_billing: true, manage_plan: true }, events: [
    activityEvent('work_succeeded', ids.op, { feature: 'export' }), activityEvent('plan_changed', planID, { plan: { before: 'starter', after: 'pro' } }),
    activityEvent('statement_finalized', randomUUID(), { billing: { period: '2026-09', currency: 'EUR', amount_millicents: 25000 } }),
    activityEvent('credit_recorded', randomUUID(), { billing: { period: '2026-09', currency: 'EUR', amount_millicents: -1000 } }), activityEvent('help_resolved', helpID, { help_kind: 'workspace' }),
  ].sort((a, b) => b.kind.localeCompare(a.kind)), next: '' };
  state.page = (filter = 'all', events = state.events) => ({ account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, filter, events: structuredClone(events), observed_at: new Date().toISOString(), history_scope: 'retained_product_control_records', next_cursor: state.next });
  const b = helpBrowser(planAPI(async (path, options) => {
    const supplied = await custom(path, options, state); if (supplied) return supplied;
    if (path.endsWith('/product')) { const v = fixture(); v.access.permissions = { ...state.permissions }; return jsonResponse(v); }
    if (path.includes('/activity?')) { const filter = new URL(path, 'https://portal.example').searchParams.get('filter'); return jsonResponse(state.page(filter, state.events.filter(e => filter === 'all' || e.kind.startsWith(filter === 'plans' ? 'plan_' : filter === 'billing' ? 'statement_' : `${filter}_`) || filter === 'billing' && e.kind === 'credit_recorded'))); }
    if (path.endsWith(`/plans/changes/${planID}`)) return jsonResponse(planReceipt(planPreview({ id: planID, plan: 'pro', revision: quote })));
    if (path.includes('/billing?')) return jsonResponse({ review: { currency: 'EUR', finalized: { amount_millicents: 25000 } }, documents: { documents: [{ id: 'statement', kind: 'statement', currency: 'EUR', amount_millicents: 25000 }] } });
  }));
  const help = b.helpState.record({ id: helpID, kind: 'workspace', message: '<script>Private question</script>' }); help.status = 'resolved'; help.version = 2; help.response = '<script>Private answer</script>';
  b.activityState = state;
  b.openActivity = async kind => { const row = b.get('activity-list').children[state.events.findIndex(e => e.kind === kind)]; await row.children.at(-1).onclick(); await settle(); };
  return b;
}

test('activity joins customer records and opens permitted source views using only GET', async () => {
  const b = activityBrowser(); await b.signIn();
  assert.equal(b.get('activity-panel').hidden, false); assert.equal(b.get('activity-list').children.length, 5);
  const options = [...b.get('activity-filter').children]; await b.get('refresh').onclick();
  assert.ok(options.every((option, i) => option === b.get('activity-filter').children[i]), 'normal workspace refresh must preserve an open activity selector');
  assert.match(textTree(b.get('activity-list')), /Run completed.*Statement finalized.*Plan change confirmed.*Help request resolved.*Credit note recorded/);
  assert.ok(!textTree(b.get('activity-list')).includes('Private question')); assert.ok(!textTree(b.get('activity-list')).includes('Private answer'));
  await b.openActivity('work_succeeded'); assert.equal(b.get('run-detail').hidden, false); assert.match(textTree(b.get('run-detail-content')), /Plan and allowance/);
  await b.get('run-detail-content').children.find(e => e.textContent === 'View result and files').onclick(); assert.equal(b.get('result').hidden, false);
  await reviewPlan(b); const comparison = textTree(b.get('plan-comparison'));
  await b.openActivity('plan_changed'); assert.match(textTree(b.get('plan-history-focus')), /Selected plan change.*Starter → <script>Pro<\/script>/);
  assert.equal(b.get('plan-confirm').hidden, false); assert.equal(textTree(b.get('plan-comparison')), comparison);
  await b.openActivity('statement_finalized'); assert.equal(b.get('billing-period').value, '2026-09'); assert.match(b.get('billing-summary').textContent, /2026-09.*Finalized charges/);
  await b.openActivity('help_resolved'); assert.match(textTree(b.get('help-detail')), /Private answer/);
  assert.match(textTree(b.get('activity-list')), /does not confirm a refund/);
  assert.equal(b.calls.filter(c => /\/(start|confirm|read|cancel)$/.test(c.path)).length, 0); assert.equal(b.storage.size, 0);
  for (const c of b.calls.filter(c => /\/(activity|plans\/changes|operations|help|billing)/.test(c.path))) { assert.equal(c.options.method || 'GET', 'GET'); assert.equal(c.options.headers.Authorization, undefined); }
});

test('activity pagination binds to its filter and resets the cursor when the filter changes', async () => {
  const events = Array.from({ length: 27 }, (_, i) => activityEvent('work_succeeded', randomUUID(), { feature: 'export' }, `2026-10-16T12:00:00.${String(100-i).padStart(6, '0')}Z`));
  const b = activityBrowser((path, options, state) => {
    if (!path.includes('/activity?')) return;
    const q = new URL(path, 'https://portal.example').searchParams;
    const next = !!q.get('cursor'), filter = q.get('filter');
    return jsonResponse({ ...state.page(filter, filter === 'work' ? events.slice(next ? 25 : 0, next ? 27 : 25) : []), next_cursor: filter === 'work' && !next ? 'older-work' : '' });
  });
  await b.signIn(); b.get('activity-filter').value = 'work'; await b.get('activity-filter').onchange();
  assert.equal(b.get('activity-list').children.length, 25); assert.equal(b.get('activity-more').hidden, false);
  await b.get('activity-more').onclick(); assert.equal(b.get('activity-list').children.length, 2); assert.equal(b.get('activity-more').hidden, true);
  assert.ok(b.calls.some(c => c.path.endsWith('filter=work&cursor=older-work')));
  b.get('activity-filter').value = 'billing'; await b.get('activity-filter').onchange();
  assert.ok(b.calls.at(-1).path.endsWith('filter=billing&cursor=')); assert.match(textTree(b.get('activity-list')), /No activity visible/);
});

test('activity validates microseconds before the kind and UUID tie breakers', async () => {
  const later = activityEvent('work_succeeded', '11111111-1111-4111-8111-111111111111', { feature: 'export' }, '2026-10-16T12:00:00.000009Z');
  const earlier = activityEvent('work_succeeded', 'ffffffff-ffff-4fff-8fff-ffffffffffff', { feature: 'export' }, '2026-10-16T12:00:00.000001Z');
  const b = activityBrowser((path, options, state) => path.includes('/activity?') ? jsonResponse(state.page('all', [later, earlier])) : undefined);
  await b.signIn(); assert.equal(b.get('activity-list').children.length, 2); assert.equal(b.get('activity-status').textContent, '');
});

test('activity rejects foreign scopes, private fields, missing permissions, invalid order and continuations', async () => {
  for (const change of [p => p.tenant_id = randomUUID(), p => p.account_id = randomUUID(), p => p.filter = 'billing', p => p.events = [...p.events].reverse(), p => p.events.push(p.events[0]), p => p.events[0].billing = { period: '2026-09', currency: 'EUR', amount_millicents: 1 }, p => p.events[0].review_url = '/dashboard/private', p => p.next_cursor = 'incomplete-page-cursor', p => p.events[0].occurred_at = 'not-a-time']) {
    const b = activityBrowser((path, options, state) => { if (path.includes('/activity?')) { const p = state.page(); change(p); return jsonResponse(p); } });
    await b.signIn(); assert.equal(b.get('activity-list').children.length, 0); assert.equal(b.get('activity-more').hidden, true); assert.match(b.get('activity-status').textContent, /could not be checked|did not match/);
  }
  const b = activityBrowser(); b.activityState.permissions = { view_work: true }; await b.signIn();
  assert.equal(b.get('activity-list').children.length, 0); assert.match(b.get('activity-status').textContent, /current access/);
  assert.deepEqual(b.get('activity-filter').children.map(e => e.value), ['all', 'work', 'help']);
});

test('late activity successes and failures cannot overwrite the latest selected filter', async () => {
  for (const failure of [false, true]) {
    let pending, delay = false;
    const b = activityBrowser((path, options, state) => {
      if (!delay || !path.includes('/activity?')) return; delay = false;
      return new Promise((resolve, reject) => { pending = () => failure ? reject(new Error('old network failure')) : resolve(jsonResponse(state.page())); });
    });
    await b.signIn(); delay = true; const old = b.get('activity-refresh').onclick(); await waitFor(() => pending);
    b.get('activity-filter').value = 'billing'; await b.get('activity-filter').onchange(); pending(); await old;
    assert.equal(b.get('activity-status').textContent, ''); assert.equal(b.get('activity-list').children.length, 2);
    assert.ok(!textTree(b.get('activity-list')).includes('Run completed'));
  }
});

test('old activity buttons are fenced after a filter change and after logout', async () => {
  const b = activityBrowser(); await b.signIn();
  const stale = b.get('activity-list').children[0].children.at(-1); b.get('activity-filter').value = 'billing'; await b.get('activity-filter').onchange();
  const n = b.calls.length; await stale.onclick(); assert.equal(b.calls.length, n);
  const prior = b.get('activity-list').children[0].children.at(-1); await b.get('disconnect').onclick(); const signedOut = b.calls.length; await prior.onclick();
  assert.equal(b.calls.length, signedOut); assert.equal(b.get('activity-panel').hidden, true); assert.equal(b.get('activity-list').children.length, 0); assert.equal(b.get('plan-history-focus').children.length, 0);
});

test('pending activity cannot return across sign-out or a workspace switch', async () => {
  for (const change of ['signout', 'workspace']) {
    let pending, delay = false;
    const b = activityBrowser((path, options, state) => { if (delay && path.includes('/activity?')) { delay = false; const page = state.page(); return new Promise(resolve => { pending = () => resolve(jsonResponse(page)); }); } });
    await b.signIn(); delay = true; const loading = b.get('activity-refresh').onclick(); await waitFor(() => pending);
    if (change === 'signout') await b.get('disconnect').onclick(); else { b.activityState.events = []; b.get('organization').value = randomUUID(); await b.get('organization').onchange(); }
    pending(); await loading; assert.ok(!textTree(b.get('activity-list')).includes('Run completed'));
  }
});

test('activity denial and current-role changes clear previously visible records', async () => {
  for (const change of ['denied', 'role']) {
    let denied = false;
    const b = activityBrowser(path => denied && path.includes('/activity?') ? teamResponse({ detail: 'Workspace access ended' }, 403) : undefined);
    await b.signIn();
    if (change === 'denied') { denied = true; await b.get('activity-refresh').onclick(); } else { b.activityState.permissions = { view_work: true }; await b.get('refresh').onclick(); }
    assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('activity-list').children.length, 0); assert.equal(b.get('activity-panel').hidden, true);
  }
});

test('expired run details remain a historical outcome without fabricating a result', async () => {
  const b = activityBrowser(path => { if (path.endsWith('/details')) { const d = runDetailsFixture(); d.outcome.details_available = false; d.outcome.result_available = false; return jsonResponse(d); } });
  await b.signIn(); await b.openActivity('work_succeeded');
  assert.match(textTree(b.get('run-detail-content')), /Completed.*no longer available/); assert.equal(b.get('result').hidden, true); assert.match(textTree(b.get('activity-list')), /Run completed/);
});

test('personal opt-out history is historical evidence and clears at sign-out', async () => {
  const id = randomUUID();
  const b = browser(path => path.endsWith('/email-opt-outs') ? { ok: true, status: 200, json: async () => ({ receipts: [{ id, version: 2, unsubscribed_at: new Date().toISOString() }] }) } : undefined);
  await b.signIn();
  assert.match(b.get('email-opt-outs').children[0].textContent, new RegExp(id));
  assert.match(b.get('email-opt-outs').children[0].textContent, /Current choices are checked separately/);
  assert.equal(b.get('email-work').checked, false);
  await b.get('disconnect').onclick();
  assert.equal(b.get('email-opt-outs').children.length, 0); assert.equal(b.get('email-opt-outs-more').hidden, true);
});

test('personal opt-out history follows only the returned cursor and cannot appear after sign-out', async () => {
  let release;
  const b = browser(path => {
    if (path.endsWith('/email-opt-outs')) return { ok: true, status: 200, json: async () => ({ receipts: [{ id: randomUUID(), version: 2, unsubscribed_at: new Date().toISOString() }], next_cursor: 'scoped-cursor' }) };
    if (path.endsWith('/email-opt-outs?cursor=scoped-cursor')) return new Promise(resolve => { release = () => resolve({ ok: true, status: 200, json: async () => ({ receipts: [{ id: randomUUID(), version: 4, unsubscribed_at: new Date().toISOString() }] }) }); });
  });
  await b.signIn(); assert.equal(b.get('email-opt-outs-more').hidden, false);
  const pending = b.get('email-opt-outs-more').onclick(); await waitFor(() => release);
  await b.get('disconnect').onclick(); release(); await pending;
  assert.equal(b.get('email-opt-outs').children.length, 0); assert.equal(b.get('email-opt-outs-more').hidden, true);
});

function runDetailsFixture(id = ids.op) {
  return { account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, operation_id: id, feature: 'export', plan: 'starter', period: '2026-09', admitted_at: '2026-09-15T12:00:00Z', observed_at: new Date().toISOString(), starts_consumed: 1,
    allowance: { monthly_starts: 10, starts_before: 3, starts_after: 4 }, outcome: { state: 'succeeded', resolved_at: '2026-09-15T12:01:00Z', details_available: true, result_available: true },
    charge: { state: 'provisional', currency: 'EUR', unit_price_millicents: 25000, amount_millicents: 25000, credit_millicents: 0, credit_count: 0, net_amount_millicents: 25000 } };
}
function runBrowser(custom = () => undefined) {
  const state = { operationID: ids.op, details: runDetailsFixture(), permissions: { view_work: true, view_billing: true } };
  const b = helpBrowser(async (path, options) => {
    const supplied = await custom(path, options, state); if (supplied) return supplied;
    if (path.endsWith('/product')) { const v = fixture(); v.access.permissions = { ...state.permissions }; v.operations = [{ id: state.operationID, name: 'export', state: state.details.outcome.state }]; return jsonResponse(v); }
    if (path.endsWith('/details')) return jsonResponse(structuredClone(state.details));
    if (path.includes('/billing?')) return jsonResponse({ review: { currency: 'EUR', finalized: { amount_millicents: 25000 } }, documents: { documents: [] } });
  });
  b.runState = state;
  b.openRun = async () => { await b.get('work').children.find(e => e.textContent.includes(state.operationID)).children.find(e => e.textContent === 'Run details').onclick(); await settle(); };
  b.runAction = label => b.get('run-detail-content').children.find(e => e.tag === 'button' && e.textContent === label);
  return b;
}

test('run details connect original allowance and outcome with permitted next actions', async () => {
  const b = runBrowser(); await b.signIn(); const writes = b.calls.filter(c => c.options.method === 'POST').length; await b.openRun();
  const text = textTree(b.get('run-detail-content'));
  assert.match(text, /plan starter.*One monthly start.*2026-09.*3 → 4 of 10.*historical usage/);
  assert.match(text, /Completed.*provisional charge.*EUR 0.25/);
  assert.match(text, /does not confirm payment or a cash refund/);
  await b.runAction('View result and files').onclick(); assert.equal(b.get('result').hidden, false);
  await b.runAction('Get help with this run').onclick(); assert.equal(b.get('help-run').value, ids.op);
  assert.equal(b.calls.filter(c => c.options.method === 'POST').length, writes);
  assert.equal(b.storage.size, 0);
});

test('run details preserve signed partial and full credits and link the original month', async () => {
  const b = runBrowser(); Object.assign(b.runState.details.charge, { state: 'partially_credited', statement: { id: randomUUID(), created_at: '2026-10-01T12:00:00Z' }, credit_count: 2, credit_millicents: -3000, net_amount_millicents: 22000 });
  await b.signIn(); await b.openRun();
  assert.match(textTree(b.get('run-detail-content')), /Part of.*Credits: EUR -0.03 across 2.*Net charge: EUR 0.22/);
  await b.runAction('View billing month').onclick(); assert.equal(b.get('billing-period').value, '2026-09');
  Object.assign(b.runState.details.charge, { state: 'credited', credit_count: 3, credit_millicents: -25000, net_amount_millicents: 0 });
  await b.get('run-detail-refresh').onclick(); assert.match(textTree(b.get('run-detail-content')), /fully credited.*Net charge: EUR 0.00/);
});

test('legacy and unresolved run details do not invent allowance, failure or zero charges', async () => {
  const b = runBrowser(); delete b.runState.details.allowance;
  b.runState.details.outcome = { state: 'unavailable', details_available: false, result_available: false };
  b.runState.details.charge = { state: 'unresolved', currency: 'EUR', unit_price_millicents: 25000, credit_millicents: 0, credit_count: 0 };
  await b.signIn(); await b.openRun();
  assert.match(textTree(b.get('run-detail-content')), /not retained.*Outcome unavailable.*before starting a replacement.*does not establish a final charge/);
  assert.ok(!textTree(b.get('run-detail-content')).includes('Net charge:'));
  assert.equal(b.runAction('View result and files'), undefined);
});

test('failed, cancelled and waived runs still consume one start', async () => {
  for (const state of ['failed', 'cancelled', 'waived']) {
    const b = runBrowser(); b.runState.details.outcome.state = state === 'waived' ? 'succeeded' : state; b.runState.details.outcome.result_available = false;
    Object.assign(b.runState.details.charge, { state: state === 'waived' ? 'waived' : 'not_billable', amount_millicents: 0, net_amount_millicents: 0 });
    await b.signIn(); await b.openRun();
    assert.match(textTree(b.get('run-detail-content')), /One monthly start was consumed/);
    assert.match(textTree(b.get('run-detail-content')), state === 'waived' ? /was waived/ : /No success charge/);
  }
});

test('viewer run details expose no financial values and reject an unexpected charge', async () => {
  const b = runBrowser(); b.runState.permissions = { view_work: true }; delete b.runState.details.charge;
  await b.signIn(); await b.openRun();
  assert.match(textTree(b.get('run-detail-content')), /does not include billing access/);
  assert.ok(!textTree(b.get('run-detail-content')).includes('EUR'));
  b.runState.details.charge = runDetailsFixture().charge;
  await b.get('run-detail-refresh').onclick(); assert.equal(b.get('run-detail-content').children.length, 0); assert.match(b.get('run-detail-status').textContent, /could not be verified/);
});

test('run details reject cross-scope records, owner fields and contradictory arithmetic', async () => {
  for (const mutate of [d => d.tenant_id = randomUUID(), d => d.operation_id = randomUUID(), d => d.executions = [], d => d.allowance.starts_after = 99, d => d.charge.credit_millicents = 1000, d => d.charge.net_amount_millicents = 1, d => d.charge.state = 'finalized', d => d.outcome.details_available = false]) {
    const b = runBrowser(); mutate(b.runState.details); await b.signIn(); await b.openRun();
    assert.equal(b.get('run-detail-content').children.length, 0); assert.match(b.get('run-detail-status').textContent, /could not be verified/);
  }
});

test('run details treat a missing receipt as unavailable rather than inferring an outcome', async () => {
  const b = runBrowser(path => path.endsWith('/details') ? teamResponse({ detail: 'Not found' }, 404) : undefined);
  await b.signIn(); await b.openRun(); assert.equal(b.get('run-detail-content').children.length, 0); assert.match(b.get('run-detail-status').textContent, /unavailable in this workspace/);
});

test('late run detail responses cannot overwrite a new selection or a closed panel', async () => {
  for (const closing of [false, true]) {
    let release, delay = true;
    const b = runBrowser((path, options, state) => { if (delay && path.endsWith('/details')) { delay = false; const d = structuredClone(state.details); return new Promise(resolve => { release = () => resolve(jsonResponse(d)); }); } });
    await b.signIn(); const pending = b.openRun(); await waitFor(() => release);
    if (closing) b.get('run-detail-close').onclick();
    else { b.runState.operationID = b.runState.details.operation_id = randomUUID(); await b.get('refresh').onclick(); await b.openRun(); }
    release(); await pending;
    if (closing) { assert.equal(b.get('run-detail').hidden, true); assert.equal(b.get('run-detail-content').children.length, 0); }
    else { assert.ok(textTree(b.get('run-detail-content')).includes(b.runState.details.operation_id)); assert.ok(!textTree(b.get('run-detail-content')).includes(ids.op)); }
  }
});

test('run details and old action buttons are fenced across sign-out and role changes', async () => {
  for (const change of ['signout', 'role']) {
    const b = runBrowser(); await b.signIn(); await b.openRun(); const old = b.runAction('View result and files');
    if (change === 'signout') await b.get('disconnect').onclick(); else { b.runState.permissions = { view_billing: true }; await b.get('refresh').onclick(); }
    const requests = b.calls.length; await old.onclick(); assert.equal(b.calls.length, requests); assert.equal(b.get('run-detail').hidden, true); assert.equal(b.get('run-detail-content').children.length, 0);
  }
});

test('pending run details cannot repopulate a signed-out or replaced workspace', async () => {
  for (const change of ['signout', 'workspace']) {
    let release;
    const b = runBrowser((path, options, state) => path.endsWith('/details') ? new Promise(resolve => { const d = structuredClone(state.details); release = () => resolve(jsonResponse(d)); }) : undefined);
    await b.signIn(); const pending = b.openRun(); await waitFor(() => release);
    if (change === 'signout') await b.get('disconnect').onclick(); else { b.get('organization').value = randomUUID(); await b.get('organization').onchange(); }
    release(); await pending; assert.equal(b.get('run-detail').hidden, true); assert.equal(b.get('run-detail-content').children.length, 0);
  }
});

test('run detail access denial clears the customer workspace', async () => {
  const b = runBrowser(path => path.endsWith('/details') ? teamResponse({ detail: 'Access ended' }, 403) : undefined);
  await b.signIn(); await b.openRun(); assert.equal(b.get('workspace').hidden, true); assert.equal(b.get('run-detail-content').children.length, 0);
});

test('opening another run result closes the previous explanation', async () => {
  const other = randomUUID();
  const b = runBrowser((path, options, state) => {
    if (path.endsWith('/product')) { const v = fixture(); v.access.permissions = { ...state.permissions }; v.operations = [ids.op, other].map(id => ({ id, name: 'export', state: 'succeeded' })); return jsonResponse(v); }
    if (path.endsWith(`/operations/${other}`)) return jsonResponse({ id: other, name: 'export', state: 'succeeded', result: { selected: other } });
  });
  await b.signIn(); await b.openRun();
  await b.get('work').children.find(e => e.textContent.includes(other)).children.find(e => e.textContent === 'View result and files').onclick();
  assert.equal(b.get('run-detail').hidden, true); assert.equal(b.get('run-detail-content').children.length, 0);
  assert.equal(b.get('result').hidden, false); assert.match(textTree(b.get('result')), new RegExp(other));
});

test('a delayed standalone result cannot appear beneath a newly selected run explanation', async () => {
  const other = randomUUID(); let release;
  const b = runBrowser((path, options, state) => {
    if (path.endsWith('/product')) { const v = fixture(); v.access.permissions = { ...state.permissions }; v.operations = [ids.op, other].map(id => ({ id, name: 'export', state: 'succeeded' })); return jsonResponse(v); }
    if (path.endsWith(`/operations/${ids.op}`)) return new Promise(resolve => { release = () => resolve(jsonResponse({ id: ids.op, name: 'export', state: 'succeeded', result: { stale: 'DO_NOT_SHOW' } })); });
  });
  await b.signIn();
  const pending = b.get('work').children[0].children.find(e => e.textContent === 'View result and files').onclick(); await waitFor(() => release);
  b.runState.operationID = b.runState.details.operation_id = other; await b.openRun(); release(); await pending;
  assert.equal(b.get('run-detail').hidden, false); assert.ok(textTree(b.get('run-detail-content')).includes(other));
  assert.equal(b.get('result').hidden, true); assert.ok(!textTree(b.get('result')).includes('DO_NOT_SHOW'));
});

test('old artifact buttons cannot request bytes after a new run is selected', async () => {
  const other = randomUUID();
  const b = runBrowser(); await b.signIn(); await b.openRun(); await b.runAction('View result and files').onclick();
  const old = b.get('result').children[1]; b.runState.operationID = b.runState.details.operation_id = other;
  await b.get('refresh').onclick(); await b.openRun(); const calls = b.calls.length;
  await old.onclick(); assert.equal(b.calls.length, calls); assert.equal(b.get('result').hidden, true);
});

const historyEntry = (id = ids.op, at = '2026-09-01T00:00:00.000001Z') => ({ operation_id: id, feature: 'retired-feature', plan: 'old-plan', period: '2026-09', admitted_at: at, outcome: { state: 'succeeded', resolved_at: '2026-09-02T00:00:00Z', details_available: false, result_available: false } });
const historyPage = (f = { state: 'all' }, entries = [historyEntry()], next) => ({ account_id: ids.account, app_id: ids.app, tenant_id: ids.tenant, filter: f, entries, observed_at: '2026-10-17T00:00:00Z', history_scope: 'retained_product_control_receipts', ...(next ? { next_cursor: next } : {}) });
const searchHistory = b => b.get('history-form').onsubmit({ preventDefault() {} });
const historyCalls = b => b.calls.filter(c => c.path.includes('/work-history?'));

test('work history loads explicitly and opens an expired historical run', async () => {
  const b = browser(async path => path.includes('/work-history?') ? teamResponse(historyPage()) : undefined);
  await b.signIn(); assert.equal(historyCalls(b).length, 0); assert.equal(b.get('history-section').hidden, false);
  await searchHistory(b); assert.match(textTree(b.get('history-list')), /retired-feature.*Completed/); assert.match(textTree(b.get('history-list')), /old-plan/); assert.match(textTree(b.get('history-list')), /Result details are unavailable/);
  const button = b.get('history-list').children[0].children.find(e => e.tag === 'button'); await button.onclick();
  assert.ok(b.calls.some(c => c.path.endsWith(`/operations/${ids.op}/details`))); assert.equal(b.get('run-detail').hidden, false);
  assert.equal(b.confirms.length, 0); assert.equal(historyCalls(b)[0].options.method, undefined);
});

test('work history paginates in microsecond admission order and preserves every filter', async () => {
  const entries = Array.from({ length: 25 }, (_, i) => historyEntry(`00000000-0000-4000-8000-${String(100-i).padStart(12, '0')}`, '2026-09-01T00:00:00.000002Z'));
  const filters = { state: 'succeeded', feature: 'retired-feature', from: '2026-09-01', through: '2026-09-01' };
  const b = browser(async path => { if (!path.includes('/work-history?')) return; const q = new URL(path, 'https://portal.example').searchParams; return teamResponse(historyPage(filters, q.has('cursor') ? [historyEntry(ids.op)] : entries, q.has('cursor') ? '' : 'scoped_cursor')); });
  await b.signIn(); b.get('history-state').value='succeeded'; b.get('history-feature').value='retired-feature'; b.get('history-from').value='2026-09-01'; b.get('history-through').value='2026-09-01';
  await searchHistory(b); assert.equal(b.get('history-list').children.length,25); assert.equal(b.get('history-more').hidden,false);
  await b.get('history-more').onclick(); assert.equal(b.get('history-list').children.length,1); assert.equal(b.get('history-first').hidden,false);
  const q=new URL(historyCalls(b)[1].path,'https://portal.example').searchParams; for(const [key,value] of Object.entries(filters)) assert.equal(q.get(key),value); assert.equal(q.get('cursor'),'scoped_cursor');
  await b.get('history-first').onclick(); assert.equal(b.get('history-first').hidden,true); assert.equal(new URL(historyCalls(b).at(-1).path,'https://portal.example').searchParams.has('cursor'),false);
});

test('editing work history invalidates old buttons and clears run selection', async () => {
  const b=browser(async path => path.includes('/work-history?') ? teamResponse(historyPage()) : undefined); await b.signIn(); await searchHistory(b);
  const button=b.get('history-list').children[0].children.find(e=>e.tag==='button'); await button.onclick(); const count=b.calls.length;
  b.get('history-feature').value='other'; b.get('history-feature').oninput(); assert.equal(b.get('run-detail').hidden,true); assert.equal(b.get('history-list').children.length,0);
  await button.onclick(); assert.equal(b.calls.length,count); assert.equal(b.get('history-more').hidden,true);
});

test('new work history search fences a delayed page and error', async () => {
  for(const failure of [false,true]) {
    let finish, began; const started=new Promise(r=>{began=r;}); let reads=0;
    const b=browser(async path=>{ if(!path.includes('/work-history?')) return; if(++reads===1){began(); return new Promise((resolve,reject)=>{finish=()=>failure?reject(new Error('OLD_FAILURE')):resolve(teamResponse(historyPage()));});} return teamResponse(historyPage({state:'all',feature:'empty'},[])); });
    await b.signIn(); const pending=searchHistory(b); await started; b.get('history-feature').value='empty'; b.get('history-feature').oninput(); await searchHistory(b); finish(); await pending;
    assert.match(textTree(b.get('history-list')),/No retained runs/); assert.doesNotMatch(b.get('history-status').textContent,/OLD_FAILURE/);
  }
});

test('work history rejects foreign scope, hidden fields, invented outcomes and broken pagination', async () => {
  for(const corrupt of [p=>p.tenant_id=randomUUID(),p=>p.filter={state:'failed'},p=>p.entries[0].charge={amount_millicents:100},p=>p.entries[0].feature='<script>',p=>p.entries[0].outcome.result_available=true,p=>delete p.entries[0].outcome.resolved_at,p=>p.entries.push(p.entries[0]),p=>p.next_cursor='unexpected',p=>p.entries=null]) {
    const page=historyPage(); corrupt(page); const b=browser(async path=>path.includes('/work-history?')?teamResponse(page):undefined); await b.signIn(); await searchHistory(b); assert.equal(b.get('history-list').children.length,0); assert.match(b.get('history-status').textContent,/could not be verified|did not match/);
  }
});

test('work history rejects invalid filters locally and searches exact references', async () => {
  const b=browser(async path=>path.includes('/work-history?')?teamResponse(historyPage({state:'all',operation_id:ids.op},[])):undefined); await b.signIn();
  for(const [id,value] of [['history-reference','bad'],['history-feature','../x'],['history-from','2026-02-30'],['history-state','expired']]) { b.get(id).value=value; await searchHistory(b); assert.equal(historyCalls(b).length,0); b.get(id).value=id==='history-state'?'all':''; }
  b.get('history-from').value='2026-10-02';b.get('history-through').value='2026-10-01';await searchHistory(b);assert.equal(historyCalls(b).length,0);b.get('history-from').value='';b.get('history-through').value='';
  b.get('history-reference').value=ids.op;await searchHistory(b);assert.equal(new URL(historyCalls(b)[0].path,'https://portal.example').searchParams.get('operation_id'),ids.op);assert.match(textTree(b.get('history-list')),/No retained runs/);
});

test('work history access denial clears the workspace', async () => {
  const b=browser(async path=>path.includes('/work-history?')?teamResponse({detail:'revoked'},403):undefined);await b.signIn();await searchHistory(b);assert.equal(b.get('workspace').hidden,true);assert.equal(b.get('history-section').hidden,true);assert.equal(b.get('history-list').children.length,0);
});

test('billing-only members cannot search customer work history', async () => {
  const v=fixture();v.access.permissions={view_billing:true};const b=browser(async path=>path.endsWith('/product')?teamResponse(v):undefined);await b.signIn();assert.equal(b.get('history-section').hidden,true);await searchHistory(b);assert.equal(historyCalls(b).length,0);
});

test('workspace switching and sign-out fence a pending work history response', async () => {
  for(const disconnect of [false,true]) {
    let finish,began;const started=new Promise(r=>{began=r;});const b=browser(async path=>{if(path.includes('/work-history?')){began();return new Promise(resolve=>{finish=()=>resolve(teamResponse(historyPage()));});}});await b.signIn();const pending=searchHistory(b);await started;
    if(disconnect) await b.get('disconnect').onclick();else {b.get('organization').value=randomUUID();await b.get('organization').onchange();}
    finish();await pending;assert.equal(b.get('history-list').children.length,0);assert.equal(b.get('run-detail').hidden,true);
  }
});


test('work history accepts nanosecond outcomes returned by the memory store', async () => {
  const page=historyPage();page.entries[0].outcome.resolved_at='2026-09-02T00:00:00.123456789Z';
  const b=browser(async path=>path.includes('/work-history?')?teamResponse(page):undefined);
  await b.signIn();await searchHistory(b);assert.match(textTree(b.get('history-list')),/Completed/);assert.match(b.get('history-status').textContent,/Checked/);
});
