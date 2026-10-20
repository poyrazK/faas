(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const states = ['available', 'near_limit', 'allowance_exhausted', 'access_paused', 'permission_required', 'temporarily_unavailable', 'invoice_overdue'];
  const allowanceStates = ['available', 'near_limit', 'exhausted', 'no_allowance'];
  const utc = value => new Date(value).toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC');
  globalThis.GregaleProductGuidance = class {
    constructor(host) { this.host = host; this.reset(); }
    reset() {
      const $ = this.host.$;
      $('guidance-section').hidden = true; $('guidance-list').replaceChildren(); $('guidance-actions').replaceChildren();
      $('guidance-status').textContent = ''; $('guidance-period').textContent = '';
    }
    unavailable() {
      this.reset(); this.host.$('guidance-section').hidden = false;
      this.host.$('guidance-status').textContent = 'Usage guidance could not be checked. Refresh to check your current allowance.';
    }
    valid(g, view) {
      const permissions = view?.access?.permissions || {};
      if (!g || g.account_id !== view.account_id || g.app_id !== view.app_id || g.tenant_id !== view.customer.tenant_id || g.revision !== view.customer.revision || g.policy_revision !== view.access?.policy_revision || g.run_permission !== (permissions.run_work === true) || g.suspended !== view.customer.suspended || (g.invoice_overdue === true) !== Boolean(view.overdue_hold) || !Array.isArray(g.features) || g.features.length > Object.keys(view.remaining_starts || {}).length) return false;
      const observed = new Date(g.observed_at), reset = new Date(g.resets_at);
      if (!Number.isFinite(observed.getTime()) || !Number.isFinite(reset.getTime()) || observed.toISOString().slice(0, 7) !== g.period || reset.getTime() !== Date.UTC(observed.getUTCFullYear(), observed.getUTCMonth() + 1, 1) || !Number.isSafeInteger(g.near_limit_percent) || g.near_limit_percent < 1 || g.near_limit_percent > 100) return false;
      if (!permissions.run_work && !permissions.view_billing && g.features.length) return false;
      const seen = new Set();
      return g.features.every(row => {
        if (!row || typeof row.feature !== 'string' || typeof row.label !== 'string' || seen.has(row.feature) || !Object.hasOwn(view.remaining_starts || {}, row.feature) || ![row.limit, row.starts, row.remaining].every(n => Number.isSafeInteger(n) && n >= 0) || row.remaining !== Math.max(0, row.limit - row.starts) || row.remaining !== view.remaining_starts[row.feature] || !states.includes(row.state) || !allowanceStates.includes(row.allowance_state)) return false;
        if (g.suspended ? row.state !== 'access_paused' : !g.run_permission ? row.state !== 'permission_required' : (row.state === 'invoice_overdue') !== (g.invoice_overdue === true)) return false;
        seen.add(row.feature); return true;
      });
    }
    sync() {
      const view = this.host.view(), g = view?.guidance;
      if (!this.valid(g, view)) { this.unavailable(); return; }
      const $ = this.host.$;
      $('guidance-section').hidden = false; $('guidance-list').replaceChildren(); $('guidance-actions').replaceChildren();
      $('guidance-period').textContent = `${g.period} · Allowances reset ${utc(g.resets_at)}. Checked ${utc(g.observed_at)}.`;
      const status = g.suspended ? 'New runs are paused for this workspace. Contact your product team.' : g.invoice_overdue && g.run_permission ? 'New runs are paused until your overdue invoice is paid. Work already running continues.' : !g.run_permission ? 'Your role does not allow running features. Ask a workspace administrator if you need access.' : g.features.length ? 'These counts cover starts accepted this month, including failed, cancelled and pending work. Changing plans keeps this month’s used starts.' : 'Your assigned plan has no features available to run. Contact your product team.';
      if ($('guidance-status').textContent !== status) $('guidance-status').textContent = status;
      for (const row of g.features) {
        const card = node('article'); card.className = 'guidance-card';
        card.append(node('h3', row.label), node('p', `${row.starts} of ${row.limit} starts used · ${row.remaining} remaining`));
        if (row.limit > 0) {
          const label = node('label', `${row.label} usage`), progress = node('progress'); progress.max = row.limit; progress.value = Math.min(row.starts, row.limit); label.append(progress); card.append(label);
        }
        card.append(node('p', this.explanation(row, g)));
        if (row.state !== 'available' && this.host.help) { const help = node('button', 'Get help with this feature'); help.type = 'button'; help.className = 'secondary'; help.onclick = () => { if (this.host.view()?.guidance === g) this.host.help(row.feature); }; card.append(help); }
        $('guidance-list').append(card);
      }
      if (g.features.some(row => row.state === 'near_limit' || row.state === 'allowance_exhausted')) this.planAction(g);
    }
    explanation(row, g) {
      switch (row.state) {
        case 'access_paused': return 'New runs are paused for your workspace. Contact your product team.';
        case 'invoice_overdue': return 'New runs are paused until your overdue invoice is paid.';
        case 'permission_required': return 'Your role cannot run this feature. Ask a workspace administrator for access.';
        case 'temporarily_unavailable': return 'This feature is temporarily unavailable. Refresh to check again or contact your product team.';
        case 'allowance_exhausted': return row.limit === 0 ? 'This plan includes no starts for this feature. Ask about a plan that includes starts.' : `Your monthly allowance is used up. The next allowance period starts ${utc(g.resets_at)}.`;
        case 'near_limit': return `You have used at least ${g.near_limit_percent}% of this feature’s monthly allowance.`;
        default: return 'You have starts remaining. Availability and allowance are checked again when you confirm a run.';
      }
    }
    planAction(g) {
      const container = this.host.$('guidance-actions');
      if (g.plan_action === 'review_plans' && this.host.view()?.access?.permissions?.manage_plan === true && !g.suspended) {
        const button = node('button', 'Review available plan changes'); button.type = 'button';
        button.onclick = () => { if (this.host.view()?.guidance === g) return this.host.reviewPlans(); };
        container.append(button, node('p', 'Review current choices and prices before confirming a change.'));
      } else {
        container.append(node('p', g.plan_action === 'ask_workspace_admin' ? 'Ask a workspace administrator about your allowance or available plan changes.' : 'Contact your product team about your allowance. No self-service plan change is currently offered.'));
      }
    }
  };
})();
