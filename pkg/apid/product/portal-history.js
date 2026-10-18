(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const only = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).every(k => keys.includes(k));
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const name = value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,62}$/.test(value);
  const timestamp = value => {
    if (typeof value !== 'string' || !Number.isFinite(Date.parse(value))) return '';
    const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
    return match ? `${match[1]}.${(match[2] || '').padEnd(9, '0')}Z` : '';
  };
  const date = value => /^\d{4}-\d{2}-\d{2}$/.test(value) && value >= '0001-01-01' && Number.isFinite(Date.parse(`${value}T00:00:00Z`)) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
  const states = { all: 'All outcomes', accepted: 'Queued', running: 'Running', succeeded: 'Completed', failed: 'Failed', cancelled: 'Cancelled', requires_reconciliation: 'Needs review', unavailable: 'Outcome unavailable' };
  const before = (a, b) => a.at > b.at || a.at === b.at && a.id > b.id;
  globalThis.GregaleProductHistory = class {
    constructor(host) {
      this.host = host; this.request = 0; this.reset();
      const $ = host.$;
      $('history-form').onsubmit = event => { event.preventDefault(); return this.action(() => this.load()); };
      $('history-more').onclick = () => this.action(() => this.load(true));
      $('history-first').onclick = () => this.action(() => this.load());
      $('history-clear').onclick = () => { this.reset(); this.sync(); return this.action(() => this.load()); };
      for (const id of ['history-reference', 'history-feature', 'history-state', 'history-from', 'history-through']) {
        $(id).oninput = () => this.invalidate(); $(id).onchange = () => this.invalidate();
      }
    }
    invalidate() {
      this.request++; this.cursor = ''; this.boundary = undefined; this.filter = undefined;
      this.host.$('history-list').replaceChildren(); this.host.$('history-more').hidden = true; this.host.$('history-first').hidden = true;
      this.host.$('history-status').textContent = 'Search to load matching work.'; this.host.resetRun?.();
    }
    reset() {
      this.invalidate();
      for (const id of ['history-reference', 'history-feature', 'history-from', 'history-through']) this.host.$(id).value = '';
      this.host.$('history-state').value = 'all'; this.host.$('history-section').hidden = true;
    }
    sync() {
      const visible = this.host.view()?.access?.permissions?.view_work === true;
      if (!visible) this.reset();
      this.host.$('history-section').hidden = !visible;
    }
    current(generation, request) {
      this.host.current(generation);
      if (request !== this.request || !this.host.view()?.access?.permissions?.view_work) throw new Error('Work history selection or permissions changed.');
    }
    filters() {
      const $ = this.host.$, f = { state: $('history-state').value || 'all' };
      for (const [key, id] of [['operation_id', 'history-reference'], ['feature', 'history-feature'], ['from', 'history-from'], ['through', 'history-through']]) {
        const value = $(id).value.trim(); if (value) f[key] = value;
      }
      if (!Object.hasOwn(states, f.state) || f.operation_id && !uuid(f.operation_id) || f.feature && !name(f.feature) || f.from && !date(f.from) || f.through && !date(f.through) || f.from && f.through && f.from > f.through) throw new Error('Check the run reference, feature and UTC date range.');
      return f;
    }
    valid(e, f, observed) {
      const o = e?.outcome, at = timestamp(e?.admitted_at);
      if (!only(e, ['operation_id', 'feature', 'plan', 'period', 'admitted_at', 'outcome']) || !uuid(e.operation_id) || !name(e.feature) || !name(e.plan) || !/^\d{4}-(0[1-9]|1[0-2])$/.test(e.period) || !at || at > observed || !only(o, ['state', 'resolved_at', 'details_available', 'result_available']) || o.state === 'all' || !Object.hasOwn(states, o.state) || typeof o.details_available !== 'boolean' || typeof o.result_available !== 'boolean' || o.result_available && (!o.details_available || o.state !== 'succeeded')) return false;
      const settled = ['succeeded', 'failed', 'cancelled'].includes(o.state), resolved = timestamp(o.resolved_at);
      if (settled ? !resolved || resolved < at || resolved > observed : o.resolved_at !== undefined || o.details_available !== (o.state !== 'unavailable')) return false;
      const day = e.admitted_at.slice(0, 10);
      return (!f.operation_id || e.operation_id === f.operation_id) && (!f.feature || e.feature === f.feature) && (f.state === 'all' || o.state === f.state) && (!f.from || day >= f.from) && (!f.through || day <= f.through);
    }
    async action(task) {
      const generation = this.host.epoch();
      try { await task(); }
      catch (error) { if (generation === this.host.epoch()) { if (error.status === 401 || error.status === 403) this.host.denied(); else this.host.$('history-status').textContent = error.message; } }
    }
    async load(next = false) {
      if (next && !this.cursor) return;
      const generation = this.host.epoch(), request = ++this.request, $ = this.host.$;
      const oldBoundary = next ? this.boundary : undefined, cursor = next ? this.cursor : '';
      this.host.resetRun?.(); $('history-list').replaceChildren(); $('history-more').hidden = true; $('history-first').hidden = true;
      $('history-status').textContent = 'Searching retained work…';
      try {
        this.current(generation, request);
        const f = next ? this.filter : this.filters(), q = new URLSearchParams(f); if (cursor) q.set('cursor', cursor);
        const page = await this.host.api(`${this.host.base()}/work-history?${q}`); this.current(generation, request);
        const v = this.host.view(), observed = timestamp(page?.observed_at), size = this.host.pageSize(), max = this.host.cursorMax();
        if (!Number.isInteger(size) || size < 1 || !Number.isInteger(max) || max < 1 || !only(page, ['account_id', 'app_id', 'tenant_id', 'filter', 'entries', 'observed_at', 'history_scope', 'next_cursor']) || page.account_id !== v.account_id || page.app_id !== v.app_id || page.tenant_id !== v.customer.tenant_id || !only(page.filter, ['operation_id', 'feature', 'state', 'from', 'through']) || Object.keys(f).length !== Object.keys(page.filter).length || Object.entries(f).some(([k, value]) => page.filter[k] !== value) || !observed || page.history_scope !== 'retained_product_control_receipts' || !Array.isArray(page.entries) || page.entries.length > size || page.next_cursor !== undefined && (typeof page.next_cursor !== 'string' || page.next_cursor.length > max || !/^[A-Za-z0-9_-]+$/.test(page.next_cursor) || page.entries.length !== size)) throw new Error('Work history could not be verified for your current workspace and search.');
        let previous = oldBoundary;
        for (const e of page.entries) { const ordered = { at: timestamp(e.admitted_at), id: e.operation_id }; if (!this.valid(e, f, observed) || previous && !before(previous, ordered)) throw new Error('Work history did not match the requested outcome or admission order.'); previous = ordered; }
        this.filter = f; this.cursor = page.next_cursor || ''; this.boundary = previous;
        for (const e of page.entries) this.render(e, generation, request);
        if (!page.entries.length) $('history-list').append(node('li', 'No retained runs match this search in your workspace.'));
        $('history-more').hidden = !this.cursor; $('history-first').hidden = !next;
        $('history-status').textContent = `Checked ${page.observed_at}. Dates use the original start time in UTC. Search again to check for changes.`;
      } catch (error) {
        if (generation !== this.host.epoch() || request !== this.request) return;
        this.cursor = ''; this.boundary = undefined; this.filter = undefined; $('history-list').replaceChildren(); throw error;
      }
    }
    render(e, generation, request) {
      const row = node('li'); row.append(node('h4', `${e.feature} · ${states[e.outcome.state]}`), node('p', `Reference ${e.operation_id}`), node('p', `Started ${e.admitted_at} · Original plan ${e.plan} · Start month ${e.period}`));
      row.append(node('p', e.outcome.result_available ? 'Result available at this check.' : e.outcome.details_available ? 'No result available at this check.' : 'Result details are unavailable. The original start and any recorded outcome remain here.'));
      const open = node('button', 'View run details'); open.type = 'button'; open.className = 'secondary';
      open.onclick = () => { if (generation !== this.host.epoch() || request !== this.request) return; return this.action(async () => { this.current(generation, request); await this.host.openRun(e.operation_id); }); };
      row.append(open); this.host.$('history-list').append(row);
    }
  };
})();
