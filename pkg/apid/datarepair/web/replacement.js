'use strict';
let repairReplacementParent = '';
function renderRepairReplacement(plan, reservedID = '') {
  const section = document.getElementById('replacement-review');
  section.hidden = !plan;
  if (!plan) return;
  const input = document.getElementById('replacement-id');
  if (repairReplacementParent !== plan.id) {
    const query = new URLSearchParams(location.search);
    const sameParent = (query.get('plan') || query.get('operation')) === plan.id;
    input.value = reservedID || plan.replacement_plan_id || (sameParent && query.get('replacement')) || crypto.randomUUID();
    repairReplacementParent = plan.id;
  } else if (reservedID || plan.replacement_plan_id) input.value = reservedID || plan.replacement_plan_id;
  const comparison = plan.replacement;
  const history = comparison ? `Replaces plan ${comparison.parent_plan_id}, digest ${comparison.parent_plan_digest}. ${comparison.previous_candidates} previous candidates: ${comparison.retained_candidates} retained, ${comparison.removed_candidates} newly excluded. ${comparison.previous_excluded} previously excluded records remain excluded. ` : '';
  document.getElementById('replacement-summary').textContent = history + (plan.replacement_plan_id ? `The saved replacement is ${plan.replacement_plan_id}. This plan's original approval remains in history and cannot authorize further changes. Resume with the same replacement ID to load it.` : 'Preparing a replacement disables further use of this plan. The new candidate set needs its own rehearsal and approval. Previously excluded records are kept without being checked again.');
  document.getElementById('replace').textContent = reservedID || plan.replacement_plan_id ? 'Resume replacement' : 'Prepare replacement plan';
}
