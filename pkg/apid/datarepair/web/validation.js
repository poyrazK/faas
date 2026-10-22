'use strict';
function repairValidationBlocks(plan, report) {
  return !!report && report.plan_id === plan?.id && report.plan_digest === plan.digest && !report.matches;
}
function renderRepairValidation(plan, report) {
  const section = document.getElementById('validation-review'), rows = document.getElementById('validation-rows');
  section.hidden = !plan;
  rows.replaceChildren();
  document.getElementById('validation-table').hidden = !report || !report.rows.length;
  if (!plan) return;
  const message = document.getElementById('validation-message');
  if (!report) {
    message.textContent = 'Check the saved candidates against current records. This does not change the plan or run business checks.';
    return;
  }
  const labels = new Map([
    ['plan_expired', 'The plan expired. Prepare, rehearse and review a new plan.'],
    ['plan_applied', 'This correction already committed. Load its database receipt.'],
    ['apply_closed', 'Apply authority permanently closed'],
    ['plan_replaced', 'This plan was replaced. Load its saved replacement; the original approval cannot authorize further changes.'],
    ['payload_purged', 'Captured row values were purged; retained identities and receipts remain available.'],
    ['no_candidates', 'This saved plan has no candidates.'],
    ['schema_changed', 'The table schema or enrollment changed. The application operator must review it. Records were not checked.'],
    ['checks_changed', 'The configured check definitions changed or are unavailable. Ask the operator to review the policy before preparing a new plan.'],
    ['candidate_drift', 'Some original candidates changed. Prepare and review a new plan; this correction will not silently exclude them.'],
  ]);
  const reasons = new Map([
    ['record_missing', 'Original row is missing'],
    ['record_version_changed', 'Row version changed'],
    ['recorded_values_changed', 'Recorded field values changed'],
  ]);
  message.textContent = `${report.matches ? 'No drift observed' : 'Drift or blocking conditions found'} at ${report.checked_at}. ${report.checked} of ${report.candidates} candidates checked: ${report.unchanged} unchanged, ${report.changed} changed. ${report.excluded} originally excluded records remain excluded. ${report.issues.map(issue => labels.get(issue) || 'Unknown validation issue.').join(' ')} The saved plan and approval are unchanged. Live apply checks again; a match does not replace rehearsal or approval.`;
  for (const item of report.rows) {
    const row = document.createElement('tr');
    const record = `${item.key_omitted ? 'Key omitted by size limit' : item.key_json} · row ${item.row_identity}`;
    for (const value of [record, item.expected_version, item.observed_version || 'Missing', reasons.get(item.reason) || 'Unknown change']) {
      const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
    }
    rows.append(row);
  }
}
