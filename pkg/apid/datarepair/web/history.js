'use strict';
class RepairHistoryBrowser {
  constructor({request, select, openOperation, locked, scope}) {
    this.request = request; this.select = select; this.openOperation = openOperation;
    this.locked = locked; this.scope = scope;
    this.el = id => document.getElementById(id);
    this.busy = false; this.next = ''; this.filters = null; this.source = ''; this.generation = 0;
    this.el('history-search').onsubmit = event => { event.preventDefault(); this.load(false); };
    this.el('history-search').oninput = () => this.reset();
    this.el('history-next').onclick = () => this.load(true);
    this.controls();
  }
  controls() {
    const blocked = this.busy || this.locked();
    for (const input of this.el('history-search').elements) input.disabled = blocked;
    this.el('history-next').disabled = blocked || !this.next;
    for (const button of this.el('history-results').querySelectorAll('button')) button.disabled = blocked;
  }
  reset() {
    this.generation++; this.busy = false; this.next = ''; this.filters = null; this.source = '';
    this.el('history-results').replaceChildren(); this.el('history-message').textContent = '';
    this.controls();
  }
  show(source, scope) {
    this.el('history-source').value = source;
    return this.load(false, scope);
  }
  async load(nextPage, selectedScope) {
    if (this.busy || this.locked()) return;
    if (!nextPage) {
      this.source = this.el('history-source').value.trim();
      this.filters = new URLSearchParams();
      const scope = selectedScope || this.scope?.();
      if (this.scope && !scope) { this.el('history-message').textContent = 'Choose a configured environment and customer above.'; return; }
      if (scope && this.scope) {
        this.filters.set('environment', scope.environment);
        this.filters.set('customer', scope.platform_tenant_id || '');
      }
    }
    if (!this.source || !this.filters) return;
    const params = new URLSearchParams(this.filters), source = this.source;
    if (nextPage) params.set('cursor', this.next);
    const generation = ++this.generation;
    this.busy = true; this.controls(); this.next = '';
    this.el('history-results').replaceChildren();
    this.el('history-message').textContent = 'Loading repair history…';
    try {
      const page = await this.request(source, params);
      if (generation !== this.generation) return;
      if (page.source_operation_id !== source || !page.scope || !Array.isArray(page.items) ||
        (params.has('environment') && page.scope.environment !== params.get('environment')) ||
        (params.has('customer') && (page.scope.platform_tenant_id || '') !== params.get('customer'))) throw new Error('History did not match the selected source and scope.');
      this.next = page.next_cursor || '';
      this.el('history-results').replaceChildren(...page.items.map(item => this.row(item, source, page.scope, page.checked_at)));
      this.el('history-message').textContent = `${page.items.length} plans shown as of ${page.checked_at}. No committed receipt or closure means the apply outcome is unconfirmed. Loading history does not retry an apply.`;
    } catch (error) {
      if (generation === this.generation) { this.next = ''; this.el('history-results').replaceChildren(); this.el('history-message').textContent = error.message; }
    } finally {
      if (generation === this.generation) { this.busy = false; this.controls(); }
    }
  }
  row(item, source, scope, checkedAt) {
    const row = document.createElement('tr');
    const receipt = item.applied_at ? `Committed ${item.applied_at}; repair ${item.repair_operation_id}` :
      item.apply_resolution ? `Closed without commit ${item.apply_resolution.resolved_at}; repair ${item.apply_resolution.repair_operation_id}; by ${item.apply_resolution.resolved_by}` : 'No committed receipt or closure recorded';
    const lineage = [item.replacement ? `Replaces ${item.replacement.parent_plan_id}` : '', item.replacement_plan_id ? `Replaced by ${item.replacement_plan_id}` : ''].filter(Boolean).join('; ') || 'Original plan';
    const execution = !item.execution ? 'Source metadata' : item.execution.availability === 'retained' ? `${item.execution.phase} (${item.execution.state})` : 'Hosted execution history unavailable';
    const approval = item.approved_at ? `Approved ${item.approved_at}; by ${item.approved_by}` : 'No source approval recorded';
    const availability = `${item.payload_purged_at ? `Payload purged ${item.payload_purged_at}` : 'Payload retained'}; ${new Date(item.expires_at) <= new Date(checkedAt) ? 'expired' : 'expires'} ${item.expires_at}`;
    for (const value of [`${item.id}\n${item.digest}\nPrepared ${item.created_at}`, `${item.candidates} candidates; ${item.conflicts} conflicts; ${item.unavailable} unavailable`, approval, lineage, receipt, availability, execution]) {
      const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
    }
    const actions = document.createElement('td');
    const button = (label, action) => {
      const element = document.createElement('button'); element.type = 'button'; element.textContent = label;
      element.onclick = () => { if (!this.busy && !this.locked()) action(); };
      actions.append(element);
    };
    button('Load plan', () => this.select(item.id, item.digest, source, scope));
    if (item.replacement) button('Load parent', () => this.select(item.replacement.parent_plan_id, item.replacement.parent_plan_digest, source, scope));
    if (item.replacement_plan_id) button('Load replacement', () => this.select(item.replacement_plan_id, '', source, scope));
    if (this.openOperation && item.execution?.availability === 'retained') button('Open operation', () => this.openOperation(item.execution.id));
    row.append(actions); return row;
  }
}
