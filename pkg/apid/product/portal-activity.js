(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const name = value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,62}$/.test(value);
  const kinds = {
    work_succeeded: ['work', 'Run completed'], work_failed: ['work', 'Run failed'], work_cancelled: ['work', 'Run cancelled'], work_attention: ['work', 'Run needs your product team’s attention'],
    plan_changed: ['plans', 'Plan change confirmed'], statement_finalized: ['billing', 'Statement finalized'], credit_recorded: ['billing', 'Credit note recorded'],
    help_replied: ['help', 'Help request updated'], help_resolved: ['help', 'Help request resolved'],
  };
  const before = (a, b) => a.occurred_at === b.occurred_at ? (a.kind === b.kind ? a.reference_id > b.reference_id : a.kind > b.kind) : a.occurred_at > b.occurred_at;
  // Compare canonical UTC strings with all six microsecond digits. Date alone
  // would collapse distinct PostgreSQL timestamps within the same millisecond.
  const timestamp = value => {
    if (typeof value !== 'string') return '';
    const parts = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,6}))?Z$/.exec(value);
    return parts && Number.isFinite(Date.parse(value)) ? `${parts[1]}.${(parts[2] || '').padEnd(6, '0')}Z` : '';
  };
  globalThis.GregaleProductActivity = class {
    constructor(host) {
      this.host = host; this.reset(); const $ = host.$;
      $('activity-refresh').onclick = () => this.action(() => this.load());
      $('activity-more').onclick = () => this.action(() => this.load(true));
      $('activity-filter').onchange = () => this.action(() => this.load());
    }
    active() { return !!this.host.view() && Number.isSafeInteger(this.host.pageSize()) && this.host.pageSize() > 0 && Number.isSafeInteger(this.host.cursorMax()) && this.host.cursorMax() > 0; }
    reset() {
      this.request = (this.request || 0) + 1; this.cursor = ''; this.filter = 'all'; this.optionsKey = ''; this.boundary = null; this.loaded = false; this.loading = false; this.firstPage = true;
      const $ = this.host.$; $('activity-panel').hidden = true; $('activity-filter').value = 'all'; $('activity-filter').replaceChildren(); this.clearPage();
    }
    clearPage() { const $ = this.host.$; $('activity-list').replaceChildren(); $('activity-more').hidden = true; $('activity-status').textContent = ''; }
    sync() {
      const $ = this.host.$; $('activity-panel').hidden = !this.active(); if (!this.active()) return;
      const p = this.host.view().access?.permissions || {}, selected = $('activity-filter').value || 'all';
      const key = JSON.stringify([p.view_work === true, p.manage_plan === true, p.view_billing === true]);
      if (key === this.optionsKey) return; this.optionsKey = key;
      $('activity-filter').replaceChildren();
      for (const [value, label, visible] of [['all', 'All activity', true], ['work', 'Work', p.view_work], ['plans', 'Plan changes', p.manage_plan], ['billing', 'Statements and credits', p.view_billing], ['help', 'Help', true]]) {
        if (visible !== true) continue; const option = node('option', label); option.value = value; $('activity-filter').append(option);
      }
      $('activity-filter').value = Array.from($('activity-filter').children).some(option => option.value === selected) ? selected : 'all';
    }
    valid(e, filter) {
      const p = this.host.view()?.access?.permissions || {}, category = Object.hasOwn(kinds, e?.kind) ? kinds[e.kind][0] : '';
      if (!category || !uuid(e.reference_id) || e.id !== `${e.kind}/${e.reference_id}` || !timestamp(e.occurred_at) || filter !== 'all' && filter !== category || Object.keys(e).some(key => !['id', 'kind', 'reference_id', 'occurred_at', 'feature', 'help_kind', 'plan', 'billing'].includes(key))) return false;
      if (!!e.plan !== (category === 'plans') || !!e.billing !== (category === 'billing') || !!e.feature !== (category === 'work') || !!e.help_kind !== (category === 'help')) return false;
      if (category === 'work') return p.view_work === true && name(e.feature);
      if (category === 'plans') return p.manage_plan === true && Object.keys(e.plan).sort().join(',') === 'after,before' && name(e.plan.before) && name(e.plan.after);
      if (category === 'billing') return p.view_billing === true && Object.keys(e.billing).sort().join(',') === 'amount_millicents,currency,period' && /^\d{4}-(0[1-9]|1[0-2])$/.test(e.billing.period) && /^[A-Z]{3}$/.test(e.billing.currency) && Number.isInteger(e.billing.amount_millicents) && (e.kind === 'credit_recorded' ? e.billing.amount_millicents < 0 : e.billing.amount_millicents >= 0);
      return e.help_kind === 'workspace' && Object.values(p).some(v => v === true) || e.help_kind === 'run' && p.view_work === true || e.help_kind === 'feature' && (p.run_work === true || p.view_billing === true);
    }
    async action(task) {
      const generation = this.host.epoch();
      try { if (this.active()) await task(); }
      catch (error) { if (generation !== this.host.epoch()) return; if (error.status === 401 || error.status === 403) { this.host.denied(); return; } this.host.$('activity-status').textContent = error.message; }
    }
    async load(next = false) {
      const generation = this.host.epoch(), request = ++this.request, $ = this.host.$, filter = $('activity-filter').value, cursor = next ? this.cursor : '', boundary = next ? this.boundary : null;
      this.loading = true;
      if (!next || filter !== this.filter) this.clearPage();
      try {
        const page = await this.host.api(`${this.host.base()}/activity?filter=${encodeURIComponent(filter)}&cursor=${encodeURIComponent(cursor)}`); this.host.current(generation);
        if (request !== this.request) return;
        const v = this.host.view();
        if (page.account_id !== v.account_id || page.app_id !== v.app_id || page.tenant_id !== v.customer.tenant_id || page.filter !== filter || page.history_scope !== 'retained_product_control_records' || !timestamp(page.observed_at) || !Array.isArray(page.events) || page.events.length > this.host.pageSize() || typeof (page.next_cursor || '') !== 'string' || (page.next_cursor || '').length > this.host.cursorMax() || page.next_cursor && (page.next_cursor === cursor || page.events.length !== this.host.pageSize())) throw new Error('Activity could not be checked for this workspace. Refresh to try again.');
        let previous = boundary;
        for (const e of page.events) { const ordered = { ...e, occurred_at: timestamp(e.occurred_at) }; if (!this.valid(e, filter) || previous && !before(previous, ordered)) throw new Error('Activity did not match your current access or timeline order. Refresh to try again.'); previous = ordered; }
        this.clearPage(); this.cursor = page.next_cursor || ''; this.boundary = previous; this.filter = filter; this.loaded = true; this.firstPage = !next;
        for (const e of page.events) this.render(e, generation, request);
        if (!page.events.length) $('activity-list').append(node('li', 'No activity visible with your current workspace permissions.'));
        $('activity-more').hidden = !this.cursor;
      } catch (error) { if (generation !== this.host.epoch() || request !== this.request) return; this.clearPage(); this.cursor = ''; this.loaded = false; throw error; }
      finally { if (generation === this.host.epoch() && request === this.request) this.loading = false; }
    }
    render(e, generation, request) {
      const category = kinds[e.kind][0], row = node('li'); row.append(node('h4', kinds[e.kind][1]), node('p', `${e.occurred_at.replace('T', ' ').replace(/(?:\.\d+)?Z$/, ' UTC')} · ${e.reference_id}`));
      if (e.feature) row.append(node('p', `Feature: ${e.feature}`));
      if (e.plan) row.append(node('p', `${e.plan.before} → ${e.plan.after}`));
      if (e.billing) { row.append(node('p', `${e.billing.period} · ${this.host.amount(e.billing.amount_millicents, e.billing.currency)}`)); if (e.kind === 'credit_recorded') row.append(node('p', 'This adjustment does not confirm a refund.')); }
      const open = node('button', { work: 'View run', plans: 'View plan change', billing: 'View billing month', help: 'View help request' }[category]); open.type = 'button'; open.className = 'secondary';
      open.onclick = () => this.action(async () => {
        this.host.current(generation); if (request !== this.request || !this.valid(e, this.filter)) return;
        if (category === 'work') await this.host.openWork(e.reference_id);
        if (category === 'plans') await this.host.openPlan(e.reference_id);
        if (category === 'billing') await this.host.openBilling(e.billing.period);
        if (category === 'help') await this.host.openHelp(e.reference_id);
      });
      row.append(open); this.host.$('activity-list').append(row);
    }
    async connect() { this.sync(); if (this.active()) await this.action(() => this.load()); }
  };
})();
