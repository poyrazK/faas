(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
  const name = value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,62}$/.test(value);
  const time = value => typeof value === 'string' && Number.isFinite(Date.parse(value));
  const only = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).every(k => keys.includes(k));
  const states = { accepted: 'Queued', running: 'Running', succeeded: 'Completed', failed: 'Failed', cancelled: 'Cancelled', requires_reconciliation: 'Needs review', unavailable: 'Outcome unavailable' };
  globalThis.GregaleProductRun = class {
    constructor(host) {
      this.host = host; this.request = 0; this.reset();
      host.$('run-detail-close').onclick = () => this.reset();
      host.$('run-detail-refresh').onclick = () => this.selected && this.show(this.selected);
    }
    reset() {
      this.request++; this.selected = ''; this.host.$('run-detail').hidden = true;
      this.host.$('run-detail-content').replaceChildren(); this.host.$('run-detail-status').textContent = '';
      this.host.$('run-detail-refresh').disabled = false; this.host.clearResult?.();
    }
    current(generation, request) {
      this.host.current(generation);
      if (request !== this.request || !this.host.view()?.access?.permissions?.view_work) throw new Error('Run selection or permissions changed.');
    }
    valid(d, id) {
      const v = this.host.view(), p = v?.access?.permissions;
      if (!p?.view_work || !only(d, ['account_id', 'app_id', 'tenant_id', 'operation_id', 'feature', 'plan', 'period', 'admitted_at', 'observed_at', 'starts_consumed', 'allowance', 'outcome', 'charge']) || d.account_id !== v.account_id || d.app_id !== v.app_id || d.tenant_id !== v.customer.tenant_id || d.operation_id !== id || !uuid(id) || !name(d.feature) || !name(d.plan) || !/^\d{4}-(0[1-9]|1[0-2])$/.test(d.period) || !time(d.admitted_at) || !time(d.observed_at) || Date.parse(d.admitted_at) > Date.parse(d.observed_at) || d.starts_consumed !== 1) return false;
      const a = d.allowance, o = d.outcome;
      if (a !== undefined && (!only(a, ['monthly_starts', 'starts_before', 'starts_after']) || !Number.isSafeInteger(a.monthly_starts) || !Number.isSafeInteger(a.starts_before) || a.monthly_starts < 1 || a.starts_before < 0 || a.starts_before >= a.monthly_starts || a.starts_after !== a.starts_before + 1)) return false;
      if (!only(o, ['state', 'resolved_at', 'details_available', 'result_available']) || !Object.hasOwn(states, o.state) || typeof o.details_available !== 'boolean' || typeof o.result_available !== 'boolean' || o.result_available && (!o.details_available || o.state !== 'succeeded')) return false;
      const settled = ['succeeded', 'failed', 'cancelled'].includes(o.state);
      if (settled ? !time(o.resolved_at) || Date.parse(o.resolved_at) < Date.parse(d.admitted_at) || Date.parse(o.resolved_at) > Date.parse(d.observed_at) : o.resolved_at !== undefined || o.details_available !== (o.state !== 'unavailable')) return false;
      if (!p.view_billing) return d.charge === undefined;
      return this.validCharge(d);
    }
    validCharge(d) {
      const c = d.charge;
      if (!only(c, ['state', 'currency', 'unit_price_millicents', 'amount_millicents', 'credit_millicents', 'credit_count', 'net_amount_millicents', 'statement']) || typeof c.currency !== 'string' || !/^[A-Z]{3}$/.test(c.currency) || !Number.isSafeInteger(c.unit_price_millicents) || c.unit_price_millicents < 0 || !Number.isSafeInteger(c.credit_millicents) || c.credit_millicents > 0 || c.credit_millicents < -c.unit_price_millicents || !Number.isSafeInteger(c.credit_count) || c.credit_count < 0 || (c.credit_count === 0) !== (c.credit_millicents === 0)) return false;
      if (c.statement !== undefined && (!only(c.statement, ['id', 'created_at']) || !uuid(c.statement.id) || !time(c.statement.created_at) || Date.parse(c.statement.created_at) < Date.parse(d.admitted_at) || Date.parse(c.statement.created_at) > Date.parse(d.observed_at))) return false;
      if (['pending', 'awaiting_review', 'unresolved'].includes(c.state)) {
        return c.amount_millicents === undefined && c.net_amount_millicents === undefined && c.statement === undefined && c.credit_count === 0 && (c.state === 'pending' ? ['accepted', 'running'].includes(d.outcome.state) : d.outcome.state === (c.state === 'awaiting_review' ? 'requires_reconciliation' : 'unavailable'));
      }
      let amount = 0;
      if (['provisional', 'finalized', 'partially_credited', 'credited'].includes(c.state)) {
        amount = c.unit_price_millicents;
        if (d.outcome.state !== 'succeeded' || (c.state === 'provisional') !== (c.statement === undefined)) return false;
      } else if (c.state !== 'waived' && (c.state !== 'not_billable' || !['failed', 'cancelled'].includes(d.outcome.state))) return false;
      const credited = ['partially_credited', 'credited'].includes(c.state);
      return Number.isSafeInteger(c.amount_millicents) && Number.isSafeInteger(c.net_amount_millicents) && c.amount_millicents === amount && c.net_amount_millicents === amount + c.credit_millicents && credited === (c.credit_count > 0) && (!credited || c.statement !== undefined && amount > 0 && (c.state === 'credited') === (c.net_amount_millicents === 0));
    }
    async show(id) {
      const generation = this.host.epoch(), request = ++this.request, $ = this.host.$;
      this.selected = id; $('run-detail-content').replaceChildren(); this.host.clearResult?.(); $('run-detail').hidden = false;
      $('run-detail-status').textContent = 'Checking the recorded plan, outcome and charge…'; $('run-detail-refresh').disabled = true;
      try {
        this.current(generation, request); if (!uuid(id)) throw new Error('Choose a valid run reference.');
        const d = await this.host.api(`${this.host.base()}/operations/${encodeURIComponent(id)}/details`); this.current(generation, request);
        if (!this.valid(d, id)) throw new Error('Run details could not be verified for your current workspace and permissions.');
        this.render(d, generation, request);
        $('run-detail-status').textContent = `Checked ${d.observed_at}. Refresh to check for later updates.`;
      } catch (error) {
        if (generation !== this.host.epoch() || request !== this.request) return;
        $('run-detail-content').replaceChildren();
        if (error.status === 401 || error.status === 403) { this.host.denied(); return; }
        $('run-detail-status').textContent = error.status === 404 ? 'Run details are unavailable in this workspace. Keep the reference and contact your product team.' : error.message;
      } finally { if (generation === this.host.epoch() && request === this.request) $('run-detail-refresh').disabled = false; }
      if (generation === this.host.epoch() && request === this.request) $('run-detail').scrollIntoView?.({ block: 'start' });
    }
    action(label, generation, request, task) {
      const button = node('button', label); button.type = 'button'; button.className = 'secondary';
      button.onclick = async () => {
        const check = () => this.current(generation, request);
        try { check(); await task(check); }
        catch (error) { if (generation === this.host.epoch() && request === this.request) { if (error.status === 401 || error.status === 403) this.host.denied(); else this.host.$('run-detail-status').textContent = error.message; } }
      };
      return button;
    }
    render(d, generation, request) {
      const content = this.host.$('run-detail-content'), a = d.allowance, o = d.outcome;
      content.append(node('p', `${d.feature} · Reference ${d.operation_id}`), node('h4', 'Plan and allowance'), node('p', `Allowed on plan ${d.plan} when started on ${d.admitted_at}. One monthly start was consumed in ${d.period}, including if this run failed or was cancelled.`));
      content.append(node('p', a ? `At admission: ${a.starts_before} → ${a.starts_after} of ${a.monthly_starts} monthly starts used. This is historical usage; your current balance may differ.` : 'The original allowance breakdown was not retained for this older run.'));
      content.append(node('h4', 'Outcome'), node('p', `${states[o.state]}${o.resolved_at ? ` · ${o.resolved_at}` : ''}`));
      if (!o.details_available) content.append(node('p', 'Results are no longer available. The original plan and any recorded outcome remain here.'));
      if (o.state === 'unavailable' || o.state === 'requires_reconciliation') content.append(node('p', 'Contact your product team before starting a replacement; the available evidence does not establish a failed run.'));
      if (d.charge) this.renderCharge(d);
      else content.append(node('h4', 'Charge'), node('p', 'Your workspace role does not include billing access.'));
      if (o.result_available) content.append(this.action('View result and files', generation, request, check => this.host.openResult(d.operation_id, check)));
      if (d.charge?.statement) content.append(this.action('View billing month', generation, request, check => this.host.openBilling(d.period, check)));
      content.append(this.action('Get help with this run', generation, request, () => this.host.openHelp(d.operation_id)));
    }
    renderCharge(d) {
      const c = d.charge, content = this.host.$('run-detail-content'), money = n => this.host.amount(n, c.currency);
      const labels = { pending: 'The charge is pending the work outcome.', awaiting_review: 'The charge is awaiting outcome review.', unresolved: 'The retained evidence does not establish a final charge.', waived: 'The success charge was waived.', not_billable: 'No success charge applies to this failed or cancelled run.', provisional: 'The successful run has a provisional charge; its billing month has not been finalized.', finalized: 'The success charge is included in a finalized statement.', partially_credited: 'Part of the finalized success charge was credited.', credited: 'The finalized success charge was fully credited.' };
      content.append(node('h4', 'Charge'), node('p', labels[c.state]), node('p', `Agreed price per successful run: ${money(c.unit_price_millicents)}.`));
      if (c.amount_millicents !== undefined) content.append(node('p', `Success charge: ${money(c.amount_millicents)} · Credits: ${money(c.credit_millicents)} across ${c.credit_count} credit ${c.credit_count === 1 ? 'note' : 'notes'} · Net charge: ${money(c.net_amount_millicents)}.`));
      if (c.statement) content.append(node('p', `Statement ${c.statement.id} · Finalized ${c.statement.created_at}.`));
      content.append(node('p', 'A statement or credit note does not confirm payment or a cash refund.'));
    }
  };
})();
