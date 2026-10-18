'use strict';
function renderRepairChecks(plan) {
  const section = document.getElementById('check-review'), rows = document.getElementById('check-rows');
  section.hidden = !plan;
  rows.replaceChildren();
  if (!plan) return;
  const failure = plan.check_failure;
  const rehearsed = failure?.stage === 'rehearsal' ? failure.evidence.check_results : plan.rehearsal?.check_results;
  const applied = failure?.stage === 'apply' ? failure.evidence.check_results : plan.apply_checks;
  const declarations = plan.checks || [{name: 'Single SQL assertion (legacy)', sql_sha256: plan.checks_sha256}];
  const labels = new Map([['passed', 'Passed'], ['failed', 'Failed'], ['error', 'Could not evaluate check'], ['not_run', 'Not run after an earlier failure']]);
  const status = (report, check, legacyPassed) => {
    const result = report?.results.find(result => result.name === check.name && result.sql_sha256 === check.sql_sha256);
    return result ? labels.get(result.status) || 'Invalid result' : legacyPassed && !plan.checks ? 'Passed' : 'Not recorded';
  };
  for (const check of declarations) {
    const row = document.createElement('tr');
    for (const value of [check.name, check.sql_sha256, status(rehearsed, check, !!plan.rehearsed_at), status(applied, check, !!plan.applied_at)]) {
      const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
    }
    rows.append(row);
  }
  document.getElementById('check-message').textContent = failure ? `The ${failure.stage === 'apply' ? 'live correction' : 'rehearsal'} failed a declared check (${failure.recorded_at}). Failed reports do not authorize approval.` : plan.apply_checks ? 'Every declared check passed before the correction committed.' : plan.rehearsed_at ? 'The configured checks passed in the isolated rehearsal. Live apply runs them again.' : 'Rehearsal results will appear here. The application operator defines the business checks.';
}
