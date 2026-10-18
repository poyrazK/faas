(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const statuses = { queued: 'Queued', captured: 'Captured locally', provider_accepted: 'Accepted by email provider', delivered: 'Provider reported delivery', bounced: 'Provider reported a bounce', complained: 'Provider reported a complaint', failed: 'Failed', cancelled: 'Cancelled' };
  globalThis.GregaleProductEmail = class {
    constructor(host) {
      this.host = host; this.reset(); const $ = host.$;
      $('email-preferences-form').onsubmit = event => { event.preventDefault(); return this.action(() => this.save()); };
      $('email-refresh').onclick = () => this.action(() => this.load());
      $('email-recover').onclick = () => this.action(() => this.recover());
      $('email-recoveries-more').onclick = () => this.action(() => this.history(this.recoveryNext));
      $('email-opt-outs-more').onclick = () => this.action(() => this.optOutHistory(this.optOutNext));
    }
    active() { return Boolean(this.host.view()); }
    reset() {
      this.request = (this.request || 0) + 1; this.version = undefined; this.busy = false; this.paused = false; this.pendingRecovery = null; this.generation = undefined; this.recoveryNext = null; this.optOutNext = null;
      const $ = this.host.$; $('email-section').hidden = true; $('email-work').checked = false; $('email-help').checked = false;
      $('email-save').disabled = true; $('email-deliveries').replaceChildren(); $('email-status').textContent = ''; $('email-mode').textContent = ''; $('email-recover').hidden = true; $('email-recovery-guidance').textContent = ''; $('email-recoveries').replaceChildren(); $('email-recoveries-more').hidden = true; $('email-opt-outs').replaceChildren(); $('email-opt-outs-more').hidden = true;
    }
    sync() { this.host.$('email-section').hidden = !this.active(); }
    async action(task) {
      const at = this.host.epoch();
      try { if (this.active() && !this.busy) await task(); }
      catch (error) { if (at === this.host.epoch()) this.host.$('email-status').textContent = error.message; }
    }
    async load() {
      const at = this.host.epoch(), request = ++this.request, $ = this.host.$;
      this.version = undefined; $('email-save').disabled = true;
      $('email-deliveries').replaceChildren();
      const [p, history] = await Promise.all([this.host.api(`${this.host.base()}/email-preferences`), this.host.api(`${this.host.base()}/email-deliveries`)]);
      this.host.current(at); if (request !== this.request) return;
      if (!p || typeof p.work_outcomes !== 'boolean' || typeof p.help_replies !== 'boolean' || typeof p.available !== 'boolean' || !Number.isSafeInteger(p.version) || p.version < 0 || !['disabled', 'local', 'resend'].includes(p.mode) || p.pause_reason && !['hard_bounce', 'complaint'].includes(p.pause_reason) || p.expires_at && !Number.isFinite(new Date(p.expires_at).getTime()) || !Array.isArray(history?.deliveries) || !Number.isSafeInteger(p.delivery_page_size) || p.delivery_page_size < 1 || history.deliveries.length > p.delivery_page_size) throw new Error('Email preferences could not be checked. Refresh to try again.');
      for (const d of history.deliveries) if (!Object.hasOwn(statuses, d.status) || typeof d.notification_id !== 'string' || !Number.isSafeInteger(d.attempts) || d.attempts < 0 || !Number.isFinite(new Date(d.updated_at).getTime()) || d.reported_at && !Number.isFinite(new Date(d.reported_at).getTime())) throw new Error('Email delivery status could not be checked.');
      this.paused = Boolean(p.pause_reason); this.version = p.version; this.generation = p.generation; this.pageSize = p.delivery_page_size; $('email-work').checked = p.work_outcomes; $('email-help').checked = p.help_replies;
      $('email-work').disabled = !p.available && !p.work_outcomes; $('email-help').disabled = !p.available && !p.help_replies;
      $('email-save').disabled = !p.available && !p.work_outcomes && !p.help_replies;
      $('email-mode').textContent = p.mode === 'local' ? 'Internal testing: messages are captured locally. No email is sent.' : p.mode === 'resend' ? 'Provider acceptance and receiving-server delivery reports do not confirm inbox placement or reading.' : 'Email delivery is paused.';
      if (!p.available && p.mode !== 'disabled') $('email-mode').textContent += ' Notification setup is unavailable.';
      $('email-status').textContent = p.expires_at ? `${new Date(p.expires_at).getTime() <= Date.now() ? 'Consent expired' : 'Consent expires'} ${new Date(p.expires_at).toISOString().slice(0, 10)}. Save your choices to renew. Only new updates after opt-in are eligible.` : 'Choose which new updates you want by email.';
      if (this.paused) $('email-status').textContent = `Personal notifications are paused after a ${p.pause_reason === 'complaint' ? 'complaint' : 'hard bounce'}${p.paused_at ? ` since ${new Date(p.paused_at).toISOString()}` : ''}. Saving choices does not resume sending.`;
      const recoveryReasons = { verified_replacement_required: 'Verify a different sign-in email address in your account, then return here. Confirming recovery renews consent for your selected categories. Only future updates will be eligible; cancelled messages stay cancelled.', complaint_review: 'A complaint requires review by your product team. Recovery is unavailable here.', recipient_evidence_unavailable: 'This pause has no retained address evidence. Contact your product team for review.', setup_unavailable: 'Notification setup is unavailable. Try refreshing later.' };
      $('email-recovery-guidance').textContent = this.paused ? recoveryReasons[p.recovery_blocked_reason] || 'Contact your product team to review this pause.' : '';
      this.recoveryAllowed = p.recovery_blocked_reason === 'verified_replacement_required' && Number.isSafeInteger(p.generation) && p.generation > 0;
      this.recoveryButtons();
      $('email-deliveries').replaceChildren();
      for (const d of history.deliveries) $('email-deliveries').append(node('li', `${statuses[d.status]} · ${d.notification_id} · Attempts: ${d.attempts} · ${d.updated_at}${d.reported_at ? ` · Provider reported ${d.reported_at}` : ''}${d.reason ? ` · ${d.reason.replaceAll('_', ' ')}` : ''}`));
      if (!history.deliveries.length) $('email-deliveries').append(node('li', 'No recent email deliveries visible with your current permissions.'));
      await Promise.all([this.history(), this.optOutHistory()]);
    }
    async save() {
      if (this.busy || this.version === undefined) return;
      if (this.pendingRecovery) throw new Error('Check the pending notification recovery before changing preferences.');
      const at = this.host.epoch(), $ = this.host.$;
      const request = { work_outcomes: $('email-work').checked === true, help_replies: $('email-help').checked === true, expected_version: this.version };
      this.busy = true; this.request++; $('email-save').disabled = true; let failure;
      try { await this.host.api(`${this.host.base()}/email-preferences`, { method: 'PUT', body: JSON.stringify(request) }); this.host.current(at); }
      catch (error) { this.host.current(at); failure = error; }
      finally { if (at === this.host.epoch()) this.busy = false; }
      this.host.current(at); await this.load();
      $('email-status').textContent = failure ? `${failure.message} Preferences reloaded. Review your choices before saving again.` : this.paused ? 'Preferences saved. Personal notifications remain paused after a hard bounce or complaint.' : request.work_outcomes || request.help_replies ? 'Email preferences saved. New eligible updates will be queued while you are away.' : 'Email notifications are off for you in this workspace.';
    }
    recoveryButtons() {
      const $ = this.host.$;
      $('email-recover').hidden = !this.pendingRecovery && !this.recoveryAllowed;
      $('email-recover').disabled = this.busy;
      $('email-recover').textContent = this.pendingRecovery ? 'Check or repeat the same recovery' : 'Resume notifications with my verified replacement address';
      if (this.pendingRecovery) { $('email-save').disabled = true; $('email-work').disabled = true; $('email-help').disabled = true; }
    }
    async recover() {
      if (this.busy || (!this.pendingRecovery && !this.recoveryAllowed)) return;
      const at = this.host.epoch(), $ = this.host.$;
      if (!this.pendingRecovery) {
        if (!$('email-work').checked && !$('email-help').checked) throw new Error('Choose at least one notification category before confirming recovery.');
        this.pendingRecovery = { id: crypto.randomUUID(), expected_version: this.version, expected_generation: this.generation, work_outcomes: $('email-work').checked === true, help_replies: $('email-help').checked === true };
      }
      const request = this.pendingRecovery; this.busy = true; this.recoveryButtons();
      try {
        const receipt = await this.host.api(`${this.host.base()}/email-recovery`, { method: 'POST', body: JSON.stringify(request) }); this.host.current(at);
        if (receipt?.id !== request.id || receipt.expected_version !== request.expected_version || receipt.previous_generation !== request.expected_generation || receipt.version !== request.expected_version + 1 || receipt.generation !== request.expected_generation + 1 || receipt.work_outcomes !== request.work_outcomes || receipt.help_replies !== request.help_replies || !Number.isFinite(new Date(receipt.recovered_at).getTime())) throw new Error('Recovery receipt could not be verified.');
        await this.load(); this.host.current(at); this.pendingRecovery = null;
        $('email-status').textContent = `Recovery confirmed: ${receipt.id}. ${this.paused ? 'Notifications are currently paused again; review the current reason.' : 'Only future eligible updates will be queued for your verified replacement address.'}`;
      } catch (error) {
        this.host.current(at);
        if (error.code === 'product_email_recovery_conflict' || error.status === 400 || error.status === 403) { this.pendingRecovery = null; await this.load(); }
        $('email-status').textContent = this.pendingRecovery ? `${error.message} Recovery may have completed. Check or repeat the same recovery; do not start another request.` : error.message;
      } finally { if (at === this.host.epoch()) { this.busy = false; this.recoveryButtons(); } }
    }
    async history(cursor = '') {
      const at = this.host.epoch(), request = this.request, $ = this.host.$;
      const p = await this.host.api(`${this.host.base()}/email-recoveries${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`); this.host.current(at);
      if (request !== this.request) return;
      if (!Array.isArray(p?.receipts) || p.receipts.length > this.pageSize || p.next_cursor && typeof p.next_cursor !== 'string') throw new Error('Notification recovery history could not be checked.');
      const rows = [];
      for (const r of p.receipts) {
        if (typeof r.id !== 'string' || !Number.isSafeInteger(r.generation) || r.generation < 2 || !Number.isFinite(new Date(r.recovered_at).getTime())) throw new Error('Notification recovery history could not be checked.');
        rows.push(node('li', `Recovery ${r.id} · Confirmed ${r.recovered_at} · Only future updates were enabled. Current delivery state is checked separately.`));
      }
      if (!cursor) $('email-recoveries').replaceChildren();
      for (const row of rows) $('email-recoveries').append(row);
      if (!cursor && !rows.length) $('email-recoveries').append(node('li', 'No retained notification recoveries.'));
      this.recoveryNext = p.next_cursor || ''; $('email-recoveries-more').hidden = !this.recoveryNext;
    }
    async optOutHistory(cursor = '') {
      const at = this.host.epoch(), request = this.request, $ = this.host.$;
      const p = await this.host.api(`${this.host.base()}/email-opt-outs${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`); this.host.current(at);
      if (request !== this.request) return;
      if (!Array.isArray(p?.receipts) || p.receipts.length > this.pageSize || p.next_cursor && typeof p.next_cursor !== 'string') throw new Error('Notification opt-out history could not be checked.');
      const rows = [];
      for (const r of p.receipts) {
        if (typeof r.id !== 'string' || !Number.isSafeInteger(r.version) || r.version < 1 || !Number.isFinite(new Date(r.unsubscribed_at).getTime())) throw new Error('Notification opt-out history could not be checked.');
        rows.push(node('li', `Unsubscribe ${r.id} · Confirmed ${r.unsubscribed_at} · Preferences were turned off. Current choices are checked separately.`));
      }
      if (!cursor) $('email-opt-outs').replaceChildren();
      for (const row of rows) $('email-opt-outs').append(row);
      if (!cursor && !rows.length) $('email-opt-outs').append(node('li', 'No retained notification opt-outs.'));
      this.optOutNext = p.next_cursor || ''; $('email-opt-outs-more').hidden = !this.optOutNext;
    }
    async connect() { this.sync(); await this.action(() => this.load()); }
  };
})();
