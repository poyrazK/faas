'use strict';
class RepairSourceBrowser {
  constructor({request, select, locked, loadScopes, history}) {
    this.request = request; this.select = select; this.locked = locked;
    this.history = history;
    this.el = id => document.getElementById(id);
    this.busy = false; this.next = ''; this.filters = null; this.generation = 0;
    this.el('source-search').onsubmit = event => { event.preventDefault(); this.load(false); };
    this.el('source-next').onclick = () => this.load(true);
    this.el('source-search').oninput = () => this.reset();
    this.controls();
    if (loadScopes) this.loadScopes(loadScopes);
  }
  controls() {
    const blocked = this.busy || this.locked();
    for (const input of this.el('source-search').elements) input.disabled = blocked;
    this.el('source-next').disabled = blocked || !this.next;
    for (const button of this.el('source-results').querySelectorAll('button')) button.disabled = blocked || button.dataset.available !== 'true';
  }
  reset() {
    this.generation++; this.next = ''; this.filters = null; this.busy = false;
    this.el('source-results').replaceChildren();
    this.el('source-message').textContent = '';
    this.controls();
  }
  async loadScopes(load) {
    this.busy = true; this.controls();
    try {
      const data = await load(), select = this.el('source-scope');
      select.replaceChildren();
      for (const scope of data.scopes) {
        const option = document.createElement('option');
        option.textContent = `${scope.environment} / ${scope.platform_tenant_id || 'account'}`;
        option.value = new URLSearchParams({environment: scope.environment, customer: scope.platform_tenant_id || ''}).toString();
        select.append(option);
      }
      if (!data.scopes.length) this.el('source-message').textContent = 'No repair source scopes are configured for this app.';
    } catch (error) { this.el('source-message').textContent = error.message; }
    finally { this.busy = false; this.controls(); }
  }
  async load(nextPage) {
    if (this.busy || this.locked()) return;
    if (!nextPage) {
      this.filters = new URLSearchParams();
      for (const key of ['release', 'table', 'since', 'before']) {
        const value = this.el(`source-${key}`).value.trim();
        if (value) this.filters.set(key, value);
      }
      const scope = this.el('source-scope');
      if (scope) {
        if (!scope.value) { this.el('source-message').textContent = 'Choose a configured source scope.'; return; }
        for (const [key, value] of new URLSearchParams(scope.value)) this.filters.set(key, value);
      }
    }
    const params = new URLSearchParams(this.filters);
    if (nextPage) params.set('cursor', this.next);
    const generation = ++this.generation;
    this.busy = true; this.controls(); this.el('source-message').textContent = 'Loading captures…';
    try {
      const data = await this.request(params);
      if (generation !== this.generation) return;
      this.next = data.next_cursor || '';
      this.el('source-results').replaceChildren(...data.items.map(item => {
        const row = document.createElement('tr');
        const receipt = item.latest_repair ? `Plan ${item.latest_repair.plan_id}; repair ${item.latest_repair.operation_id}; ${item.latest_repair.applied_at}` : 'None recorded';
        const state = item.payload_purged_at ? 'Payload purged' : item.source_state === 'not_retained' ? 'Source Operation unavailable' : item.source_state === 'repair_operation' ? 'Recorded repair operation' : item.source_state === 'unsettled' ? 'Source Operation still running' : 'Payload retained';
        for (const value of [item.id, item.release, item.table, item.created_at, item.change_rows === null ? 'Not retained' : String(item.change_rows), state, `${item.applied_repairs} applied; ${receipt}`]) {
          const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
        }
        const cell = document.createElement('td'), button = document.createElement('button');
        button.type = 'button'; button.textContent = 'Prepare repair'; button.dataset.available = String(item.can_prepare);
        button.onclick = () => { if (!this.busy && !this.locked() && item.can_prepare) this.select(item.id); };
        cell.append(button);
        if (this.history) {
          const history = document.createElement('button');
          history.type = 'button'; history.textContent = 'Repair history'; history.dataset.available = 'true';
          history.onclick = () => { if (!this.busy && !this.locked()) this.history(item.id, data.scope); };
          cell.append(history);
        }
        row.append(cell); return row;
      }));
      this.el('source-message').textContent = data.items.length ? `${data.items.length} captures shown. Planning checks current records and determines candidates.` : 'No captures match these filters.';
    } catch (error) {
      if (generation === this.generation) { this.next = ''; this.el('source-message').textContent = error.message; }
    } finally {
      if (generation === this.generation) { this.busy = false; this.controls(); }
    }
  }
}
