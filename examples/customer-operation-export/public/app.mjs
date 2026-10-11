import {GregaleOperationClient} from '/sdk/operations.js';
import {ExportSession} from './session.mjs';
const element = id => document.getElementById(id);
const config = await (await fetch('/config', {cache: 'no-store'})).json();
let session;
const showError = error => { element('error').textContent = error.message; };
const run = action => async event => { event?.preventDefault(); element('error').textContent = ''; try { await action(); } catch (error) { showError(error); } };
function render(update) {
  if (update.error) element('error').textContent = update.error;
  if (update.history) {
    element('history').replaceChildren(...update.history.map(row => {
      const li = document.createElement('li'), button = document.createElement('button');
      button.textContent = `${new Date(row.created_at).toLocaleString()} · ${row.state.replaceAll('_', ' ')}`;
      button.onclick = run(() => session.open(row.id)); li.append(button); return li;
    }));
    element('more').hidden = !update.hasMore;
  }
  if (update.operation) {
    const op = update.operation; element('detail').hidden = false;
    element('business').textContent = `Export: ${op.state.replaceAll('_', ' ')}${op.cancellation_requested ? ' · cancellation requested' : ''}`;
    element('identity').textContent = op.id;
    element('progress').max = op.progress?.total || 1; element('progress').value = op.progress?.completed || 0;
    element('delivery').textContent = op.completion_delivery.state === 'not_requested' ? 'Completion notification is not configured.' : `Completion notification: ${op.completion_delivery.state.replaceAll('_', ' ')} · ${op.completion_delivery.attempts} attempts`;
    element('reconciliation').hidden = op.state !== 'requires_reconciliation';
    element('download').disabled = op.state !== 'succeeded';
    element('cancel').disabled = !['accepted', 'running'].includes(op.state) || op.cancellation_requested;
  }
}
element('signin').onsubmit = run(async () => {
  session?.close(); element('workspace').hidden = true; element('detail').hidden = true; element('history').replaceChildren();
  const token = element('token').value; element('token').value = '';
  const client = new GregaleOperationClient({apiURL: config.apiURL, credential: () => token});
  const next = new ExportSession({...config, client, onChange: update => { if (session === next) render(update); }});
  session = next;
  await session.history(); element('signin').hidden = true; element('workspace').hidden = false;
});
element('export').onsubmit = run(async () => {
  element('submit').disabled = true;
  try { await session.start(Number(element('count').value)); element('notice').textContent = 'Export accepted. You can close this page and reopen it from history.'; }
  catch (error) { element('notice').textContent = 'Check history or retry the same submission; a lost response may still mean acceptance.'; throw error; }
  finally { element('submit').disabled = false; }
});
element('refresh').onclick = run(() => session.refresh());
element('more').onclick = run(() => session.history({more: true}));
element('cancel').onclick = run(() => session.cancel());
element('download').onclick = run(async () => {
  const active = session, file = await active.download();
  if (session !== active) return;
  const url = URL.createObjectURL(file.blob), link = document.createElement('a');
  link.href = url; link.download = file.name; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
});
element('signout').onclick = () => {
  session?.close(); session = undefined;
  element('history').replaceChildren(); element('workspace').hidden = true; element('detail').hidden = true;
  element('signin').hidden = false; element('notice').textContent = ''; element('error').textContent = '';
};
window.addEventListener('pagehide', () => session?.close());
