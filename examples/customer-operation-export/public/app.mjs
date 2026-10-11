import {CustomerOperationFeature, createBrowserOperationReceiptStore} from './sdk/operations.js';
import {customerAuthProvider} from './customer-auth.mjs';
/** @typedef {import('../customer-operations.generated.js').CustomerExportInput} CustomerExportInput */
/** @typedef {import('../customer-operations.generated.js').CustomerExportOutput} CustomerExportOutput */
/** @param {string} id @returns {any} */
const element = id => document.getElementById(id);
const bootstrap = await fetch('/config', {cache: 'no-store'});
if (!bootstrap.ok) {
  element('signin').hidden = true;
  element('error').textContent = 'Exports are temporarily unavailable. Please try again later.';
  throw new Error('Exports unavailable');
}
const config = await bootstrap.json();
/** @type {import('@gregale/sdk-node/operations').CustomerOperationFeature<CustomerExportInput, CustomerExportOutput> | undefined} */
let feature;
let sessionEpoch = 0;
let demoCredential = '';
const showError = error => { element('error').textContent = error.message; };
const activeSession = () => {
  if (!feature?.session) throw new Error('Connect your account to load exports.');
  return feature.session;
};
const run = action => async event => {
  event?.preventDefault(); const epoch = sessionEpoch; element('error').textContent = '';
  try { await action(); } catch (error) { if (epoch === sessionEpoch) showError(error); }
};
/** @param {import('@gregale/sdk-node/operations').OperationSessionUpdate<CustomerExportOutput>} update */
function render(update) {
  if (update.error) element('error').textContent = update.error;
  if (update.history) {
    element('history').replaceChildren(...update.history.map(row => {
      const li = document.createElement('li'), button = document.createElement('button');
      button.textContent = `${new Date(row.created_at).toLocaleString()} · ${row.state.replaceAll('_', ' ')}`;
      button.onclick = run(() => activeSession().open(row.id)); li.append(button); return li;
    }));
    element('more').hidden = !update.hasMore;
  }
  if (update.operation) {
    const op = update.operation; element('detail').hidden = false;
    element('business').textContent = `Export: ${op.state.replaceAll('_', ' ')}${op.cancellation_requested ? ' · cancellation requested' : ''}`;
    element('identity').textContent = op.id;
    element('progress').max = op.progress?.total || 1; element('progress').value = op.state === 'succeeded' ? element('progress').max : op.progress?.completed || 0;
    element('delivery').textContent = op.completion_delivery.state === 'not_requested' ? 'Completion notification is not configured.' : `Completion notification: ${op.completion_delivery.state.replaceAll('_', ' ')} · ${op.completion_delivery.attempts} attempts`;
    element('reconciliation').hidden = op.state !== 'requires_reconciliation';
    element('download').disabled = op.state !== 'succeeded';
    element('cancel').disabled = !['accepted', 'running'].includes(op.state) || op.cancellation_requested;
  }
}
function clearFeatureUI(message = '') {
  ++sessionEpoch;
  element('submit').disabled = false;
  demoCredential = '';
  element('token').value = '';
  element('history').replaceChildren(); element('workspace').hidden = true; element('detail').hidden = true;
  element('business').textContent = ''; element('identity').textContent = ''; element('delivery').textContent = '';
  element('progress').value = 0; element('more').hidden = true;
  element('download').disabled = true; element('cancel').disabled = true; element('reconciliation').hidden = true;
  element('signin').hidden = false; element('notice').textContent = '';
  element('error').textContent = '';
  if (message) element('notice').textContent = message;
}
function disconnect(message = '') {
  feature?.close();
  clearFeatureUI(message);
}
feature = new CustomerOperationFeature({
  ...config,
  name: 'customer-export',
  provider: customerAuthProvider,
  fallbackCredential: () => demoCredential,
  receiptStore: createBrowserOperationReceiptStore(),
  onChange: render,
  onIdentityChange: () => clearFeatureUI('Your account changed. Connect again to load its exports.'),
});
if (feature.integrated) {
  element('token-field').hidden = true;
  element('token').disabled = true;
  element('connect').textContent = 'Connect account';
  element('auth-help').textContent = 'Connect using your application account.';
} else {
  element('auth-help').textContent = 'Development demo: enter a short-lived customer Operations token.';
  element('signout').textContent = 'Sign out';
}
const signIn = run(async () => {
  element('workspace').hidden = true; element('detail').hidden = true; element('history').replaceChildren();
  demoCredential = feature.integrated ? '' : element('token').value;
  element('token').value = '';
  const connection = await feature.connect();
  if (!connection || feature.session !== connection.session) return;
  element('signin').hidden = true; element('workspace').hidden = false;
  const restored = connection.restored;
  element('notice').textContent = restored.state === 'unresolved' ? 'An earlier export request is unresolved. Check your exports, or enter the same row count and retry.' : restored.state === 'accepted' ? 'Your earlier export request has been restored.' : '';
});
element('signin').onsubmit = event => {
  ++sessionEpoch; element('connect').disabled = true;
  return signIn(event).finally(() => { element('connect').disabled = false; });
};
element('export').onsubmit = run(async () => {
  element('submit').disabled = true;
  const active = activeSession();
  try { await active.start({count: Number(element('count').value)}); if (feature.session === active) element('notice').textContent = 'Export accepted. You can close this page and reopen it from history.'; }
  catch (error) { if (feature.session === active) element('notice').textContent = 'Check history or retry the same submission; a lost response may still mean acceptance.'; throw error; }
  finally { if (feature.session === active) element('submit').disabled = false; }
});
element('refresh').onclick = run(() => activeSession().refresh());
element('more').onclick = run(() => activeSession().history({more: true}));
element('cancel').onclick = run(() => activeSession().cancel());
element('download').onclick = run(async () => {
  const active = activeSession(), result = await active.result();
  const artifactID = result.result?.artifact_id;
  if (!artifactID || !result.artifacts?.some(file => file.id === artifactID)) throw new Error('The retained export file is unavailable');
  const file = await active.download(artifactID);
  if (feature.session !== active) return;
  const url = URL.createObjectURL(file.blob), link = document.createElement('a');
  link.href = url; link.download = file.name; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
});
element('signout').onclick = () => disconnect();
window.addEventListener('pagehide', () => feature.dispose(), {once: true});
