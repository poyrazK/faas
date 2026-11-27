const base = '/reports';
const stages = ['north', 'south', 'merge'];
const names = { north: 'North orders', south: 'South orders', merge: 'Merge report' };
const titles = {
  staging: 'Submitting', queued: 'Queued', running: 'In progress', delivering: 'Preparing download',
  failed: 'Failed', succeeded: 'Ready to download', rerun_unavailable: 'Rerun unavailable',
  restoring: 'Starting', blocked: 'Blocked', timed_out: 'Timed out', out_of_memory: 'Out of memory',
  cancelled: 'Cancelled', retrying: 'Retrying', contract_failed: 'Validation failed', reused: 'Reused',
  waiting: 'Waiting', deadline_exceeded: 'Deadline exceeded',
  intake_expired: 'Input expired',
};
const active = new Set(['staging', 'queued', 'running', 'delivering']);
const idPattern = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/;
const $ = id => document.getElementById(id);
const orderRows = $('order-rows');
const sessionScope = document.querySelector('meta[name="report-session"]').content;
const stepViews = new Map();
const query = new URLSearchParams(location.search);
const from = query.get('from');
let requestID = query.get('report');
let cursor = 0;
let timer;
let mutating = false;
let latestReport;
let renderedRecovery = '';

function node(tag, text, className) {
  const item = document.createElement(tag);
  if (text) item.textContent = text;
  if (className) item.className = className;
  return item;
}

function reportLink(id, parent) {
  const params = new URLSearchParams({ report: id });
  if (parent) params.set('from', parent);
  return `${base}?${params}`;
}

function pending(kind) {
  try {
    return JSON.parse(sessionStorage.getItem(storageKey(kind)));
  } catch {
    return null;
  }
}

function storageKey(kind) {
  return `gregale-report:${sessionScope}:${kind}:${requestID}`;
}

function remember(kind, value) {
  sessionStorage.setItem(storageKey(kind), JSON.stringify(value));
}

function forget(kind) {
  try { sessionStorage.removeItem(storageKey(kind)); } catch { /* optional cleanup */ }
}

function errorMessage(message = '') {
  $('report-error').textContent = message;
  $('report-error').hidden = !message;
}

function progressMessage(badge, message, state) {
  const chip = $('status-badge');
  chip.textContent = badge;
  chip.dataset.state = state ?? '';
  $('report-state').textContent = message;
}

function renderSteps() {
  $('steps').replaceChildren(...stages.map(label => {
    const { state = 'waiting', attempt } = stepViews.get(label) ?? {};
    const caption = state === 'succeeded' ? 'Succeeded' : titles[state] ?? state;
    const step = node('li');
    step.dataset.state = state;
    step.append(node('span', names[label], 'step-name'),
      node('span', `${caption}${attempt ? ` · attempt ${attempt}` : ''}`, 'step-status'));
    return step;
  }));
}

function applyProgress(pipeline) {
  if (!pipeline) return;
  if ((pipeline.gap || pipeline.terminal) && pipeline.snapshot) {
    stepViews.clear();
    for (const step of pipeline.snapshot.steps) stepViews.set(step.label, step);
  } else {
    for (const event of pipeline.events) {
      if (event.type === 'step') stepViews.set(event.step.label, event.step);
    }
  }
  cursor = pipeline.nextSequence;
  renderSteps();
}

function renderRecovery(recovery) {
  const section = $('recovery');
  let intent = pending('rerun');
  if (intent && recovery?.childRequestID && recovery.childRequestID !== intent.requestID) {
    // Another tab already reserved a child. That child is authoritative.
    forget('rerun');
    intent = null;
    errorMessage('A rerun already exists. Open it to see its progress.');
  }
  const view = JSON.stringify([recovery, intent]);
  if (view === renderedRecovery) return;
  renderedRecovery = view;
  section.hidden = !recovery;
  section.replaceChildren();
  if (!recovery) return;
  section.append(node('h3', 'Recover this report'));
  const detail = node('dl', '', 'recovery-plan');
  for (const [label, steps] of [['Will run', recovery.runSteps], ['Will reuse', recovery.reuseSteps]]) {
    detail.append(node('dt', label), node('dd', steps.map(step => names[step]).join(', ') || 'None'));
  }
  section.append(detail);
  if (recovery.childRequestID) {
    const child = node('a', 'View rerun', 'button secondary');
    child.href = reportLink(recovery.childRequestID, requestID);
    section.append(child);
  }
  if (intent) {
    const retry = node('button', 'Retry same rerun', 'secondary');
    retry.addEventListener('click', () => void sendRerun(intent, retry));
    section.append(retry);
    return;
  }
  if (recovery.childRequestID) return;
  const label = node('label', '', 'approval');
  const approval = node('input');
  approval.type = 'checkbox';
  label.append(approval, node('span', `I approve repeating ${recovery.repeatSteps.map(step => names[step]).join(', ')}.`));
  const button = node('button', 'Rerun report');
  button.disabled = recovery.repeatSteps.length > 0;
  if (recovery.repeatSteps.length) {
    section.append(label);
    approval.addEventListener('change', () => { button.disabled = !approval.checked; });
  }
  button.addEventListener('click', () => {
    if (recovery.repeatSteps.length && !approval.checked) return;
    void sendRerun({ requestID: crypto.randomUUID(), repeatSteps: recovery.repeatSteps }, button);
  });
  section.append(button);
}

