'use strict';
const el = id => document.getElementById(id);
const base = location.pathname.replace(/\/review$/, '/operations');
const app = location.pathname.split('/')[3];
const listURL = `/dashboard/apps/${encodeURIComponent(app)}/customer-operations`;
el('operations').href = listURL;
const query = new URLSearchParams(location.search);
el('source').value = query.get('source') || '';
let current = null, busy = false, stale = false;
let validation = null;
let verification = null;
let sourceBrowser = null;
let historyBrowser = null;
let doctor = null;
let retainedPath = '';
let repairID = query.get('operation') || '';
const buttons = ['rehearse', 'cancel-rehearsal', 'approve', 'reject', 'apply', 'reconcile', 'resolve'];
const reasonLabels = new Map([
  ['matches_recorded_write', 'Matches the recorded write'],
  ['record_version_changed', 'Record changed since capture'],
  ['recorded_values_changed', 'Recorded field values differ'],
  ['record_missing', 'Captured row no longer exists'],
]);
async function request(path, body, headers = {}) {
  const response = await fetch(path, {method: body ? 'POST' : 'GET', credentials: 'same-origin', cache: 'no-store', headers: {...headers, 'Content-Type': 'application/json'}, body: body ? JSON.stringify(body) : undefined});
  const data = await response.json();
  if (!response.ok) throw new Error(data.detail || 'Request failed; refresh the repair before continuing.');
  return data;
}
function controls() {
  sourceBrowser?.controls();
  historyBrowser?.controls();
  doctor?.controls();
  const phase = current?.operation?.data_repair.phase;
  const blocked = busy || stale || !current || !!retainedPath;
  const payloadGone = !!current?.plan.payload_purged_at;
  const drift = repairValidationBlocks(current?.plan, validation);
  el('validate').disabled = busy || !current || !!retainedPath;
  el('verify').disabled = busy || !current?.plan.applied_at;
  const reserved = current?.operation?.data_repair.replacement_operation_id;
  el('replace').disabled = blocked || payloadGone || !!current?.plan.applied_at || !['prepared', 'awaiting_approval', 'approved', 'failed', 'rejected', 'closed', 'replacing', 'replaced'].includes(phase) || current.plan.candidates < 1 || !el('replacement-id').value.trim();
  el('replacement-id').disabled = busy || !!retainedPath || !!reserved || !!current?.plan.replacement_plan_id;
  el('rehearse').disabled = blocked || payloadGone || drift || phase !== 'prepared' || current.plan.candidates < 1;
  el('cancel-rehearsal').disabled = blocked || phase !== 'rehearsing' || !current.operation?.data_repair.rehearsal_task_id || current.operation?.data_repair.rehearsal_cancel_requested;
  for (const id of ['approve', 'reject']) el(id).disabled = blocked || payloadGone || (id === 'approve' && drift) || phase !== 'awaiting_approval' || !el('consent').checked;
  el('apply').disabled = blocked || payloadGone || drift || !!current?.plan.apply_resolution || !['approved', 'outcome_unknown'].includes(phase) || !el('consent').checked;
  el('reconcile').disabled = blocked;
  el('resolve').disabled = blocked || !['applying', 'outcome_unknown', 'resolving', 'closed', 'succeeded'].includes(phase);
  el('refresh').disabled = busy;
  el('prepare').querySelector('button').disabled = busy;
  el('row-filter').disabled = busy || !current || payloadGone;
}
function render(data, historicalPath = '') {
  current = data;
  retainedPath = historicalPath;
  validation = null;
  verification = null;
  repairID = data.operation?.id || data.plan.id;
  stale = false;
  el('consent').checked = false;
  const p = data.plan, op = data.operation;
  renderRepairChecks(p);
  renderRepairValidation(p, validation);
  renderRepairVerification(p, verification);
  renderRepairReplacement(p, op?.data_repair.replacement_operation_id);
  renderRepairResolution(p);
  el('review').hidden = false;
  el('summary').textContent = `${app} / ${p.scope.environment} · ${p.candidates} candidates · ${p.conflicts} conflicts · ${p.unavailable} unavailable · ${p.payload_purged_at ? 'Captured row values were purged under the retention policy. Plan identity and receipts remain available.' : `plan expires ${p.expires_at}`}`;
  el('identity').textContent = `Customer ${p.scope.platform_tenant_id || 'account'} · source release ${p.source_release}\nPlan ${p.id} · digest ${p.digest} · source ${p.source_operation_id}`;
  el('phase').textContent = op ? `Repair ${op.id}: ${op.data_repair.phase}${op.data_repair.rehearsal_status ? ` · rehearsal ${op.data_repair.rehearsal_status}` : ''}${op.data_repair.rehearsal_cancel_requested ? ' · cancellation requested' : ''}` :
    `Retained source plan · read-only. ${data.execution?.availability === 'retained' ? 'Hosted execution history is retained; open its operation from the history list to take actions.' : 'Hosted execution history is unavailable.'}`;
  el('status').hidden = !op; el('timeline').hidden = !op;
  if (op) { el('status').href = `${listURL}/${encodeURIComponent(op.id)}`; el('timeline').href = data.timeline_url; }
  const decision = op?.data_repair.approval;
  el('approval').textContent = decision ? `${decision.decision} by ${decision.actor.id}` : retainedPath ? 'The source approval and commit receipt remain historical evidence.' : 'An account approval is required after rehearsal.';
  renderRows();
  const locationQuery = new URLSearchParams(location.search);
  if (op) {
    const pendingReplacement = op.data_repair.replacement_operation_id || (locationQuery.get('operation') === op.id ? locationQuery.get('replacement') : '');
    history.replaceState(null, '', `?operation=${encodeURIComponent(op.id)}${pendingReplacement ? '&replacement=' + encodeURIComponent(pendingReplacement) : ''}`);
  } else {
    const params = new URLSearchParams({source: p.source_operation_id, plan: p.id, environment: p.scope.environment, customer: p.scope.platform_tenant_id || ''});
    history.replaceState(null, '', '?' + params);
  }
  controls();
}
function renderRows() {
  const p = current?.plan;
  const items = (p?.items ?? []).filter(item => el('row-filter').value === 'all' || item.status === el('row-filter').value);
  el('row-count').textContent = p && !p.payload_purged_at ? `${items.length} of ${p.items.length} records shown` : '';
  el('rows').replaceChildren(...items.map(item => {
    const tr = document.createElement('tr');
    tr.dataset.status = item.status;
    const reason = reasonLabels.get(item.reason) || 'No detailed reason recorded';
    const observed = item.reason === 'record_missing' ? 'Captured row not found' : item.observation_omitted ? 'Observation omitted by the review size limit' : item.observed_values_json || 'Not recorded in this plan';
    const versions = `Captured: ${item.expected_version}; observed: ${item.observed_version || (item.reason === 'record_missing' ? 'missing' : 'not recorded')}`;
    for (const value of [item.key_json, item.status, item.previous_status || "—", reason, versions, item.after_json, observed, item.before_json]) {
      const td = document.createElement('td'), code = document.createElement('code');
      code.textContent = value;
      td.append(code); tr.append(td);
    }
    return tr;
  }));
}
el('row-filter').onchange = renderRows;
async function run(fn) {
  busy = true; controls(); el('message').textContent = 'Working…';
  try { await fn(); el('message').textContent = 'Current repair state loaded.'; }
  catch (error) { stale = true; el('message').textContent = `${error.message} Preserve repair ID ${repairID}. Refresh and check its database receipt before resubmitting.`; }
  finally { busy = false; controls(); }
}
el('prepare').addEventListener('submit', event => {
  event.preventDefault();
  // Keep the UUID visible in the URL before the first network submission.
  if (current || !repairID) repairID = crypto.randomUUID();
  history.replaceState(null, '', `?operation=${encodeURIComponent(repairID)}`);
  run(async () => render(await request(base, {source_operation_id: el('source').value.trim()}, {'Idempotency-Key': repairID})));
});
el('refresh').onclick = () => run(async () => {
  const path = retainedPath;
  render(await request(path || `${base}/${encodeURIComponent(repairID)}`), path);
});
el('consent').onchange = controls;
el('replacement-id').oninput = controls;
el('replace').onclick = () => {
  const id = el('replacement-id').value.trim(), op = current.operation;
  history.replaceState(null, '', '?operation=' + encodeURIComponent(op.id) + '&replacement=' + encodeURIComponent(id));
  run(async () => render(await request(`${base}/${encodeURIComponent(op.id)}/replace`, {digest: current.plan.digest, expected_sequence: op.latest_sequence}, {'Idempotency-Key': id})));
};
el('validate').onclick = () => run(async () => {
  const report = await request(`${base}/${encodeURIComponent(repairID)}/validate`, {digest: current.plan.digest});
  if (report.plan_id !== current.plan.id || report.plan_digest !== current.plan.digest || typeof report.matches !== 'boolean') throw new Error('Invalid freshness report.');
  validation = report;
  renderRepairValidation(current.plan, validation);
});
el('verify').onclick = () => run(async () => {
  verification = null;
  renderRepairVerification(current.plan, verification);
  const target = retainedPath ? retainedPath.replace('?', '/verify?') : `${base}/${encodeURIComponent(repairID)}/verify`;
  const report = await request(target, {digest: current.plan.digest});
  if (!repairVerificationMatches(current.plan, report)) throw new Error('Invalid verification report.');
  verification = report;
  renderRepairVerification(current.plan, verification);
});
for (const action of buttons) el(action).onclick = () => run(async () => {
  const op = current.operation, digest = current.plan.digest;
  if (action === 'approve' || action === 'reject') {
    await request(current.approval_url, {expected_generation: op.generation, decision: action === 'approve' ? 'approved' : 'rejected'});
    render(await request(`${base}/${encodeURIComponent(repairID)}`));
  } else render(await request(`${base}/${encodeURIComponent(repairID)}/${action}`, {digest, expected_sequence: op.latest_sequence}));
});
async function openHistorical(id, digest, source, scope) {
  const params = new URLSearchParams({environment: scope.environment, customer: scope.platform_tenant_id || ''});
  const path = base.replace(/\/operations$/, '/sources/') + encodeURIComponent(source) + '/plans/' + encodeURIComponent(id) + '?' + params;
  await run(async () => {
    const data = await request(path);
    if (data.plan.id !== id || data.plan.source_operation_id !== source || digest && data.plan.digest !== digest ||
      data.plan.scope.environment !== scope.environment || (data.plan.scope.platform_tenant_id || '') !== (scope.platform_tenant_id || '')) throw new Error('Retained plan did not match the selected history.');
    render(data, path);
  });
}
historyBrowser = new RepairHistoryBrowser({
  locked: () => busy,
  select: openHistorical,
  openOperation: id => run(async () => render(await request(`${base}/${encodeURIComponent(id)}`))),
  scope: () => {
    const value = el('source-scope').value;
    if (!value) return null;
    const params = new URLSearchParams(value);
    return {environment: params.get('environment'), platform_tenant_id: params.get('customer') || ''};
  },
  request: (source, params) => request(base.replace(/\/operations$/, '/sources/') + encodeURIComponent(source) + '/history?' + params),
});
doctor = new RepairDoctor({
  locked: () => busy,
  scope: () => {
    const value = el('source-scope').value;
    if (!value) return null;
    const params = new URLSearchParams(value);
    return {environment: params.get('environment'), platform_tenant_id: params.get('customer') || ''};
  },
  request: params => request(base.replace(/\/operations$/, '/doctor') + '?' + params),
});
el('source-scope').addEventListener('change', () => { historyBrowser.reset(); doctor.reset(); });
sourceBrowser = new RepairSourceBrowser({
  locked: () => busy,
  history: (id, scope) => historyBrowser.show(id, scope),
  select: id => { el('source').value = id; el('prepare').requestSubmit(); },
  request: params => request(base.replace(/\/operations$/, '/sources') + '?' + params),
  loadScopes: () => request(base.replace(/\/operations$/, '/source-scopes')),
});
controls();
if (query.get('source') && query.get('plan') && query.has('environment') && query.has('customer')) {
  openHistorical(query.get('plan'), '', query.get('source'), {environment: query.get('environment'), platform_tenant_id: query.get('customer')});
} else if (repairID) run(async () => render(await request(`${base}/${encodeURIComponent(repairID)}`)));
