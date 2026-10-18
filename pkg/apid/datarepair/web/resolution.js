'use strict';
function renderRepairResolution(plan) {
  document.getElementById('resolution-review').hidden = !plan;
  if (!plan) return;
  const result = plan.apply_resolution;
  document.getElementById('resolution-summary').textContent = result
    ? `No correction committed under this plan. Apply authority was permanently closed at ${result.resolved_at}. Repair identity: ${result.repair_operation_id}. A replacement needs fresh rehearsal and approval.`
    : plan.applied_at
      ? `The correction committed at ${plan.applied_at}. The saved receipt determines the outcome.`
      : 'Resolve an unsuccessful or uncertain apply before preparing a replacement. If no correction committed, resolution permanently closes this plan. A busy or unavailable database stays unresolved.';
}
