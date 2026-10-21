(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const labels = { open: 'Open', in_progress: 'In progress', resolved: 'Resolved' };
  const reasons = { workspace_reported: 'Workspace question', run_reported: 'Run reported for review', available: 'Starts were available', near_limit: 'Allowance was near its limit', allowance_exhausted: 'Allowance was exhausted', permission_required: 'Run permission was required', access_paused: 'New runs were paused', temporarily_unavailable: 'Feature was temporarily unavailable' };
  globalThis.GregaleProductHelp = class {
    constructor(host) {
      this.host = host; this.reset(); const $ = host.$;
      $('help-kind').onchange = () => this.fields();
      $('help-form').onsubmit = event => { event.preventDefault(); return this.action(() => this.submit()); };
      $('help-refresh').onclick = () => this.action(() => this.load());
      $('help-more').onclick = () => this.action(() => this.load(true));
      $('help-check').onclick = () => this.action(() => this.check());
      $('help-open').onclick = () => this.open('workspace');
    }
    active() { return !!this.host.view() && Number.isSafeInteger(this.host.textMax()) && this.host.textMax() > 0; }
    allowed(kind) {
      const p = this.host.view()?.access?.permissions || {};
      return kind === 'workspace' && Object.values(p).some(value => value === true) || kind === 'feature' && (p.run_work || p.view_billing) || kind === 'run' && p.view_work;
    }
    reset() {
      this.listRequest = (this.listRequest || 0) + 1; this.cursor = ''; this.loaded = false; this.firstPage = true; this.loading = false; this.busy = false; this.signature = ''; this.selected = ''; this.direct = false;
      const $ = this.host.$; $('help-section').hidden = true; $('help-open').hidden = true;
      for (const id of ['help-history', 'help-detail', 'help-feature']) $(id).replaceChildren();
      for (const id of ['help-message', 'help-run', 'help-status']) $(id).value = '';
      $('help-status').textContent = ''; $('help-kind').value = 'workspace'; $('help-submit').disabled = false;
      $('help-check').hidden = true; $('help-more').hidden = true; this.fields();
    }
    fields() {
      const $ = this.host.$, kind = $('help-kind').value;
      $('help-feature-label').hidden = kind !== 'feature'; $('help-run-label').hidden = kind !== 'run'; $('help-run').required = kind === 'run';
    }
    sync() {
      const $ = this.host.$; $('help-section').hidden = !this.active(); $('help-open').hidden = !this.active(); if (!this.active()) return;
      const view = this.host.view(), selected = $('help-feature').value, names = Object.keys(view.remaining_starts || {}).sort();
      const signature = JSON.stringify([names, view.guidance?.features?.map(row => [row.feature, row.label])]);
      if (signature !== this.signature) {
        this.signature = signature; $('help-feature').replaceChildren();
        for (const name of names) { const label = view.guidance?.features?.find(row => row.feature === name)?.label || view.catalog.spec.features[name]?.label || name; const option = node('option', label); option.value = name; $('help-feature').append(option); }
        $('help-feature').value = names.includes(selected) ? selected : names[0] || '';
      }
      for (const option of $('help-kind').children) option.disabled = !this.allowed(option.value);
      if (!this.allowed($('help-kind').value)) $('help-kind').value = 'workspace';
      $('help-message').maxLength = this.host.textMax(); this.fields();
    }
    open(kind, reference = '') {
      if (!this.active() || !this.allowed(kind)) return;
      this.sync(); const $ = this.host.$; $('help-kind').value = kind;
      if (kind === 'feature') $('help-feature').value = reference;
      if (kind === 'run') $('help-run').value = reference;
      this.fields(); $('help-section').scrollIntoView?.({ block: 'start', behavior: 'smooth' }); $('help-message').focus?.();
    }
    async action(task) {
      const generation = this.host.epoch();
      try { if (!this.active()) return; await task(); }
      catch (error) {
        if (generation !== this.host.epoch()) return;
        if (error.status === 401 || error.status === 403) { this.host.denied(); return; }
        this.host.$('help-status').textContent = error.message;
      }
    }
    key() { const v = this.host.view(); return `gregale.product.help.v1:${JSON.stringify([v.account_id, v.app_id, v.customer.tenant_id, this.host.organization()])}`; }
    pending() {
      const raw = this.host.storage.getItem(this.key()); if (!raw) return null;
      let value; try { if (raw.length > this.host.maxBytes()) throw new Error(); value = JSON.parse(raw); } catch { throw new Error('The saved help reference is invalid. Ask your product team to check your existing requests.'); }
      if (Object.keys(value || {}).sort().join(',') !== 'hash,id' || !uuid(value.id) || !/^[a-f0-9]{64}$/.test(value.hash)) throw new Error('The saved help reference is invalid. Ask your product team to check your existing requests.');
      return value;
    }
    async hash(request) {
      const bytes = new TextEncoder().encode(GregaleProduct.encodeInput(request));
      return Array.from(new Uint8Array(await this.host.crypto.subtle.digest('SHA-256', bytes)), value => value.toString(16).padStart(2, '0')).join('');
    }
    forget(saved) { if (JSON.stringify(this.pending()) === JSON.stringify(saved)) this.host.storage.removeItem(this.key()); }
    valid(record) {
      const v = this.host.view(), req = record?.request;
      return record?.account_id === v.account_id && record.app_id === v.app_id && record.tenant_id === v.customer.tenant_id && uuid(req?.id) && this.allowed(req.kind) && typeof req.message === 'string' && typeof record.response === 'string' && Object.hasOwn(labels, record.status) && Number.isSafeInteger(record.version) && record.version > 0 && Number.isFinite(new Date(record.created_at).getTime()) && Number.isFinite(new Date(record.updated_at).getTime()) && typeof record.context?.reason === 'string';
    }
    recorded(record) {
      if (!this.valid(record)) throw new Error('The help response did not match this workspace. Refresh to check your request.');
      this.selected = record.request.id;
      const container = this.host.$('help-detail'); container.replaceChildren();
      container.append(node('h3', `${labels[record.status]} · Request ${record.request.id}`), node('p', record.request.message));
      const context = record.context;
      container.append(node('p', `At submission: ${reasons[context.reason] || 'Context recorded for review'}.`));
      if (context.usage) container.append(node('p', `${context.usage.label} · ${context.usage.starts} of ${context.usage.limit} starts used · ${context.usage.remaining} remaining at submission.`));
      if (record.request.operation_id) container.append(node('p', `Run reference: ${record.request.operation_id}`));
      if (record.response) container.append(node('h4', 'Latest response from your product team'), node('p', record.response));
      container.append(node('p', `Updated ${new Date(record.updated_at).toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC')}.`));
      this.host.$('help-status').textContent = 'Your product team can review this request. Request status is separate from your run’s outcome.';
    }
    async show(id) {
      if (!uuid(id)) throw new Error('Choose a valid help reference.');
      const generation = this.host.epoch(), request = ++this.listRequest; this.loading = true;
      try {
        const record = await this.host.api(`${this.host.base()}/help/${id}`); this.host.current(generation);
        if (request !== this.listRequest) return;
        this.recorded(record); this.direct = true; this.host.$('help-section').scrollIntoView?.({ block: 'start' });
      } catch (error) { if (generation === this.host.epoch() && request === this.listRequest) this.host.$('help-detail').replaceChildren(); throw error; }
      finally { if (generation === this.host.epoch() && request === this.listRequest) this.loading = false; }
    }
    async load(next = false) {
      const generation = this.host.epoch(), request = ++this.listRequest;
      this.loading = true;
      try {
        const page = await this.host.api(`${this.host.base()}/help?status=all&cursor=${encodeURIComponent(next ? this.cursor : '')}`); this.host.current(generation);
        if (request !== this.listRequest) return;
        if (!Array.isArray(page.requests) || page.requests.length > this.host.pageSize() || page.requests.some(record => !this.valid(record)) || typeof (page.next_cursor || '') !== 'string') throw new Error('Help requests could not be checked. Refresh to try again.');
        const $ = this.host.$; $('help-history').replaceChildren(); this.loaded = true; this.direct = false; this.firstPage = !next; this.cursor = page.next_cursor || '';
        for (const record of page.requests) {
          const row = node('li'), button = node('button', `${labels[record.status]} · ${record.request.message.slice(0, 100)}`); button.className = 'secondary'; button.type = 'button';
          button.onclick = () => this.action(async () => { this.host.current(generation); const fresh = await this.host.api(`${this.host.base()}/help/${record.request.id}`); this.host.current(generation); this.recorded(fresh); }); row.append(button); $('help-history').append(row);
        }
        if (!page.requests.length) $('help-history').append(node('li', 'No requests visible with your current workspace permissions.'));
        if (this.selected) { const selected = page.requests.find(row => row.request.id === this.selected); if (selected) this.recorded(selected); else { this.selected = ''; $('help-detail').replaceChildren(); } }
        $('help-more').hidden = !this.cursor;
      } catch (error) {
        if (generation === this.host.epoch() && request === this.listRequest) { this.host.$('help-history').replaceChildren(); this.host.$('help-detail').replaceChildren(); this.host.$('help-more').hidden = true; }
        throw error;
      } finally { if (generation === this.host.epoch() && request === this.listRequest) this.loading = false; }
    }
    async lookup(saved, generation) {
      try {
        const record = await this.host.api(`${this.host.base()}/help/${saved.id}`); this.host.current(generation);
        if (!this.valid(record) || await this.hash(record.request) !== saved.hash) throw new Error('The saved reference does not match the recorded help request. Ask your product team to check it.');
        this.host.current(generation); this.forget(saved); this.host.$('help-check').hidden = true; this.recorded(record); return true;
      } catch (error) { this.host.current(generation); if (error.status !== 404) throw error; return false; }
    }
    async check() {
      const saved = this.pending(), generation = this.host.epoch(); this.host.$('help-check').hidden = !saved; if (!saved) return;
      if (!await this.lookup(saved, generation)) this.host.$('help-status').textContent = 'The saved request was not found with your current access. Re-enter its original details before explicitly sending it again, or ask your product team to check the reference.';
    }
    async submit() {
      if (this.busy) return;
      const $ = this.host.$, generation = this.host.epoch(); this.busy = true; $('help-submit').disabled = true;
      try {
        await this.host.locks.request(this.key(), async () => {
          this.host.current(generation); const old = this.pending();
          if (old && await this.lookup(old, generation)) { await this.load(); return; }
          const kind = $('help-kind').value, message = $('help-message').value;
          if (!this.allowed(kind) || !message.trim() || new TextEncoder().encode(message).length > this.host.textMax()) throw new Error('Choose a permitted topic and shorten your message if needed.');
          const req = { id: old?.id || this.host.crypto.randomUUID(), kind, message };
          if (kind === 'feature') { req.feature = $('help-feature').value; if (!Object.hasOwn(this.host.view().remaining_starts || {}, req.feature)) throw new Error('Choose a currently assigned feature.'); }
          if (kind === 'run') { req.operation_id = $('help-run').value.trim(); if (!uuid(req.operation_id)) throw new Error('Enter the run reference shown in your work history.'); }
          const saved = { id: req.id, hash: await this.hash(req) }; this.host.current(generation);
          if (old && old.hash !== saved.hash) throw new Error('Re-enter the original topic, reference and message to recover this saved request.');
          if (JSON.stringify(this.pending()) !== JSON.stringify(old)) throw new Error('Another tab changed the saved request. Check its reference before continuing.');
          this.host.storage.setItem(this.key(), JSON.stringify(saved)); $('help-check').hidden = false;
          let record;
          try { record = await this.host.api(`${this.host.base()}/help`, { method: 'POST', body: JSON.stringify(req) }); }
          catch (error) { this.host.current(generation); if (!old && [400, 404].includes(error.status)) { this.forget(saved); $('help-check').hidden = true; } throw error; }
          this.host.current(generation);
          if (!this.valid(record) || await this.hash(record.request) !== saved.hash) throw new Error('Your request response could not be confirmed. Check the saved reference.');
          this.host.current(generation); this.forget(saved); $('help-check').hidden = true; $('help-message').value = ''; this.recorded(record); await this.load();
        });
      } catch (error) {
        if (generation === this.host.epoch() && this.pending()) error.message = `A request reference is saved. Check it before sending another request. ${error.message}`;
        throw error;
      } finally { if (generation === this.host.epoch()) { this.busy = false; $('help-submit').disabled = false; } }
    }
    async connect() { this.sync(); await this.action(async () => { await this.load(); await this.check(); }); }
    async poll() { if (this.active() && this.loaded && !this.loading && !this.busy) { if (this.direct && this.selected) await this.action(() => this.show(this.selected)); else if (this.firstPage) await this.action(() => this.load()); } }
  };
})();
