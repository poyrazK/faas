(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const labels = { work_succeeded: 'Run completed', work_failed: 'Run failed', work_cancelled: 'Run cancelled', work_attention: 'Run needs your product team’s attention', help_replied: 'Help request updated', help_resolved: 'Help request resolved' };
  globalThis.GregaleProductNotifications = class {
    constructor(host) {
      this.host = host; this.reset(); const $ = host.$;
      $('notifications-open').onclick = () => $('notifications-section').scrollIntoView?.({ block: 'start', behavior: 'smooth' });
      $('notifications-refresh').onclick = () => !this.busy && this.action(() => this.load());
      $('notifications-more').onclick = () => !this.busy && this.action(() => this.load(true));
      $('notifications-filter').onchange = () => !this.busy && this.action(() => this.load());
    }
    active() { return !!this.host.view() && Number.isSafeInteger(this.host.pageSize()) && this.host.pageSize() > 0; }
    reset() {
      this.request = (this.request || 0) + 1; this.cursor = ''; this.firstPage = true; this.loaded = false; this.loading = false; this.busy = false; this.buttons = [];
      const $ = this.host.$; $('notifications-section').hidden = true; $('notifications-open').hidden = true; $('notifications-open').textContent = 'Updates';
      $('notifications-list').replaceChildren(); $('notifications-status').textContent = ''; $('notifications-window').textContent = '';
      $('notifications-more').hidden = true; $('notifications-filter').value = 'all'; $('notifications-filter').disabled = false;
    }
    sync() { const $ = this.host.$; $('notifications-section').hidden = !this.active(); $('notifications-open').hidden = !this.active(); }
    valid(item) {
      const p = this.host.view()?.access?.permissions || {}, pieces = String(item?.id || '').split('/');
      if (pieces.length !== 2 || !uuid(pieces[1]) || item.reference_id !== pieces[1] || !Object.hasOwn(labels, item.kind) || !/^[a-f0-9]{64}$/.test(item.revision) || typeof item.read !== 'boolean' || !Number.isSafeInteger(item.read_version) || item.read_version < 0 || !Number.isFinite(new Date(item.occurred_at).getTime())) return false;
      if (pieces[0] === 'work') return item.kind.startsWith('work_') && p.view_work === true && !item.help_kind;
      return pieces[0] === 'help' && item.kind.startsWith('help_') && (item.help_kind === 'workspace' && Object.values(p).some(v => v === true) || item.help_kind === 'run' && p.view_work === true || item.help_kind === 'feature' && (p.run_work === true || p.view_billing === true));
    }
    async action(task) {
      const generation = this.host.epoch();
      try { if (this.active()) await task(); }
      catch (error) { if (generation !== this.host.epoch()) return; if (error.status === 401 || error.status === 403) { this.host.denied(); return; } this.host.$('notifications-status').textContent = error.message; }
    }
    clearPage() { const $ = this.host.$; $('notifications-list').replaceChildren(); $('notifications-more').hidden = true; $('notifications-open').textContent = 'Updates'; this.buttons = []; }
    async load(next = false) {
      const generation = this.host.epoch(), request = ++this.request, $ = this.host.$;
      this.loading = true;
      try {
        const page = await this.host.api(`${this.host.base()}/notifications?filter=${encodeURIComponent($('notifications-filter').value)}&cursor=${encodeURIComponent(next ? this.cursor : '')}`); this.host.current(generation);
        if (request !== this.request) return;
        const view = this.host.view();
        if (page.account_id !== view.account_id || page.app_id !== view.app_id || page.tenant_id !== view.customer.tenant_id || page.read_state_scope !== 'workspace' || !Array.isArray(page.items) || page.items.length > this.host.pageSize() || page.items.some(item => !this.valid(item)) || !Number.isSafeInteger(page.unread_count) || page.unread_count < 0 || !Number.isFinite(new Date(page.window_start).getTime()) || !Number.isFinite(new Date(page.observed_at).getTime()) || typeof (page.next_cursor || '') !== 'string') throw new Error('Updates could not be checked for this workspace. Refresh to try again.');
        this.clearPage(); this.loaded = true; this.firstPage = !next; this.cursor = page.next_cursor || '';
        $('notifications-open').textContent = `Updates (${page.unread_count} unread)`;
        $('notifications-window').textContent = `Updates since ${new Date(page.window_start).toISOString().slice(0, 10)}. Read status is shared within this workspace.`;
        for (const item of page.items) {
          const row = node('li'), title = node('h3', `${item.read ? 'Read' : 'Unread'} · ${labels[item.kind]}`);
          row.append(title, node('p', `${new Date(item.occurred_at).toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC')} · ${item.reference_id}`));
          if (item.feature) row.append(node('p', `Feature: ${item.feature}`));
          const open = node('button', item.id.startsWith('work/') ? 'View run' : 'View help request'); open.type = 'button'; open.className = 'secondary';
          open.onclick = () => this.action(async () => { this.host.current(generation); if (item.id.startsWith('work/')) await this.host.openWork(item.reference_id); else await this.host.openHelp(item.reference_id); });
          const mark = node('button', item.read ? 'Mark unread' : 'Mark read'); mark.type = 'button'; mark.className = 'secondary'; mark.disabled = this.busy;
          mark.onclick = () => this.action(() => this.mark(item, generation)); this.buttons.push(mark);
          row.append(open, mark); $('notifications-list').append(row);
        }
        if (!page.items.length) $('notifications-list').append(node('li', $('notifications-filter').value === 'unread' ? 'No unread updates in this window.' : 'No updates visible with your current workspace permissions.'));
        $('notifications-more').hidden = !this.cursor; $('notifications-status').textContent = '';
      } catch (error) { if (generation === this.host.epoch() && request === this.request) this.clearPage(); throw error; }
      finally { if (generation === this.host.epoch() && request === this.request) this.loading = false; }
    }
    async mark(item, generation) {
      if (this.busy) return; this.host.current(generation);
      this.busy = true; this.request++; this.loading = false; const $ = this.host.$; $('notifications-filter').disabled = true; for (const button of this.buttons) button.disabled = true;
      let failure;
      try {
        const value = await this.host.api(`${this.host.base()}/notifications/read`, { method: 'POST', body: JSON.stringify({ id: item.id, revision: item.revision, expected_version: item.read_version, read: !item.read }) }); this.host.current(generation);
        if (!this.valid(value) || value.id !== item.id || value.revision !== item.revision || value.read !== !item.read) throw new Error('Read status could not be confirmed.');
      } catch (error) { this.host.current(generation); if (error.status === 401 || error.status === 403) throw error; failure = error; }
      finally { if (generation === this.host.epoch()) { this.busy = false; $('notifications-filter').disabled = false; for (const button of this.buttons) button.disabled = false; } }
      this.host.current(generation); await this.load();
      if (failure) $('notifications-status').textContent = 'Read status was refreshed. Check the current update before choosing again.';
    }
    async connect() { this.sync(); await this.action(() => this.load()); }
    async poll() { if (this.active() && this.loaded && this.firstPage && !this.loading && !this.busy) await this.action(() => this.load()); }
  };
})();
