(() => {
  'use strict';
  const node = (tag, text = '') => { const value = document.createElement(tag); value.textContent = text; return value; };
  const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const hex = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  const labels = permissions => [['view_work', 'View work'], ['run_work', 'Run work'], ['view_billing', 'View billing'], ['manage_plan', 'Manage plan'], ['manage_team', 'Manage team'], ['manage_billing', 'Manage billing details']].filter(([key]) => permissions?.[key] === true).map(([, label]) => label).join(', ') || 'No product access';
  globalThis.GregaleProductTeam = class {
    constructor(host) {
      this.host = host; this.reset();
      const $ = host.$;
      $('team-load').onclick = () => this.action(() => this.load());
      $('team-invite-form').onsubmit = event => { event.preventDefault(); return this.action(() => {
        if (!this.team) throw new Error('Load the team first.');
        const role = $('team-invite-role').value;
        if (!this.team.assignable_roles.some(item => item.role === role)) throw new Error('Choose an approved role.');
        return this.review({ action: 'invite', email: $('team-email').value.trim().toLowerCase(), role, expires_in: this.team.invitation_default_seconds });
      }); };
      $('team-invite-form').oninput = () => this.invalidateReview();
      $('team-confirm').onclick = () => this.action(() => this.confirm());
      $('team-check').onclick = () => this.action(() => this.check());
      $('team-history-more').onclick = () => this.action(() => this.history(true));
    }
    active() { return this.host.view()?.access?.permissions?.manage_team === true; }
    capture() { return { epoch: this.host.epoch(), generation: this.generation }; }
    current(value) { this.host.current(value.epoch); if (value.generation !== this.generation) throw new Error('Team access changed. Load the current team.'); }
    async action(task) {
      const at = this.capture();
      try { if (!this.active()) throw new Error('Your role does not allow team management.'); await task(); }
      catch (error) {
        if (at.epoch !== this.host.epoch() || at.generation !== this.generation) return;
        if (error.status === 401 || error.status === 403) this.reset();
        this.host.message(error.message);
      }
    }
    reset() {
      this.generation = (this.generation || 0) + 1; this.request = (this.request || 0) + 1; this.historyRequest = (this.historyRequest || 0) + 1;
      this.team = undefined; this.preview = undefined; this.cursor = ''; this.policyRevision = '';
      for (const id of ['team-members', 'team-invitations', 'team-invite-role', 'team-review', 'team-link', 'team-history']) this.host.$(id).replaceChildren();
      this.host.$('team-email').value = ''; this.host.$('team-confirm').hidden = true; this.host.$('team-check').hidden = true; this.host.$('team-history-more').hidden = true;
      this.host.$('team-invite-submit').disabled = true; this.host.$('team-status').textContent = '';
    }
    sync() {
      this.host.$('team-section').hidden = !this.active(); if (!this.active()) return;
      const revision = this.host.view().access.policy_revision;
      if (this.policyRevision && revision !== this.policyRevision) { this.reset(); this.host.$('team-status').textContent = 'Team permissions changed. Load the current team before reviewing another action.'; }
      this.policyRevision = revision;
    }
    async connect() { this.sync(); if (this.active()) await this.action(async () => { await this.load(); await this.check(); await this.history(); }); }
    key() { const view = this.host.view(); return `gregale.product.team.v1:${JSON.stringify([view.account_id, view.app_id, view.customer.tenant_id])}`; }
    lock(task) { return this.host.locks.request(this.key(), task); }
    pending() {
      const raw = this.host.storage.getItem(this.key()); if (!raw) return null;
      let value; const max = this.host.maxBytes();
      try { if (!Number.isSafeInteger(max) || max < 1 || raw.length > max) throw new Error(); value = JSON.parse(raw); } catch { throw new Error('Saved team-action recovery details are invalid. Contact your product team before replacing them.'); }
      if (!value || Object.keys(value).sort().join(',') !== 'hash,id' || !uuid(value.id) || !hex(value.hash)) throw new Error('Saved team-action recovery details are invalid. Contact your product team before replacing them.');
      return value;
    }
    forget(value) { if (JSON.stringify(this.pending()) === JSON.stringify(value)) this.host.storage.removeItem(this.key()); }
    invalidateReview() { this.preview = undefined; this.host.$('team-review').replaceChildren(); this.host.$('team-confirm').hidden = true; }
    button(text, task) { const value = node('button', text); value.onclick = () => this.action(task); return value; }
    async load(keepStatus = false) {
      const at = this.capture(), request = ++this.request;
      const team = await this.host.api(`${this.host.base()}/team`); this.current(at); if (request !== this.request) return;
      this.team = team; this.policyRevision = team.revision; this.invalidateReview();
      const $ = this.host.$; $('team-members').replaceChildren(); $('team-invitations').replaceChildren(); $('team-invite-role').replaceChildren();
      for (const role of team.assignable_roles) { const option = node('option', `${role.role} · ${labels(role.product_permissions)}`); option.value = role.role; $('team-invite-role').append(option); }
      $('team-invite-submit').disabled = !team.assignable_roles.length || team.suspended;
      for (const member of team.members) {
        const row = node('li', `${member.email || member.user_id}${member.user_id === team.actor_id ? ' (you)' : ''} · ${member.role} · ${labels(member.product_permissions)}`);
        if (member.can_manage) {
          const select = node('select');
          for (const role of team.assignable_roles) { const option = node('option', role.role); option.value = role.role; select.append(option); }
          select.value = member.role; select.onchange = () => this.invalidateReview();
          row.append(select, this.button('Review role change', () => this.review({ action: 'set_role', target_id: member.user_id, role: select.value })), this.button('Review removal', () => this.review({ action: 'remove_member', target_id: member.user_id })));
        }
        $('team-members').append(row);
      }
      for (const invite of team.invitations) {
        const row = node('li', `${invite.email} · ${invite.role} · Expires ${invite.expires_at} · ${labels(invite.product_permissions)}`);
        if (invite.can_revoke) row.append(this.button('Review revocation', () => this.review({ action: 'revoke_invitation', target_id: invite.id })));
        $('team-invitations').append(row);
      }
      if (!team.invitations.length) $('team-invitations').append(node('li', 'No pending invitations.'));
      if (!keepStatus) { $('team-link').replaceChildren(); $('team-status').textContent = team.suspended ? 'Team changes are paused. Contact your product team.' : 'Choose an invitation or member change to review. Your own role is managed by another workspace owner.'; }
    }
    async review(draft) {
      if (!this.team) throw new Error('Load the current team first.');
      const at = this.capture(), request = ++this.request;
      await this.lock(async () => {
        this.current(at); const pending = this.pending();
        if (pending) {
          try { const receipt = await this.host.api(`${this.host.base()}/team/actions/${encodeURIComponent(pending.id)}`); this.current(at); this.validateReceipt(receipt, pending.id); this.forget(pending); this.recorded(receipt); return; }
          catch (error) { this.current(at); if (error.status !== 404) throw error; }
        }
        const selection = { id: pending?.id || this.host.crypto.randomUUID(), ...draft };
        const preview = await this.host.api(`${this.host.base()}/team/preview`, { method: 'POST', body: JSON.stringify(selection) }); this.current(at); if (request !== this.request) return;
        if (!hex(preview.hash) || !hex(preview.auth_snapshot) || ['id', 'action', 'target_id', 'email', 'role', 'expires_in'].some(key => (preview.request[key] ?? '') !== (selection[key] ?? ''))) throw new Error('Team review did not match your selection.');
        this.preview = { value: preview, prior: JSON.stringify(pending) }; this.comparison(preview);
        this.host.$('team-confirm').hidden = false; this.host.$('team-confirm').disabled = false; this.host.$('team-status').textContent = 'Review both product and workspace permissions, then confirm. Workspace roles can affect other connected products.';
      });
    }
    comparison(preview) {
      const list = this.host.$('team-review'); list.replaceChildren();
      list.append(node('h3', `${preview.request.action.replaceAll('_', ' ')} · ${preview.subject}`));
      const table = node('table');
      for (const [label, before, after] of [['Role', preview.before_role || 'None', preview.after_role || 'None'], ['Product permissions', labels(preview.before_permissions), labels(preview.after_permissions)], ['Workspace permissions', preview.before_workspace_permissions.join(', ') || 'None', preview.after_workspace_permissions.join(', ') || 'None']]) {
        const row = node('tr'); row.append(node('th', label), node('td', before), node('td', after)); table.append(row);
      }
      list.append(table);
      if (preview.request.action === 'invite') list.append(node('p', `The invitation lasts ${preview.request.expires_in / 86400} days. Only the invited, confirmed email address can accept.`));
    }
    validateReceipt(receipt, id, hash) {
      if (receipt?.id !== id || receipt?.request?.id !== id || !uuid(id) || !receipt.result || hash && receipt.review_hash !== hash) throw new Error('The team-action receipt did not match your confirmation. Keep its reference and contact your product team.');
    }
    recorded(receipt, invitationURL = '') {
      this.invalidateReview(); const $ = this.host.$; $('team-check').hidden = true; $('team-link').replaceChildren();
      const result = receipt.result, subject = result.email || receipt.request.email || receipt.request.target_id || '';
      $('team-status').textContent = `Recorded ${receipt.action.replaceAll('_', ' ')} · ${subject} · ${result.before_role || 'None'} → ${result.after_role || 'None'} · ${receipt.created_at}. Reference ${receipt.id}.`;
      if (invitationURL) {
        const link = node('a', 'Open invitation acceptance'); link.href = invitationURL; link.rel = 'noreferrer noopener'; link.target = '_blank';
        const field = node('input'); field.value = invitationURL; field.readOnly = true;
        $('team-link').append(node('p', 'Copy this invitation link now; it is shown once. The recipient signs in with the invited email to accept, then returns to this product to sign in.'), field, link);
      } else if (receipt.action === 'invite') $('team-link').append(node('p', result.delivered ? 'The invitation was sent through your configured email provider.' : 'The invitation exists, but its link is shown only once. If you do not have it, revoke this invitation and explicitly create a new one.'));
    }
    async confirm() {
      const at = this.capture(), selected = this.preview;
      if (!selected) throw new Error('Review a team action first.');
      const p = selected.value;
      if (!this.host.confirm(`Confirm ${p.request.action.replaceAll('_', ' ')} for ${p.subject}: ${p.before_role || 'None'} → ${p.after_role || 'None'}? This changes access across the workspace.`)) return;
      this.host.$('team-confirm').disabled = true;
      try {
        await this.lock(async () => {
          this.current(at); if (JSON.stringify(this.pending()) !== selected.prior) throw new Error('Recovery details changed in another tab. Check the saved team action before continuing.');
          const saved = { id: p.request.id, hash: p.hash }; this.host.storage.setItem(this.key(), JSON.stringify(saved)); this.host.$('team-check').hidden = false;
          let value;
          try {
            value = await this.host.api(`${this.host.base()}/team/confirm`, { method: 'POST', body: JSON.stringify({ request: p.request, auth_snapshot: p.auth_snapshot, expected_hash: p.hash }) }); this.current(at);
            this.validateReceipt(value.receipt, saved.id, saved.hash);
            if (value.invitation_url) { const link = new URL(value.invitation_url); if (link.protocol !== 'https:' || link.pathname !== '/ui/invitation' || link.search || link.username || link.password || !/^#token=[A-Za-z0-9_-]{43}$/.test(link.hash)) throw new Error('The invitation link was invalid. Check the recorded action.'); }
          } catch (error) {
            this.current(at); this.invalidateReview(); const failure = new Error(`${error.message} Your team-action reference is saved. Check its status before reviewing another action. No action was automatically resubmitted.`); failure.status = error.status; throw failure;
          }
          this.forget(saved); this.recorded(value.receipt, value.invitation_url || '');
        });
        await this.load(true); await this.history();
      } finally { if (at.epoch === this.host.epoch()) this.host.$('team-confirm').disabled = false; }
    }
    async check() {
      const at = this.capture();
      await this.lock(async () => {
        this.current(at); const saved = this.pending(); this.host.$('team-check').hidden = !saved; if (!saved) return;
        try {
          const receipt = await this.host.api(`${this.host.base()}/team/actions/${encodeURIComponent(saved.id)}`); this.current(at);
          this.validateReceipt(receipt, saved.id); this.forget(saved); this.recorded(receipt);
        } catch (error) {
          this.current(at); if (error.status !== 404) throw error;
          this.host.$('team-status').textContent = `Action ${saved.id} is not recorded. Review the current team and confirm again with the saved reference. No action was resubmitted.`;
        }
      });
    }
    async history(next = false) {
      const at = this.capture(), request = ++this.historyRequest;
      const page = await this.host.api(`${this.host.base()}/team/actions?cursor=${encodeURIComponent(next ? this.cursor : '')}`); this.current(at); if (request !== this.historyRequest) return;
      this.cursor = page.next_cursor || ''; const list = this.host.$('team-history'); list.replaceChildren();
      for (const receipt of page.actions) list.append(node('li', `${receipt.action.replaceAll('_', ' ')} · ${receipt.result.email || receipt.request.target_id || ''} · ${receipt.result.before_role || 'None'} → ${receipt.result.after_role || 'None'} · ${receipt.created_at} · By ${receipt.actor_role} (${receipt.actor_id}) · Reference ${receipt.id}`));
      if (!page.actions.length) list.append(node('li', 'No team-management actions recorded.'));
      this.host.$('team-history-more').hidden = !this.cursor;
    }
  };
})();