function renderResult(summary) {
  const result = $('result');
  result.hidden = !summary;
  result.replaceChildren();
  if (!summary) return;
  const values = node('dl', '', 'totals');
  const revenue = new Intl.NumberFormat('en-US', { style: 'currency', currency: summary.currency });
  for (const [name, value] of [
    ['Revenue', revenue.format(summary.revenue_cents / 100)], ['Orders', summary.orders], ['Units', summary.units],
  ]) {
    const group = node('div');
    group.append(node('dt', name), node('dd', String(value)));
    values.append(group);
  }
  const download = node('a', 'Download report', 'button');
  download.href = `${base}/api/${requestID}/download`;
  download.download = 'regional-sales-report.html';
  result.append(values, download);
}

function renderReport(report) {
  latestReport = report;
  const { status } = report;
  const descriptions = {
    staging: 'Waiting for the submission to be accepted.',
    queued: 'The report is in the queue.',
    running: 'The regional sales pipeline is running.',
    delivering: 'The pipeline finished. The private report is being prepared.',
    succeeded: 'Your private report is ready.',
    failed: report.outcome && report.outcome !== 'failed' ? `Report ended: ${titles[report.outcome]}.` : 'The pipeline did not finish. Review the steps below.',
    rerun_unavailable: 'The saved branch outputs are unavailable. Start a new report.',
    intake_expired: 'The uploaded input expired. Start a new report.',
  };
  progressMessage(titles[status] ?? 'Report status', descriptions[status] ?? '', status);
  if (status !== 'staging') forget('submit');
  $('retry-request').hidden = status !== 'staging' || !pending('submit');
  renderRecovery(report.recovery);
  renderResult(report.summary);
}

async function requestJSON(path, body) {
  const response = await fetch(path, {
    signal: AbortSignal.timeout(15_000),
    ...(body && {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Report-Session': sessionScope }, body: JSON.stringify(body),
    }),
  });
  if (!response.ok) {
    const { error } = await response.json().catch(() => ({}));
    throw new Error(error ?? 'The report request could not be completed.', { cause: response.status });
  }
  return response.json();
}

async function refresh() {
  clearTimeout(timer);
  try {
    const { report, pipeline } = await requestJSON(`${base}/api/${requestID}/progress?after=${cursor}`);
    if (!pending('rerun')) errorMessage();
    applyProgress(pipeline);
    renderReport(report);
    if (active.has(report.status)) timer = setTimeout(refresh, 1300);
  } catch (error) {
    const canRetry = pending('submit');
    if (canRetry) $('retry-request').hidden = false;
    errorMessage(canRetry ? 'Submission is unconfirmed. Retry to send the same request.' : error.message);
    if (error.cause !== 401 && (error.cause !== 404 || canRetry)) timer = setTimeout(refresh, 3000);
  }
}

function showActive() {
  $('order-card').hidden = true;
  $('new-report').hidden = false;
  if (idPattern.test(from ?? '')) {
    $('parent-report').href = reportLink(from);
    $('parent-report').hidden = false;
  }
}

async function sendSubmission(intent) {
  if (mutating) return;
  mutating = true;
  $('retry-request').disabled = true;
  errorMessage();
  try {
    const report = await requestJSON(`${base}/api`, intent);
    renderReport(report);
    await refresh();
  } catch (error) {
    const unconfirmed = error.cause !== 403 && error.cause !== 401;
    errorMessage(unconfirmed ? 'Submission is unconfirmed. Retry to send the same request.' : error.message);
    $('retry-request').hidden = !unconfirmed;
    if (unconfirmed) await refresh();
  } finally {
    $('retry-request').disabled = false;
    mutating = false;
  }
}

