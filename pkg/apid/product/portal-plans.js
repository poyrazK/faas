(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const hex = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  globalThis.GregaleProductPlans = class {
    constructor(host) {
      this.host = host; this.reset();
      const $ = host.$;
      $('plan-load').onclick = () => this.action(() => this.load());
      $('plan-review-form').onsubmit = event => { event.preventDefault(); return this.action(() => this.review()); };
      $('plan-confirm').onclick = () => this.action(() => this.confirm());
      $('plan-check').onclick = () => this.action(() => this.check());
      $('plan-history-more').onclick = () => this.action(() => this.history(true));
    }
    active() { return this.host.view()?.access?.permissions?.manage_plan === true; }
    async action(task) {
      const generation = this.host.epoch();
      try { if (!this.active()) throw new Error('Your role does not allow plan changes.'); await task(); }
      catch (error) { if (generation === this.host.epoch()) this.host.message(error.message); }
    }
    reset() {
      this.request = (this.request || 0) + 1; this.historyRequest = (this.historyRequest || 0) + 1; this.focusRequest = (this.focusRequest || 0) + 1; this.preview = undefined; this.options = undefined; this.cursor = ''; this.customer = '';
      for (const id of ['plan-options', 'plan-comparison', 'plan-history', 'plan-history-focus']) this.host.$(id).replaceChildren();
      this.host.$('plan-confirm').hidden = true; this.host.$('plan-check').hidden = true; this.host.$('plan-history-more').hidden = true;
      this.host.$('plan-status').textContent = '';
    }
    sync() {
      this.host.$('plans-section').hidden = !this.active();
      if (!this.active()) return;
      const current = JSON.stringify(this.host.view().customer);
      if (this.customer && current !== this.customer) {
        this.preview = undefined; this.options = undefined;
        this.host.$('plan-comparison').replaceChildren(); this.host.$('plan-options').replaceChildren(); this.host.$('plan-confirm').hidden = true;
        this.host.$('plan-status').textContent = 'Your current plan changed. Load the current choices before reviewing another change.';
      }
      this.customer = current;
    }
    key() { const v = this.host.view(); return `gregale.product.plan.v1:${JSON.stringify([v.account_id, v.app_id, v.customer.tenant_id])}`; }
    lock(task) { return this.host.locks.request(this.key(), task); }
    pending() {
      const raw = this.host.storage.getItem(this.key()); if (!raw) return null;
      const max = this.host.maxBytes();
      let value; try { if (!Number.isSafeInteger(max) || max < 1 || raw.length > max) throw new Error(); value = JSON.parse(raw); } catch { throw new Error('Saved plan-change recovery details are invalid. Contact your product team before replacing them.'); }
      const r = value?.request;
      if (!r || Object.keys(value).sort().join(',') !== 'expected_hash,request' || Object.keys(r).sort().join(',') !== 'id,plan,revision' || !uuid(r.id) || !/^[a-z][a-z0-9-]{0,62}$/.test(r.plan) || !hex(r.revision) || !hex(value.expected_hash)) throw new Error('Saved plan-change recovery details are invalid. Contact your product team before replacing them.');
      return value;
    }
    save(value) { this.host.storage.setItem(this.key(), JSON.stringify(value)); }
    forget(value) { if (JSON.stringify(this.pending()) === JSON.stringify(value)) this.host.storage.removeItem(this.key()); }
    async connect() {
      this.sync(); if (!this.active()) return;
      await this.load(); await this.check(); await this.history();
    }
    async load() {
      const generation = this.host.epoch(), request = ++this.request;
      const options = await this.host.api(`${this.host.base()}/plans`); this.host.current(generation);
      if (request !== this.request) return;
      this.options = options; this.preview = undefined;
      const $ = this.host.$; $('plan-options').replaceChildren(); $('plan-comparison').replaceChildren(); $('plan-confirm').hidden = true;
      for (const offer of options.offers) { const option = node('option', offer.label); option.value = offer.plan; $('plan-options').append(option); }
      $('plan-review').disabled = !options.offers.length;
      $('plan-status').textContent = options.suspended ? 'Plan changes are paused. Contact your product team.' : options.offers.length ? 'Choose a plan and review its exact prices and allowances.' : 'Your product team is not offering a change from your current plan.';
    }
    async review() {
      if (!this.options) throw new Error('Load current plan choices first.');
      const plan = this.host.$('plan-options').value;
      if (!this.options.offers.some(offer => offer.plan === plan)) throw new Error('Choose an offered plan.');
      const generation = this.host.epoch(), request = ++this.request;
      await this.lock(async () => {
        this.host.current(generation); const pending = this.pending();
        if (pending) {
          try {
            const receipt = await this.host.api(`${this.host.base()}/plans/changes/${encodeURIComponent(pending.request.id)}`); this.host.current(generation);
            this.validateReceipt(receipt, pending, true); await this.host.changed(); this.host.current(generation); this.forget(pending); this.recorded(receipt); return;
          } catch (error) { this.host.current(generation); if (error.status !== 404) throw error; }
        }
        const selection = { id: pending?.request.id || this.host.crypto.randomUUID(), plan, revision: this.options.head_revision };
        const preview = await this.host.api(`${this.host.base()}/plans/preview`, { method: 'POST', body: JSON.stringify(selection) }); this.host.current(generation);
        if (request !== this.request) return;
        if (preview.request.id !== selection.id || preview.request.plan !== selection.plan || preview.request.revision !== selection.revision || !hex(preview.hash)) throw new Error('Plan review did not match your selection.');
        this.preview = { value: preview, prior: JSON.stringify(pending) }; this.comparison(preview);
        this.host.$('plan-confirm').hidden = false; this.host.$('plan-confirm').disabled = false;
        this.host.$('plan-status').textContent = 'Review these terms, then confirm. Accepted work keeps its original price. Consumed starts are not reset.';
      });
    }
    comparison(preview) {
      const container = this.host.$('plan-comparison'); container.replaceChildren();
      container.append(node('p', `${preview.before.label} → ${preview.after.label} · ${preview.period} UTC`));
      const table = node('table'), head = node('tr');
      for (const label of ['Feature', 'Current plan', 'Proposed plan']) head.append(node('th', label)); table.append(head);
      const before = new Map(preview.before.features.map(feature => [feature.name, feature]));
      const after = new Map(preview.after.features.map(feature => [feature.name, feature]));
      const describe = (feature, currency) => feature ? `${this.host.amount(feature.unit_price_millicents, currency)} per successful run · ${feature.monthly_starts} starts/month · ${feature.starts} used · ${feature.remaining_starts} remaining` : 'Not included';
      for (const name of [...new Set([...before.keys(), ...after.keys()])].sort()) {
        const row = node('tr'); row.append(node('th', (after.get(name) || before.get(name)).label), node('td', describe(before.get(name), preview.before.currency)), node('td', describe(after.get(name), preview.after.currency))); table.append(row);
      }
      container.append(table, node('p', 'The change applies immediately to future starts. Existing runs retain their agreed prices. A lower allowance can leave no starts remaining until the next UTC month.'));
    }
    async confirm() {
      const generation = this.host.epoch(), selected = this.preview;
      if (!selected) throw new Error('Review a plan change first.');
      if (!this.host.confirm(`Change from ${selected.value.before.label} to ${selected.value.after.label}? The reviewed prices and allowances apply to future starts immediately. Consumed starts are not reset.`)) return;
      this.host.$('plan-confirm').disabled = true;
      try {
        await this.lock(async () => {
          this.host.current(generation);
          if (JSON.stringify(this.pending()) !== selected.prior) throw new Error('Plan recovery details changed in another tab. Check the previous change before continuing.');
          const value = { request: selected.value.request, expected_hash: selected.value.hash };
          this.save(value); this.host.$('plan-check').hidden = false;
          try {
            const receipt = await this.host.api(`${this.host.base()}/plans/confirm`, { method: 'POST', body: JSON.stringify(value) }); this.host.current(generation);
            this.validateReceipt(receipt, value); await this.host.changed(); this.host.current(generation); this.forget(value); this.recorded(receipt);
          } catch (error) {
            this.host.current(generation); this.preview = undefined; this.host.$('plan-confirm').hidden = true;
            throw new Error(`${error.message} Your confirmation reference is saved. Check its status, then review again if it was not recorded. Work has not been started or retried.`);
          }
        });
        await this.history();
      } finally { if (generation === this.host.epoch()) this.host.$('plan-confirm').disabled = false; }
    }
    validateReceipt(receipt, saved, referenceOnly = false) {
      const actual = receipt?.preview;
      // A prior confirmation under this reference can win a race with a fresh
      // review. Lookup reports its actual terms; it never claims new terms won.
      if (receipt?.id !== saved.request.id || actual?.request?.id !== saved.request.id || !hex(actual?.hash) || !referenceOnly && (JSON.stringify(actual?.request) !== JSON.stringify(saved.request) || actual?.hash !== saved.expected_hash)) throw new Error('The plan-change receipt did not match the saved confirmation. Keep its reference and contact your product team.');
    }
    recorded(receipt) {
      if (!uuid(receipt.id) || !receipt.preview) throw new Error('The plan-change receipt is unavailable. Check the saved reference.');
      this.preview = undefined; this.host.$('plan-confirm').hidden = true; this.host.$('plan-check').hidden = true;
      this.host.$('plan-status').textContent = `Recorded ${receipt.preview.before.label} → ${receipt.preview.after.label} at ${receipt.effective_at}. Reference ${receipt.id}.`;
      this.host.$('plan-comparison').replaceChildren();
    }
    async check() {
      const generation = this.host.epoch();
      await this.lock(async () => {
        this.host.current(generation); const saved = this.pending(); this.host.$('plan-check').hidden = !saved;
        if (!saved) return;
        try {
          const receipt = await this.host.api(`${this.host.base()}/plans/changes/${encodeURIComponent(saved.request.id)}`); this.host.current(generation);
          this.validateReceipt(receipt, saved, true); await this.host.changed(); this.host.current(generation); this.forget(saved); this.recorded(receipt);
        } catch (error) {
          this.host.current(generation);
          if (error.status !== 404) throw error;
          this.host.$('plan-status').textContent = `Confirmation ${saved.request.id} is not recorded. Load choices and review again to retry with the same reference. No change was resubmitted.`;
        }
      });
    }
    async show(id) {
      if (!this.active() || !uuid(id)) throw new Error('Your role does not allow viewing plan changes.');
      const generation = this.host.epoch(), request = ++this.focusRequest, target = this.host.$('plan-history-focus'); target.replaceChildren();
      const receipt = await this.host.api(`${this.host.base()}/plans/changes/${encodeURIComponent(id)}`); this.host.current(generation);
      if (request !== this.focusRequest) return;
      this.validateReceipt(receipt, { request: { id } }, true);
      target.append(node('h3', 'Selected plan change'), node('p', `${receipt.preview.before.label} → ${receipt.preview.after.label} · ${receipt.effective_at} · Reference ${receipt.id}`));
      target.scrollIntoView?.({ block: 'start' });
    }
    async history(next = false) {
      const generation = this.host.epoch(), request = ++this.historyRequest;
      const page = await this.host.api(`${this.host.base()}/plans/changes?cursor=${encodeURIComponent(next ? this.cursor : '')}`); this.host.current(generation);
      if (request !== this.historyRequest) return;
      this.cursor = page.next_cursor || ''; const list = this.host.$('plan-history'); list.replaceChildren();
      for (const receipt of page.changes) list.append(node('li', `${receipt.preview.before.label} → ${receipt.preview.after.label} · ${receipt.effective_at} · Reference ${receipt.id}`));
      if (!page.changes.length) list.append(node('li', 'No self-service plan changes recorded.'));
      this.host.$('plan-history-more').hidden = !this.cursor;
    }
  };
})();
