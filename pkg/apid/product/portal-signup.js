(() => {
  'use strict';
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  const slug = /^[a-z][a-z0-9-]{0,62}$/;
  const https = value => { try { const url = new URL(value); return url.protocol === 'https:' ? url.href : ''; } catch { return ''; } };
  const money = value => Number.isSafeInteger(value) && value >= 0;
  // Self-serve signup (ADR-20261022100735): the product's own Auth app creates
  // the account; the portal then creates the user's workspace on an offered plan.
  globalThis.GregaleProductSignup = class {
    constructor(host) {
      this.host = host; const $ = host.$;
      $('signup-open').onclick = () => this.action(() => this.browse());
      $('signup-form').onsubmit = event => { event.preventDefault(); return this.action(() => this.create()); };
      $('signup-signout').onclick = () => $('disconnect').onclick();
      this.reset();
    }
    reset() {
      this.request = (this.request || 0) + 1; this.busy = false; this.plans = new Map();
      const $ = this.host.$; $('signup-section').hidden = true; $('signup-plans').replaceChildren(); $('signup-plan').replaceChildren(); $('signup-form').hidden = true;
      $('signup-account').hidden = true; $('signup-account').href = '#'; $('signup-signout').hidden = true; $('signup-status').textContent = ''; $('signup-submit').disabled = false;
    }
    // created names the workspace a signup just made, so the portal opens it.
    workspace() { return this.created || ''; }
    key() { return `gregale.product.signup.v1:${this.host.app()}`; }
    async action(task) {
      const at = this.host.epoch();
      try { if (!this.busy) await task(); }
      catch (error) { if (at === this.host.epoch()) this.host.$('signup-status').textContent = error.message; }
    }
    // offer is the product's public offer, or null when it offers no signup.
    async offer(app) {
      const at = this.host.epoch(), request = ++this.request;
      const response = await this.host.raw(`/v1/product-control/customer/${encodeURIComponent(app)}/offer`, {}, false);
      this.host.current(at); if (request !== this.request) throw new Error('The signup offer changed. Try again.');
      if (response.status === 404) return null;
      if (!response.ok) throw await this.host.problem(response);
      const offer = await response.json(); this.host.current(at);
      if (!offer || offer.product !== app || typeof offer.currency !== 'string' || !https(offer.sign_up_url) || !Array.isArray(offer.plans) || !offer.plans.length ||
        offer.plans.some(p => typeof p.plan !== 'string' || typeof p.label !== 'string' || !Array.isArray(p.features) || p.monthly_fee_millicents !== undefined && !money(p.monthly_fee_millicents) ||
          p.trial_days !== undefined && !Number.isSafeInteger(p.trial_days) || p.features.some(f => typeof f.label !== 'string' || !money(f.unit_price_millicents) || !Number.isSafeInteger(f.monthly_starts)))) throw new Error('The signup offer could not be checked. Try again later.');
      return offer;
    }
    terms(plan, currency) {
      const parts = [plan.monthly_fee_millicents > 0 ? `${this.host.amount(plan.monthly_fee_millicents, currency)} per month` : 'No monthly fee'];
      if (plan.trial_days > 0) parts.push(`${plan.trial_days}-day free trial`);
      return parts.join(' · ');
    }
    feature(f, currency) {
      const price = `${this.host.amount(f.unit_price_millicents, currency)} per successful run`;
      return `${f.label}: ${f.included_starts > 0 ? `${f.included_starts} included runs, then ${price}` : price}, up to ${f.monthly_starts} starts a month`;
    }
    render(offer) {
      const $ = this.host.$; this.plans = new Map(); $('signup-plans').replaceChildren(); $('signup-plan').replaceChildren();
      for (const plan of offer.plans) {
        this.plans.set(plan.plan, plan);
        const item = node('li'); item.append(node('h3', plan.label), node('p', this.terms(plan, offer.currency)));
        for (const f of plan.features) item.append(node('p', this.feature(f, offer.currency)));
        $('signup-plans').append(item);
        const option = node('option', `${plan.label} · ${this.terms(plan, offer.currency)}`); option.value = plan.plan; $('signup-plan').append(option);
      }
      $('signup-section').hidden = false;
    }
    // browse shows the offer before sign-in, with the Auth app's sign-up page.
    async browse() {
      const $ = this.host.$, app = $('app').value.trim();
      if (!slug.test(app)) throw new Error('Enter your product name to see its plans.');
      this.reset();
      const offer = await this.offer(app);
      if (!offer) { $('signup-section').hidden = false; $('signup-status').textContent = 'This product does not offer sign-up. Ask your product team for an invitation.'; return; }
      this.render(offer);
      $('signup-account').href = https(offer.sign_up_url); $('signup-account').hidden = false;
      $('signup-status').textContent = 'Create your account on the product\'s sign-in page and confirm your email if asked. Then return here and sign in to choose your plan.';
    }
    // choose offers a signed-in user without a workspace to create one. It
    // reports whether the product offers signup.
    async choose() {
      const $ = this.host.$, offer = await this.offer(this.host.app());
      if (!offer) return false;
      this.render(offer);
      $('login').hidden = true; $('signup-form').hidden = false; $('signup-signout').hidden = false;
      $('signup-status').textContent = 'Name your workspace and choose a plan to get started.';
      return true;
    }
    async create() {
      const $ = this.host.$, at = this.host.epoch(), name = $('signup-name').value.trim(), plan = this.plans.get($('signup-plan').value);
      if (!name) throw new Error('Name your workspace.');
      if (!plan) throw new Error('Choose a plan.');
      if (!this.host.confirm(`Create workspace "${name}" on plan ${plan.label}?\n${this.terms(plan, plan.currency)}.`)) return;
      // The request ID survives a reload, so a retry finds what the first attempt created.
      let saved; try { saved = JSON.parse(this.host.storage.getItem(this.key()) || 'null'); } catch { saved = null; }
      const id = uuid.test(saved?.id) ? saved.id : this.host.crypto.randomUUID();
      this.host.storage.setItem(this.key(), JSON.stringify({ id }));
      this.busy = true; $('signup-submit').disabled = true; $('signup-status').textContent = 'Creating your workspace…';
      let receipt;
      try { receipt = await this.host.api(`${this.host.base()}/signup`, { method: 'POST', body: JSON.stringify({ id, plan: plan.plan, workspace_name: name }) }); this.host.current(at); }
      catch (error) {
        if (at === this.host.epoch() && error.status === 409 && error.code === 'conflict') { this.host.storage.removeItem(this.key()); throw new Error(`${error.message} Try again to start a new request.`); }
        throw error;
      } finally { if (at === this.host.epoch()) { this.busy = false; $('signup-submit').disabled = false; } }
      if (!uuid.test(receipt?.organization_id)) throw new Error('Your workspace could not be confirmed. Sign in again to check it.');
      this.host.storage.removeItem(this.key()); this.created = receipt.organization_id;
      await this.host.reconnect();
      this.host.message(`Workspace ${receipt.workspace} created on plan ${plan.label}.`);
    }
  };
  // A card on file in the product team's Stripe (ADR-20261022100735). Card
  // details are entered only on Stripe's own page; the portal sees the brand,
  // last digits and expiry.
  globalThis.GregaleProductPayment = class {
    constructor(host) {
      this.host = host; const $ = host.$, url = new URL(host.href());
      this.returned = { session: url.searchParams.get('checkout') || '', workspace: url.searchParams.get('workspace') || '' };
      if (this.returned.session || this.returned.workspace) { url.searchParams.delete('checkout'); url.searchParams.delete('workspace'); host.replaceURL(url.href); }
      $('payment-add').onclick = () => this.action(() => this.checkout());
      $('payment-refresh').onclick = () => this.action(() => this.load());
      this.reset();
    }
    // workspace is the workspace a returning Checkout belongs to.
    workspace() { return uuid.test(this.returned.workspace) ? this.returned.workspace : ''; }
    active() { return Boolean(this.host.view()) && this.host.allowed('manage_billing'); }
    reset() {
      this.request = (this.request || 0) + 1; this.busy = false;
      const $ = this.host.$; $('payment-section').hidden = true; $('payment-card').textContent = ''; $('payment-status').textContent = ''; $('payment-consent').checked = false; $('payment-add').disabled = true;
    }
    sync() { if (!this.active()) this.host.$('payment-section').hidden = true; }
    async action(task) {
      const at = this.host.epoch();
      try { if (this.active() && !this.busy) await task(); }
      catch (error) { if (at === this.host.epoch()) this.host.$('payment-status').textContent = error.message; }
    }
    async connect() {
      const returned = this.returned; this.returned = { session: '', workspace: '' };
      if (!returned.session || returned.workspace !== this.host.organization()) return this.action(() => this.load());
      if (returned.session === 'cancelled') { await this.action(() => this.load()); if (this.active()) this.host.$('payment-status').textContent = 'Card setup was cancelled. No card was saved.'; return; }
      return this.action(() => this.complete(returned.session));
    }
    render(view) {
      const $ = this.host.$, card = view.payment_method;
      $('payment-section').hidden = !view.available; $('payment-add').disabled = false;
      if (!card) { $('payment-card').textContent = 'No card on file. Your product team emails invoices for payment.'; $('payment-add').textContent = 'Add a card'; }
      else {
        $('payment-card').textContent = `${card.brand ? card.brand[0].toUpperCase() + card.brand.slice(1) : 'Card'} ending ${card.last4} · expires ${String(card.exp_month).padStart(2, '0')}/${card.exp_year}. Your product team can charge this workspace's invoices to this card.`;
        $('payment-add').textContent = 'Replace card';
      }
      return this.host.rendered?.(Boolean(card));
    }
    valid(view) {
      const card = view?.payment_method;
      if (!view || typeof view.available !== 'boolean' || card !== undefined && (typeof card.brand !== 'string' || !/^\d{4}$/.test(card.last4) || !Number.isSafeInteger(card.exp_month) || !Number.isSafeInteger(card.exp_year))) throw new Error('Your payment method could not be checked. Refresh to try again.');
      return view;
    }
    async load() {
      const at = this.host.epoch(), request = ++this.request;
      const view = this.valid(await this.host.api(`${this.host.base()}/payment-method`)); this.host.current(at);
      if (request === this.request) await this.render(view);
    }
    async complete(session) {
      if (!/^cs_[A-Za-z0-9_]{1,250}$/.test(session)) throw new Error('The card page returned an unexpected reference. Add your card again.');
      const at = this.host.epoch(), request = ++this.request; this.busy = true; this.host.$('payment-status').textContent = 'Saving your card…';
      let view;
      try { view = this.valid(await this.host.api(`${this.host.base()}/payment-method/complete`, { method: 'POST', body: JSON.stringify({ session_id: session }) })); this.host.current(at); }
      finally { if (at === this.host.epoch()) this.busy = false; }
      if (request !== this.request) return;
      this.host.$('payment-status').textContent = 'Your card is saved for this workspace\'s invoices.'; await this.render(view);
    }
    async checkout() {
      const $ = this.host.$, at = this.host.epoch();
      if (!$('payment-consent').checked) throw new Error('Confirm that your product team may charge this card for future invoices.');
      this.busy = true; $('payment-add').disabled = true; $('payment-status').textContent = 'Opening the secure card page…';
      try {
        const page = await this.host.api(`${this.host.base()}/payment-method/checkout`, { method: 'POST', body: JSON.stringify({ consent: true }) }); this.host.current(at);
        const target = https(page?.url);
        if (!target) throw new Error('The card page could not be opened. Try again later.');
        this.host.navigate(target);
      } finally { if (at === this.host.epoch()) { this.busy = false; $('payment-add').disabled = false; } }
    }
  };
})();