async function sendRerun(intent, button) {
  if (mutating) return;
  mutating = true;
  button.disabled = true;
  errorMessage();
  try {
    remember('rerun', intent);
    const report = await requestJSON(`${base}/api/${requestID}/rerun`, intent);
    forget('rerun');
    location.assign(reportLink(report.requestID, requestID));
  } catch (error) {
    if (error.cause === 403 || error.cause === 401) {
      errorMessage(error.message);
      mutating = false;
      return;
    }
    errorMessage(pending('rerun') ? 'Rerun is unconfirmed. Retry to send the same approval.' : 'Enable session storage to save the approval.');
    if (pending('rerun')) await refresh();
    else if (latestReport?.recovery) renderRecovery(latestReport.recovery);
    button.disabled = false;
    mutating = false;
  }
}

function rowControls() {
  $('add-order').disabled = orderRows.rows.length >= 20;
  [...orderRows.rows].forEach((row, index) => {
    const suffix = `for order ${index + 1}`;
    for (const [name, label] of [['order_id', 'Order ID'], ['region', 'Region'], ['quantity', 'Quantity'], ['price', 'Unit price']]) {
      row.querySelector(`[name="${name}"]`).setAttribute('aria-label', `${label} ${suffix}`);
    }
    const remove = row.querySelector('button');
    remove.disabled = orderRows.rows.length <= 2;
    remove.setAttribute('aria-label', `Remove order ${index + 1}`);
  });
}

function addRow(order = {}) {
  const row = $('order-row').content.firstElementChild.cloneNode(true);
  row.querySelector('[name="order_id"]').value = order.order_id ?? '';
  row.querySelector('[name="region"]').value = order.region ?? (orderRows.rows.length % 2 ? 'south' : 'north');
  row.querySelector('[name="quantity"]').value = order.quantity ?? 1;
  if (order.unit_price_cents !== undefined) row.querySelector('[name="price"]').value = (order.unit_price_cents / 100).toFixed(2);
  row.querySelector('button').addEventListener('click', () => { row.remove(); rowControls(); });
  orderRows.append(row);
  rowControls();
  return row;
}

function readOrders() {
  const orderIDs = new Set();
  const regions = new Set();
  const orders = [...orderRows.rows].map(row => {
    const value = name => row.querySelector(`[name="${name}"]`).value.trim();
    const order_id = value('order_id');
    if (!order_id || orderIDs.has(order_id)) throw new Error('Every order needs a unique ID.');
    orderIDs.add(order_id);
    const region = value('region');
    regions.add(region);
    const [dollars, cents = ''] = value('price').split('.');
    const unit_price_cents = Number(dollars) * 100 + Number(cents.padEnd(2, '0'));
    if (unit_price_cents > 1_000_000_000) throw new Error('Unit price must be at most $10,000,000.');
    return { order_id, region, quantity: Number(value('quantity')), unit_price_cents };
  });
  if (regions.size !== 2) throw new Error('Include an order from both North and South.');
  return orders;
}

$('add-order').addEventListener('click', () => addRow().querySelector('input').focus());
$('sample').addEventListener('click', async () => {
  try {
    const { rows } = await requestJSON(`${base}/sample.json`);
    orderRows.replaceChildren();
    rows.forEach(addRow);
    errorMessage();
  } catch {
    errorMessage('The sample orders could not be loaded.');
  }
});
$('order-form').addEventListener('submit', event => {
  event.preventDefault();
  if (mutating || requestID) return;
  let orders;
  try { orders = readOrders(); } catch (error) { errorMessage(error.message); return; }
  requestID = crypto.randomUUID();
  const intent = { requestID, orders };
  try { remember('submit', intent); } catch {
    requestID = null;
    errorMessage('Enable session storage so this submission can be retried.');
    return;
  }
  history.replaceState(null, '', reportLink(requestID));
  showActive();
  $('progress-heading').focus();
  progressMessage('Submitting', 'Sending your orders.', 'staging');
  void sendSubmission(intent);
});
$('retry-request').addEventListener('click', () => {
  const intent = pending('submit');
  if (intent) void sendSubmission(intent);
});

renderSteps();
if (idPattern.test(requestID ?? '')) {
  showActive();
  progressMessage('Loading report', 'Checking the latest progress.');
  void refresh();
} else {
  requestID = null;
  addRow();
  addRow();
}
