'use strict';
function repairVerificationMatches(plan, report) {
  if (!plan?.applied_at || !report || report.plan_id !== plan.id || report.plan_digest !== plan.digest ||
      report.source_operation_id !== plan.source_operation_id || report.repair_operation_id !== plan.repair_operation_id ||
      report.applied_at !== plan.applied_at || report.restored !== plan.candidates || report.excluded !== plan.conflicts + plan.unavailable ||
      !['account_id', 'app_id', 'environment', 'platform_tenant_id'].every(k => (report.scope?.[k] || '') === (plan.scope?.[k] || '')) ||
      typeof report.available !== 'boolean' || !Array.isArray(report.rows) || !Array.isArray(report.issues)) return false;
  if (![report.checked, report.unchanged, report.edited, report.missing].every(n => Number.isSafeInteger(n) && n >= 0 && n <= report.restored) ||
      report.checked !== report.unchanged + report.edited + report.missing || report.rows.length !== report.edited + report.missing) return false;
  return report.available ? report.checked === report.restored && report.issues.length === 0 :
    report.checked === 0 && report.issues.length === 1 && ['journal_purged', 'schema_changed'].includes(report.issues[0]);
}
function renderRepairVerification(plan, report) {
  const section = document.getElementById('verification-review'), rows = document.getElementById('verification-rows');
  section.hidden = !plan?.applied_at;
  rows.replaceChildren();
  document.getElementById('verification-table').hidden = true;
  if (!plan?.applied_at) return;
  document.getElementById('verification-receipt').textContent = `${plan.candidates} records restored at commit on ${plan.applied_at}. ${plan.conflicts + plan.unavailable} records were excluded. Receipt: ${plan.repair_operation_id}.`;
  const message = document.getElementById('verification-message');
  if (!report) {
    message.textContent = 'Observe the repaired records now. Later edits are shown separately from the original commit result.';
    return;
  }
  if (!repairVerificationMatches(plan, report)) {
    message.textContent = 'Verification response did not match this committed receipt. Refresh and try again.';
    return;
  }
  if (!report.available) {
    message.textContent = `Current verification unavailable at ${report.checked_at}: ${report.issues[0] === 'journal_purged' ? 'the repair journal was purged' : 'the table schema or enrollment changed'}. The historical receipt remains available.`;
    return;
  }
  message.textContent = `Observed at ${report.checked_at}: ${report.unchanged} unchanged since repair, ${report.edited} with later edits or changed values, ${report.missing} missing. Later activity does not change the original commit result.`;
  document.getElementById('verification-table').hidden = report.rows.length === 0;
  const reasons = new Map([
    ['record_missing', 'Repaired row is missing'],
    ['record_version_changed', 'Edited since repair'],
    ['recorded_values_changed', 'Recorded repair values differ'],
  ]);
  for (const item of report.rows) {
    const row = document.createElement('tr');
    for (const value of [`${item.key_omitted ? 'Key omitted by size limit' : item.key_json} · row ${item.row_identity}`, item.expected_version, item.observed_version || 'Missing', reasons.get(item.reason) || 'Unknown change']) {
      const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
    }
    rows.append(row);
  }
}
