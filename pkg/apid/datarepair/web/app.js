const el = id => document.getElementById(id);
let plan = null;
let validation = null;
let verification = null;
let busy = false;
let requiresRefresh = false;
let sourceBrowser = null;
let historyBrowser = null;
let doctor = null;
let historyReadOnly = false;
const reasonLabels = new Map([
  ['matches_recorded_write', 'Matches the recorded write'],
  ['record_version_changed', 'Record changed since capture'],
  ['recorded_values_changed', 'Recorded field values differ'],
  ['record_missing', 'Captured row no longer exists'],
]);
const render = () => {
  sourceBrowser?.controls();
  historyBrowser?.controls();
  doctor?.controls();
  renderRepairChecks(plan);
  renderRepairValidation(plan, validation);
  renderRepairVerification(plan, verification);
  renderRepairReplacement(plan);
  renderRepairResolution(plan);
  el('summary').textContent = plan ? JSON.stringify({id:plan.id,digest:plan.digest,source_operation_id:plan.source_operation_id,source_release:plan.source_release,table:plan.table,checks_sha256:plan.checks_sha256,candidates:plan.candidates,conflicts:plan.conflicts,unavailable:plan.unavailable,expires_at:plan.expires_at,rehearsal:plan.rehearsal,approved_by:plan.approved_by,repair_operation_id:plan.repair_operation_id,apply_resolution:plan.apply_resolution},null,2) : '';
  el('rows').replaceChildren();
  const items = (plan?.items ?? []).filter(item => el('row-filter').value === 'all' || item.status === el('row-filter').value);
  el('row-count').textContent = plan && !plan.payload_purged_at ? `${items.length} of ${plan.items.length} records shown` : '';
  el('row-filter').disabled = busy || !plan || !!plan.payload_purged_at;
  for (const item of items) {
    const row = document.createElement('tr');
    row.dataset.status=item.status;
    const reason = reasonLabels.get(item.reason) || 'No detailed reason recorded';
    const observed = item.reason === 'record_missing' ? 'Captured row not found' : item.observation_omitted ? 'Observation omitted by the review size limit' : item.observed_values_json || 'Not recorded in this plan';
    const versions = `Captured: ${item.expected_version}; observed: ${item.observed_version || (item.reason === 'record_missing' ? 'missing' : 'not recorded')}`;
    for (const value of [item.key_json,item.status,item.previous_status || "—",reason,versions,item.after_json,observed,item.before_json]) {
      const cell = document.createElement('td');cell.textContent = typeof value === 'string' ? value : JSON.stringify(value);row.append(cell);
    }
    el('rows').append(row);
  }
  const usable = plan && !historyReadOnly && !plan.payload_purged_at && new Date(plan.expires_at) > new Date() && plan.candidates > 0 && !plan.applied_at && !plan.replacement_plan_id && !plan.apply_resolution && !busy && !requiresRefresh && !repairValidationBlocks(plan, validation);
  el('validate').disabled = busy || !plan || historyReadOnly;
  el('verify').disabled = busy || !plan?.applied_at;
  el('resolve').disabled = busy || historyReadOnly || requiresRefresh || !plan || !!plan.replacement_plan_id || !plan.approved_at || !el('repair-id').value;
  el('replace').disabled = busy || historyReadOnly || requiresRefresh || !plan || !!plan.payload_purged_at || !!plan.applied_at || plan.candidates < 1 || !el('replacement-id').value.trim();
  el('replacement-id').disabled = busy || historyReadOnly || !!plan?.replacement_plan_id;
  el('rehearse').disabled = !usable || !!plan?.approved_at;
  el('approve').disabled = !usable || !plan?.rehearsed_at || !!plan?.approved_at || !el('reviewed').checked;
  el('apply').disabled = !usable || !plan?.approved_at;
  for (const id of ['plan','load','token','operation','plan-id']) el(id).disabled = busy;
};
const call = async (path,body,expected) => {
  busy=true;render();el('status').textContent='Working…';
  try {
    const response = await fetch('/v1/data-repair/plans'+path,{method:body?'POST':'GET',headers:{Authorization:'Bearer '+el('token').value,'Content-Type':'application/json'},cache:'no-store',body:body?JSON.stringify(body):undefined});
    const result = await response.json();
    if (!response.ok) throw new Error(result.detail ?? 'Request failed. Reload the plan to inspect its receipt.');
    if (path.endsWith('/validate')) {
      if (result.plan_id !== plan?.id || result.plan_digest !== plan.digest || typeof result.matches !== 'boolean') throw new Error('Invalid freshness report.');
      validation=result;el('status').textContent='Freshness checked. The saved plan is unchanged.';
      return;
    }
    if (path.endsWith('/verify')) {
      if (!repairVerificationMatches(plan, result)) throw new Error('Invalid verification report.');
      verification=result;el('status').textContent='Repair observations loaded. The historical receipt is unchanged.';
      return;
    }
    if (expected && (result.id !== expected.id || result.source_operation_id !== expected.source || expected.digest && result.digest !== expected.digest)) throw new Error('Plan did not match the selected repair history.');
    const changed = plan?.id !== result.id;
    plan=result;validation=null;verification=null;requiresRefresh=false;el('plan-id').value=plan.id;el('operation').value=plan.source_operation_id;el('reviewed').checked=false;
    if (changed || !historyReadOnly && !el('repair-id').value) {
      const saved = new URLSearchParams(location.search);
      el('repair-id').value = plan.repair_operation_id || plan.apply_resolution?.repair_operation_id || (saved.get('plan') === plan.id && saved.get('repair')) || (historyReadOnly ? '' : crypto.randomUUID());
    }
    if (plan.repair_operation_id || plan.apply_resolution) el('repair-id').value=plan.repair_operation_id || plan.apply_resolution.repair_operation_id;
    if (path.endsWith('/replace')) history.replaceState(null, '', '?plan=' + encodeURIComponent(plan.id));
    el('status').textContent=historyReadOnly ? 'Read-only history view. Use the existing plan ID and Load plan to explicitly open the normal workflow. Preserve the original repair ID for any uncertain apply.' : plan.payload_purged_at ? 'Captured row values were purged under the retention policy. Plan identity and receipts remain available.' : plan.applied_at?'Correction committed.':'Plan loaded.';
  } catch(error) { requiresRefresh=!!plan;el('status').textContent=error.message+' Reload the plan before another action.'; }
  finally {busy=false;render();}
};
el('plan').onclick=()=>{historyReadOnly=false;call('',{operation_id:el('operation').value.trim()});};
el('load').onclick=()=>{historyReadOnly=false;call('/'+encodeURIComponent(el('plan-id').value.trim()));};
el('reviewed').onchange=render;
el('replacement-id').oninput=render;
el('replace').onclick=()=>{
  if (!plan) return;
  const id=el('replacement-id').value.trim();
  history.replaceState(null, '', '?plan=' + encodeURIComponent(plan.id) + '&replacement=' + encodeURIComponent(id));
  call('/'+plan.id+'/replace',{digest:plan.digest,plan_id:id});
};
el('resolve').onclick=()=>{
  if (!plan) return;
  history.replaceState(null, '', '?plan=' + encodeURIComponent(plan.id) + '&repair=' + encodeURIComponent(el('repair-id').value));
  call('/'+plan.id+'/resolve',{digest:plan.digest,repair_operation_id:el('repair-id').value});
};
el('row-filter').onchange=render;
el('token').oninput=()=>{plan=null;historyReadOnly=false;validation=null;verification=null;el('reviewed').checked=false;el('repair-id').value='';sourceBrowser.reset();historyBrowser.reset();doctor.reset();render();};
el('validate').onclick=()=>{if(plan)call('/'+plan.id+'/validate',{digest:plan.digest});};
el('verify').onclick=()=>{if(plan){verification=null;call('/'+plan.id+'/verify',{digest:plan.digest});}};
for (const action of ['rehearse','approve','apply']) el(action).onclick=()=>{
  if (!plan) return;
  const body={digest:plan.digest};if(action==='apply'){
    body.repair_operation_id=el('repair-id').value;
    history.replaceState(null, '', '?plan=' + encodeURIComponent(plan.id) + '&repair=' + encodeURIComponent(el('repair-id').value));
  }
  call('/'+plan.id+'/'+action,body);
};
setInterval(()=>{if(plan&&!busy&&new Date(plan.expires_at)<=new Date()&&!el('apply').disabled)render();},1000);
doctor = new RepairDoctor({
  locked: () => busy,
  request: async params => {
    const response = await fetch('/v1/data-repair/doctor?' + params, {method: 'GET', headers: {Authorization: 'Bearer ' + el('token').value}, cache: 'no-store'});
    const data = await response.json();
    if (!response.ok) throw new Error(data.detail || 'Cannot inspect repair configuration.');
    return data;
  },
});
historyBrowser = new RepairHistoryBrowser({
  locked: () => busy,
  select: (id, digest, source) => {
    historyReadOnly = true;
    const saved = new URLSearchParams(location.search);
    const repair = plan?.id === id ? el('repair-id').value : saved.get('plan') === id ? saved.get('repair') : '';
    el('plan-id').value = id;
    // Keep an earlier uncertain-apply URL reachable through browser history.
    history.pushState(null, '', '?plan=' + encodeURIComponent(id) + (repair ? '&repair=' + encodeURIComponent(repair) : ''));
    call('/' + encodeURIComponent(id), undefined, {id, digest, source});
  },
  request: async (source, params) => {
    const response = await fetch('/v1/data-repair/sources/' + encodeURIComponent(source) + '/history?' + params, {headers: {Authorization: 'Bearer ' + el('token').value}, cache: 'no-store'});
    const data = await response.json();
    if (!response.ok) throw new Error(data.detail || 'Cannot load repair history.');
    return data;
  },
});
sourceBrowser = new RepairSourceBrowser({
  locked: () => busy,
  history: (id, scope) => historyBrowser.show(id, scope),
  select: id => { el('operation').value = id; el('plan').click(); },
  request: async params => {
    const response = await fetch('/v1/data-repair/sources?' + params, {headers: {Authorization: 'Bearer ' + el('token').value}, cache: 'no-store'});
    const data = await response.json();
    if (!response.ok) throw new Error(data.detail || 'Cannot load recorded operations.');
    return data;
  },
});
const savedRepairPlan = new URLSearchParams(location.search).get('plan');
if (savedRepairPlan) el('plan-id').value = savedRepairPlan;
render();
