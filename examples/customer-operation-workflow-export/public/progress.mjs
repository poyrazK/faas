const stages = ['collect', 'transform', 'finish'];
const labels = ['Collect data', 'Transform rows', 'Generate file'];

/** @typedef {import('../customer-operations.generated.js').CustomerExportOutput} CustomerExportOutput */
/** @typedef {import('@gregale/sdk-node/operations').Operation<CustomerExportOutput>} CustomerExportOperation */

/** @param {CustomerExportOperation} operation */
export function workflowProgress(operation) {
  const current = stages.indexOf(operation.progress?.stage);
  const completed = operation.state === 'succeeded' ? stages.length : Math.min(stages.length, Math.max(0, operation.progress?.completed ?? 0));
  const active = operation.state === 'running';
  return stages.map((stage, index) => ({
    stage, label: labels[index],
    state: index < completed ? 'complete' : index === current ? active ? 'running' : operation.state === 'requires_reconciliation' ? 'needs_review' : 'stopped' : 'waiting',
  }));
}

/** @param {HTMLElement} element @param {CustomerExportOperation} operation */
export function renderWorkflowProgress(element, operation) {
  element.replaceChildren(...workflowProgress(operation).map(step => {
    const row = document.createElement('li');
    row.dataset.state = step.state;
    row.textContent = `${step.label} · ${step.state.replaceAll('_', ' ')}`;
    if (step.state === 'running') row.setAttribute('aria-current', 'step');
    return row;
  }));
}
