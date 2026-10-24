(() => {
  'use strict';
  const $ = id => document.getElementById(id), { Receipts, encodeInput } = GregaleProduct;
  const node = (tag, text = '') => { const el = document.createElement(tag); el.textContent = text; return el; };
  const message = text => { $('message').textContent = text; };
  const amount = (value, currency) => { if (!Number.isSafeInteger(value)) return 'Amount exceeds browser precision; ask your product team.'; const n = BigInt(value), abs = n < 0n ? -n : n; return `${currency} ${n < 0n ? '-' : ''}${(abs / 100000n).toString()}.${(abs % 100000n).toString().padStart(5, '0').replace(/0+$/, '').padEnd(2, '0')}`; };
  const paymentText = { draft: 'Invoice being prepared', open: 'Invoice open', paid: 'Invoice paid', void: 'Invoice voided', uncollectible: 'Invoice marked uncollectible', deleted: 'Invoice withdrawn' };
  // An exported statement's provider invoice (ADR-20261018212918). The link is only ever the provider's own https page.
  function invoiceNodes(invoice) {
    const parts = [invoice.overdue ? 'Invoice overdue' : paymentText[invoice.payment_status] || 'Invoice issued; payment status not available yet'];
    if (typeof invoice.due_at === 'string') parts.push(`due ${invoice.due_at.slice(0, 10)}`);
    // Millicents need no knowledge of the currency's decimal places (ADR-20261019100330).
    if (invoice.amount_remaining_millicents !== undefined && invoice.payment_status !== 'paid') parts.push(`${amount(invoice.amount_remaining_millicents, invoice.currency)} remaining`);
    const nodes = [node('p', parts.join(' · '))];
    let link = null;
    try { link = new URL(invoice.hosted_invoice_url); } catch { link = null; }
    if (link?.protocol === 'https:') { const a = node('a', invoice.payment_status === 'open' ? 'View and pay invoice' : 'View invoice'); a.href = link.href; a.target = '_blank'; a.rel = 'noopener noreferrer'; nodes.push(a); }
    return nodes;
  }
  const terminal = state => ['succeeded', 'failed', 'cancelled', 'requires_reconciliation', 'expired'].includes(state);
  let app = new URL(location.href).searchParams.get('app') || '', organization = '', epoch = 0, view, receipts, timer, controller = new AbortController(), refreshing;
  const allowed = permission => view?.access?.permissions?.[permission] === true;
  const work = new Map(), inputs = new Map(), cards = new Map();
  let planControls, teamControls, guidanceControls, helpControls, notificationControls, activityControls, historyControls, historyPageSize, historyCursorMax, runControls, emailControls, activityPageSize, activityCursorMax, notificationPageSize, helpTextMax, helpPageSize, recoveryMaxBytes, refreshRequest = 0;
  let signupControls, paymentControls;
  let billingRequest = 0, billingCursor = '', billingPeriod = '';
  let resultRequest = 0;
  function clearResult() { resultRequest++; $('result').hidden = true; $('result').replaceChildren(); }
  $('billing-period').value = new Date().toISOString().slice(0, 7);
  function clearBilling() { billingRequest++; billingCursor = ''; billingPeriod = ''; $('billing-documents').replaceChildren(); $('billing-summary').textContent = 'Choose a month to review finalized charges and credit notes.'; $('billing-more').hidden = true; }
  // A month's plan fee (ADR-20261021154511).
  function feeText(fee, currency) {
    const from = typeof fee.prorated_from === 'string' ? ` prorated from ${fee.prorated_from.slice(0, 10)}` : '';
    return `the monthly fee for plan ${fee.plan}, ${amount(fee.amount_millicents, currency)}${from}`;
  }
  async function loadBilling(next = false, check = () => {}) {
    if (!allowed('view_billing')) throw new Error('Your role does not include billing access.');
    const generation = epoch, request = ++billingRequest, period = next ? billingPeriod : $('billing-period').value;
    if (!/^\d{4}-\d{2}$/.test(period)) throw new Error('Choose a billing month.');
    let data;
    try { data = await api(`${base()}/billing?period=${encodeURIComponent(period)}&cursor=${encodeURIComponent(next ? billingCursor : '')}`); }
    catch (error) { if (request !== billingRequest || generation !== epoch) return; throw error; }
    current(generation); check(); if (request !== billingRequest) return;
    const review = data.review; billingPeriod = period; billingCursor = data.documents.next_cursor || '';
    const fee = review.fees?.[0] ? ` including ${feeText(review.fees[0], review.currency)}` : '';
    $('billing-summary').textContent = review.finalized ? `${period} · Finalized charges ${amount(review.finalized.amount_millicents, review.currency)}. Credit notes reduce these charges.` : `${period} · Provisional charges ${amount(review.amount_millicents, review.currency)}${fee} · ${review.unresolved} unresolved runs. ${review.closed ? 'Awaiting finalization by your product team.' : 'This month is still open.'}`;
    $('billing-documents').replaceChildren();
    for (const doc of data.documents.documents) {
      const row = node('li', `${doc.kind === 'credit_note' ? 'Credit note' : 'Finalized statement'} · ${amount(doc.amount_millicents, doc.currency)} · ${doc.id}`);
      if (doc.reason) row.append(node('p', doc.reason));
      if (doc.snapshot) for (const line of doc.snapshot.lines) row.append(node('p', `${line.feature} · ${line.succeeded} charged successes · ${line.waived} waived · ${amount(line.amount_millicents, doc.currency)}`));
      for (const charge of doc.snapshot?.fees || []) row.append(node('p', feeText(charge, doc.currency)));
      if (doc.invoice) row.append(...invoiceNodes(doc.invoice));
      $('billing-documents').append(row);
    }
    if (!data.documents.documents.length) $('billing-documents').append(node('li', 'No finalized statement or credit notes for this month.'));
    $('billing-more').hidden = !billingCursor;
  }
  $('billing-form').onsubmit = event => { event.preventDefault(); const generation = epoch; return loadBilling().catch(error => { if (generation === epoch) message(error.message); }); };
  $('billing-more').onclick = () => { const generation = epoch; return loadBilling(true).catch(error => { if (generation === epoch) message(error.message); }); };
  // Billing details live in the product team's billing provider (ADR-20261020120000); Gregale only relays them.
  const detailFields = [['country_code', 'billing-country', 'address'], ['postal_code', 'billing-postal', 'address'], ['region', 'billing-region', 'address'], ['city', 'billing-city', 'address'],
    ['first_line', 'billing-line1', 'address'], ['second_line', 'billing-line2', 'address'], ['name', 'billing-business', 'business'], ['tax_identifier', 'billing-tax-id', 'business']];
  function clearBillingDetails() { $('billing-details-form').hidden = true; $('billing-details-status').textContent = ''; for (const [, id] of detailFields) $(id).value = ''; }
  function showBillingDetails(details) { for (const [key, id, part] of detailFields) $(id).value = details?.[part]?.[key] || ''; $('billing-details-form').hidden = false; }
  async function billingDetails(request, done) {
    const generation = epoch;
    try { const details = await request(); current(generation); showBillingDetails(details); $('billing-details-status').textContent = done(details); }
    catch (error) { if (generation === epoch) $('billing-details-status').textContent = error.message; }
  }
  $('billing-details-load').onclick = () => billingDetails(() => api(`${base()}/billing-details`),
    details => details.address ? 'Invoices are addressed to these details.' : 'No billing address yet. Add one so invoices can be issued to you.');
  $('billing-details-form').onsubmit = event => {
    event.preventDefault();
    const value = id => $(id).value.trim();
    const body = { address: { country_code: value('billing-country').toUpperCase(), postal_code: value('billing-postal'), region: value('billing-region'),
      city: value('billing-city'), first_line: value('billing-line1'), second_line: value('billing-line2') } };
    if (value('billing-business')) body.business = { name: value('billing-business'), tax_identifier: value('billing-tax-id') };
    $('billing-details-status').textContent = 'Saving billing details…';
    return billingDetails(() => api(`${base()}/billing-details`, { method: 'PUT', body: JSON.stringify(body) }), () => 'Billing details saved. New invoices use them.');
  };
  $('app').value = app;
  const base = () => `/v1/product-control/customer/${encodeURIComponent(app)}`;
  const scope = feature => [view.account_id, view.app_id, view.customer.tenant_id, feature];
  const current = generation => { if (generation !== epoch) throw new Error('Customer workspace changed.'); };
  const sessionLock = (task, selectedApp = app) => {
    if (!navigator.locks) throw new Error('Use a supported browser with site storage enabled to sign in and recover your requests.');
    return navigator.locks.request(`gregale.product.session:${selectedApp}`, task);
  };
  async function raw(path, options = {}, abortable = true) {
    return fetch(path, { ...options, credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal: abortable ? controller.signal : undefined,
      headers: { 'Content-Type': 'application/json', 'X-Product-Organization': organization, ...options.headers } });
  }
  async function problem(response) {
    const body = await response.json().catch(() => ({}));
    const error = new Error(body.detail || body.title || 'Request failed.'); error.code = body.code; error.status = response.status; return error;
  }
  async function refreshSession(generation) {
    if (!refreshing || refreshing.generation !== generation) {
      const entry = { generation }; refreshing = entry;
      entry.promise = sessionLock(async () => {
        current(generation);
        // A different tab may already have rotated the shared session cookie.
        if ((await raw(`${base()}/workspaces`)).ok) return;
        const response = await raw(`${base()}/session/refresh`, { method: 'POST' }, false);
        if (!response.ok) throw await problem(response);
      }).finally(() => { if (refreshing === entry) refreshing = undefined; });
    }
    await refreshing.promise; current(generation);
  }
  async function api(path, options = {}, blob = false, recoverSession = true) {
    const generation = epoch;
    let response = await raw(path, options); current(generation);
    if (response.status === 401 && recoverSession) { await refreshSession(generation); response = await raw(path, options); current(generation); }
    if (!response.ok) throw await problem(response);
    if (response.status === 204) return null;
    const body = await (blob ? response.blob() : response.json()); current(generation); return body;
  }
  function clear() {
    epoch++; planControls?.reset(); teamControls?.reset(); guidanceControls?.reset(); helpControls?.reset(); notificationControls?.reset(); activityControls?.reset(); historyControls?.reset(); emailControls?.reset(); signupControls?.reset(); paymentControls?.reset(); clearBilling(); clearBillingDetails(); clearTimeout(timer); controller.abort(); controller = new AbortController();
    runControls?.reset(); view = undefined; receipts = undefined; organization = ''; work.clear(); inputs.clear(); cards.clear(); $('sign-in').disabled = false;
    $('workspace').hidden = true; $('login').hidden = false;
    for (const id of ['features', 'usage', 'work', 'organization']) $(id).replaceChildren();
    $('result').hidden = true; $('result').textContent = ''; message('');
  }
  async function connect() {
    const generation = epoch, data = await api(`${base()}/workspaces`); current(generation);
    recoveryMaxBytes = data.receipt_max_bytes; helpTextMax = data.help_text_max_bytes; helpPageSize = data.help_page_size; notificationPageSize = data.notification_page_size; historyPageSize = data.work_history_page_size; historyCursorMax = data.work_history_cursor_max_bytes; activityPageSize = data.activity_page_size; activityCursorMax = data.activity_cursor_max_bytes;
    if (!data.workspaces.length) {
      // A product that offers signup lets the user create their first workspace (ADR-20261022100735).
      if (await signupControls.choose()) { current(generation); return; }
      throw new Error('Your account has no customer workspace for this product. Contact your product team.');
    }
    signupControls.reset();
    receipts = new Receipts({ storage: localStorage, locks: navigator.locks, crypto, maxBytes: data.receipt_max_bytes, maxEntries: data.receipt_max_entries, replaySeconds: data.replay_seconds });
    $('organization').replaceChildren();
    for (const workspace of data.workspaces) { const option = node('option', workspace.name); option.value = workspace.organization_id; $('organization').append(option); }
    const wanted = [signupControls.workspace(), paymentControls.workspace()].find(id => data.workspaces.some(workspace => workspace.organization_id === id));
    organization = wanted || data.workspaces[0].organization_id; $('organization').value = organization;
    await refresh(true, generation); await recover(generation); await planControls.connect(); await teamControls.connect(); await helpControls.connect(); await notificationControls.connect(); await activityControls.connect(); await emailControls.connect(); await paymentControls.connect(); schedule(generation);
  }
  async function refresh(forms = false, generation = epoch) {
    const request = ++refreshRequest;
    let data;
    try { data = await api(`${base()}/product`); current(generation); }
    catch (error) { if (generation === epoch && request === refreshRequest) guidanceControls?.unavailable(); throw error; }
    if (request !== refreshRequest) return;
    if (view && (view.account_id !== data.account_id || view.app_id !== data.app_id || view.customer.tenant_id !== data.customer.tenant_id)) {
      clear(); throw new Error('Your workspace assignment changed. Sign in again before continuing.');
    }
    if (view && (view.access?.role !== data.access?.role || JSON.stringify(view.access?.permissions) !== JSON.stringify(data.access?.permissions))) {
      clear(); message('Your product permissions changed. Sign in again to continue with your current access.'); throw new Error('Product permissions changed.');
    }
    view = data;
    for (const item of data.operations) work.set(item.id, { ...work.get(item.id), ...item });
    renderSummary(); if (forms) renderFeatures(); renderWork();
  }
  function renderSummary() {
    $('login').hidden = true; $('workspace').hidden = false;
    $('plan').textContent = view.catalog.spec.plans[view.customer.plan].label;
    $('account-status').textContent = view.customer.suspended ? 'New runs are paused. Contact your product team.' : 'Your workspace is active.';
    renderOverdueHold(); renderTrialConversion();
    updateFeatureTerms();
    $('features-section').hidden = !allowed('run_work'); $('work-section').hidden = !allowed('view_work');
    $('usage-section').hidden = !allowed('view_billing'); $('billing-section').hidden = !allowed('view_billing'); $('billing-details-section').hidden = !allowed('manage_billing');
    planControls?.sync(); teamControls?.sync();
    guidanceControls?.sync(); helpControls?.sync(); notificationControls?.sync(); activityControls?.sync(); historyControls?.sync(); emailControls?.sync(); paymentControls?.sync();
    const permissions = [['view_work', 'View work'], ['run_work', 'Run work'], ['view_billing', 'View billing'], ['manage_plan', 'Manage plan'], ['manage_team', 'Manage team'], ['manage_billing', 'Manage billing details']].filter(([key]) => allowed(key)).map(([, label]) => label);
    $('access-status').textContent = `${view.access?.role || 'Workspace role'} · ${permissions.join(' · ')}`;
    $('period').textContent = view.usage ? `${view.usage.period} · ${view.usage.currency}` : '';
    $('usage').replaceChildren();
    for (const line of view.usage?.lines || []) { const row = node('tr'); for (const value of [line.feature, line.starts, line.succeeded, amount(line.amount_millicents, view.usage.currency)]) row.append(node('td', String(value))); $('usage').append(row); }
    if (!view.usage?.lines.length) { const row = node('tr'), cell = node('td', 'No usage yet this month.'); cell.colSpan = 4; row.append(cell); $('usage').append(row); }
  }
  function inputForm(schema, remembered) {
    const form = node('form'), controls = [];
    const simple = schema?.type === 'object' && schema.properties && Object.values(schema.properties).every(p => ['string', 'integer', 'number', 'boolean'].includes(p.type) && !p.oneOf && !p.anyOf && !p.allOf && !p.$ref) && !schema.oneOf && !schema.anyOf && !schema.allOf && !schema.$ref;
    if (!simple) {
      const label = node('label', 'Request details (JSON)'), input = node('textarea'); input.value = remembered || '{}'; input.required = true; label.append(input); form.append(label);
      return { form, read: () => JSON.parse(input.value) };
    }
    let saved; try { saved = remembered ? JSON.parse(remembered) : {}; } catch { saved = {}; }
    for (const [name, property] of Object.entries(schema.properties)) {
      const label = node('label', property.title || name.replaceAll('_', ' ')), input = node(property.enum ? 'select' : 'input');
      const required = schema.required?.includes(name) || false;
      if (property.enum) { if (!required) { const blank = node('option', 'Choose…'); blank.value = ''; input.append(blank); } for (const value of property.enum) { const option = node('option', String(value)); option.value = String(value); input.append(option); } }
      else { input.type = property.type === 'boolean' ? 'checkbox' : property.type === 'string' ? 'text' : 'number'; if (property.minimum !== undefined) input.min = property.minimum; if (property.maximum !== undefined) input.max = property.maximum; input.step = property.type === 'integer' ? '1' : 'any'; }
      input.required = property.type !== 'boolean' && required; input.autocomplete = 'off';
      if (property.type === 'boolean' && !property.enum) input.checked = saved[name] ?? property.default ?? false; else input.value = saved[name] ?? property.default ?? '';
      label.append(input); form.append(label); controls.push({ name, property, input, required });
    }
    return { form, read: () => {
      const value = Object.create(null);
      for (const { name, property, input, required } of controls) {
        if (!required && input.type !== 'checkbox' && input.value === '') continue;
        value[name] = property.type === 'boolean' ? (property.enum ? input.value === 'true' : input.checked) : ['integer', 'number'].includes(property.type) ? Number(input.value) : input.value;
      }
      return value;
    } };
  }
  // An overdue invoice pauses new runs until it is paid (ADR-20261018212918); this is separate from the product team pausing the workspace.
  function renderOverdueHold() {
    const hold = view.overdue_hold, status = $('overdue-status');
    status.replaceChildren(); status.hidden = !hold;
    if (!hold) return;
    status.append(node('span', `New runs are paused until your invoice due ${String(hold.due_at).slice(0, 10)} is paid. Work already running continues.`));
    let link = null;
    try { link = new URL(hold.hosted_invoice_url); } catch { link = null; }
    if (link?.protocol === 'https:') { const a = node('a', 'View and pay invoice'); a.href = link.href; a.target = '_blank'; a.rel = 'noopener noreferrer'; status.append(a); }
  }
  // A plan that requires a card keeps the workspace after its trial only with
  // one on file; otherwise the plan's fallback follows. Between the trial's
  // end and that move, new runs wait for a card (ADR-20261022180921).
  function awaitingCard() {
    const plan = view.catalog.spec.plans[view.customer.plan], ends = Date.parse(view.customer.trial_ends_at || '');
    return Boolean(plan?.requires_payment_method) && Number.isFinite(ends) && ends <= Date.now() && !view.customer.trial_resolved_at && !view.customer.payment_method_at;
  }
  function renderTrialConversion() {
    const customer = view.customer, plan = view.catalog.spec.plans[customer.plan], status = $('trial-status'), ending = trialUntil(), held = awaitingCard();
    status.replaceChildren(); status.hidden = !plan?.requires_payment_method || Boolean(customer.payment_method_at) || !ending && !held;
    if (status.hidden) return;
    const fallback = view.catalog.spec.plans[plan.fallback_plan]?.label || 'free';
    status.append(node('span', held ? `Your trial ended ${customer.trial_ends_at.slice(0, 10)}. New runs wait for a card on file; without one, your workspace moves to the ${fallback} plan shortly. `
      : `Your trial ends ${ending}. Add a card to keep the ${plan.label} plan; without one, your workspace moves to the ${fallback} plan. `));
    if (!allowed('manage_billing')) { status.append(node('span', 'Ask a workspace member who manages billing to add one.')); return; }
    if ($('payment-section').hidden) { status.append(node('span', 'Contact your product team to set up payment.')); return; }
    const link = node('a', 'Add a card'); link.href = '#payment-title'; status.append(link);
  }
  // Included starts come first at no charge; later ones cost the unit price
  // up to the monthly limit (ADR-20261021154511).
  function includedLeft(name, grant, remaining) {
    if (Number.isSafeInteger(view.included_remaining?.[name])) return view.included_remaining[name];
    const included = Number.isSafeInteger(grant.included_starts) ? grant.included_starts : 0;
    return Math.max(0, included - (grant.monthly_starts - remaining));
  }
  // During a free trial every start is free (ADR-20261021195901).
  function trialUntil() {
    const starts = Date.parse(view.customer.trial_starts_at || ''), ends = Date.parse(view.customer.trial_ends_at || ''), now = Date.now();
    return Number.isFinite(starts) && Number.isFinite(ends) && starts <= now && now < ends ? view.customer.trial_ends_at.slice(0, 10) : '';
  }
  function featureTerms(name, grant, remaining) {
    const price = `${amount(grant.unit_price_millicents, view.catalog.spec.currency)} per successful run`;
    const starts = `${remaining} of ${grant.monthly_starts} starts remaining this month.`;
    const trial = trialUntil();
    if (trial) return `Free during your trial until ${trial}, then ${price} · ${starts}`;
    if (!(grant.included_starts > 0)) return `${price} · ${starts}`;
    return `${includedLeft(name, grant, remaining)} of ${grant.included_starts} included runs left, then ${price} · ${starts}`;
  }
  function updateFeatureTerms() {
    const plan = view.catalog.spec.plans[view.customer.plan];
    for (const [name, card] of cards) {
      const grant = plan.features[name];
      if (!grant) { card.button.disabled = true; card.info.textContent = 'This feature is no longer included in your plan.'; continue; }
      const remaining = Number.isSafeInteger(view.remaining_starts?.[name]) ? view.remaining_starts[name] : 0;
      card.info.textContent = featureTerms(name, grant, remaining);
      card.button.disabled = !allowed('run_work') || card.busy || !card.pending && (view.customer.suspended || Boolean(view.overdue_hold) || awaitingCard() || view.bindings[name]?.available !== true || remaining === 0);
    }
  }
  function renderFeatures() {
    $('features').replaceChildren(); cards.clear();
    if (!allowed('run_work')) return;
    const plan = view.catalog.spec.plans[view.customer.plan];
    for (const [name, feature] of Object.entries(view.catalog.spec.features)) {
      const grant = plan.features[name], binding = view.bindings[name], card = node('div'); card.className = 'feature';
      const remaining = Number.isSafeInteger(view.remaining_starts?.[name]) ? view.remaining_starts[name] : 0;
      let saved; try { saved = receipts.load(scope(name)); } catch (error) { message(error.message); }
      const pending = saved && !saved.operationID, input = inputForm(binding?.input_schema, inputs.get(name));
      const button = node('button', pending ? 'Check / retry saved request' : 'Run feature'); button.type = 'submit';
      button.disabled = !pending && (view.customer.suspended || Boolean(view.overdue_hold) || awaitingCard() || binding?.available !== true || remaining === 0);
      const info = node('p', featureTerms(name, grant, remaining));
      const cardState = { info, button, pending, busy: false }; cards.set(name, cardState); card.append(node('h3', feature.label), info);
      if (pending) card.append(node('p', 'A saved submission needs confirmation. Enter the original details if it has not been accepted.'));
      if (binding?.available !== true) card.append(node('p', 'This feature is temporarily unavailable while its deployment is being prepared.'));
      input.form.append(button); card.append(input.form);
      const generation = epoch;
      input.form.onsubmit = event => { event.preventDefault(); if (!input.form.reportValidity()) return; let value; try { value = input.read(); } catch (error) { message(error.message); return; } cardState.busy = true; button.disabled = true; inputs.set(name, encodeInput(value)); return start(name, value, saved?.key).catch(error => { if (generation === epoch) message(error.message); }).finally(() => { if (generation === epoch && view) renderFeatures(); }); };
      const help = node('button', 'Get help with this feature'); help.type = 'button'; help.className = 'secondary'; help.onclick = () => { if (generation === epoch) helpControls.open('feature', name); }; card.append(help);
      $('features').append(card);
    }
  }
  async function start(name, input, observedKey) {
    if (!allowed('run_work')) throw new Error('Your role does not allow running work.');
    const generation = epoch, selectedScope = scope(name), store = receipts;
    await store.lock(selectedScope, async () => {
      current(generation);
      const saved = store.load(selectedScope);
      if (saved?.operationID && saved.key !== observedKey) { await follow(saved.operationID); message('This request was already accepted in another tab.'); return; }
      if (saved && !saved.operationID) {
        const observation = await lookup(name, saved); current(generation);
        if (observation.state === 'accepted') { store.acknowledge(selectedScope, saved, observation.receipt.id); await follow(observation.receipt.id); message('Your original request was already accepted.'); return; }
        if (observation.state === 'expired') throw new Error('The saved submission has expired. Contact your product team before starting again.');
      }
      const grant = view.catalog.spec.plans[view.customer.plan].features[name];
      const remaining = Number.isSafeInteger(view.remaining_starts?.[name]) ? view.remaining_starts[name] : 0;
      const charge = trialUntil() ? 'It is free during your trial' : includedLeft(name, grant, remaining) > 0 ? 'It is included in your plan at no charge' : `${amount(grant.unit_price_millicents, view.catalog.spec.currency)} will be recorded only if it succeeds`;
      if (!window.confirm(`Run ${view.catalog.spec.features[name].label}?\n${charge}. This consumes one monthly start even if it fails or is cancelled.`)) return;
      current(generation);
      // Persist before POST. A failed save must prevent submission.
      const receipt = await store.prepare(selectedScope, input, view.quotes[name]); current(generation);
      try {
        const accepted = await api(`${base()}/start`, { method: 'POST', headers: { 'Idempotency-Key': receipt.key }, body: JSON.stringify({ feature: name, input, expected_quote: receipt.quote }) }); current(generation);
        store.acknowledge(selectedScope, receipt, accepted.id); inputs.delete(name); await follow(accepted.id); await refresh(false, generation); message('Work accepted. Progress updates automatically.');
      } catch (error) {
        current(generation);
        if (error.code === 'product_quote_changed') {
          await refresh(false, generation); current(generation);
          // No admission occurred. Keep the key and input fingerprint, but
          // require a new click and visible price confirmation before retry.
          store.save(selectedScope, { ...receipt, quote: view.quotes[name] });
          message('Your price or allowance terms changed. Review the updated terms and confirm again to run.'); return;
        }
        throw new Error(`${error.message}\nYour request is saved. Check or retry this request; contact your product team before replacing uncertain work.`);
      }
    });
  }
  const lookup = (feature, receipt) => api(`${base()}/submissions/lookup`, { method: 'POST', body: JSON.stringify({ feature, idempotency_key: receipt.key }) });
  async function recover(generation) {
    if (!allowed('view_work')) return;
    const store = receipts;
    for (const saved of store.list([view.account_id, view.app_id, view.customer.tenant_id])) {
      const name = saved.scope[3], selectedScope = saved.scope;
      await store.lock(selectedScope, async () => {
        current(generation); const receipt = store.load(selectedScope); if (!receipt) return;
        if (receipt.operationID) { await follow(receipt.operationID); return; }
        const observation = await lookup(name, receipt); current(generation);
        if (observation.state === 'accepted') { store.acknowledge(selectedScope, receipt, observation.receipt.id); await follow(observation.receipt.id); }
        else if (!allowed('run_work')) message('A saved request is unresolved. Your current role cannot retry it. Contact your product team; work has not been restarted.');
        else message(observation.state === 'expired' || Date.now() >= receipt.replayNotAfter ? 'A saved submission needs your product team to resolve it. Work has not been restarted.' : 'A saved submission is unresolved. Re-enter its original details to check or retry it. Work has not been restarted.');
      });
    }
    current(generation); renderFeatures();
  }
  async function follow(id) {
    if (!allowed('view_work')) throw new Error('Your role does not include work access.');
    try { const item = await api(`${base()}/operations/${encodeURIComponent(id)}`); work.set(id, item); renderWork(); return item; }
    catch (error) { if (error.status === 404 || error.status === 410) { work.set(id, { id, name: 'Saved run', state: 'expired' }); renderWork(); return; } throw error; }
  }
  function renderWork() {
    $('work').replaceChildren();
    if (!allowed('view_work')) return;
    if (!work.size) $('work').append(node('li', 'No recent work.'));
    for (const item of work.values()) {
      const progress = item.progress ? ` · ${item.progress.stage}: ${item.progress.completed}/${item.progress.total}` : '';
      const label = { accepted: 'Queued', running: 'Running', succeeded: 'Completed', failed: 'Failed', cancelled: 'Cancelled', requires_reconciliation: 'Needs review', expired: 'Details expired' }[item.state] || item.state;
      const row = node('li', `${item.name}: ${label}${progress} · Reference ${item.id} `);
      const details = node('button', 'Run details'); details.type = 'button'; details.className = 'secondary'; const detailsGeneration = epoch; details.onclick = () => { if (detailsGeneration === epoch) return runControls.show(item.id); }; row.append(details);
      if (allowed('run_work') && !terminal(item.state)) { const button = node('button', item.cancellation_requested ? 'Cancellation requested' : 'Cancel'); button.className = 'secondary'; button.disabled = item.cancellation_requested; button.onclick = () => { const generation = epoch; return cancel(item.id).catch(error => { if (generation === epoch) message(error.message); }); }; row.append(button); }
      if (item.state === 'succeeded') { const button = node('button', 'View result and files'); button.className = 'secondary'; button.onclick = () => { const generation = epoch; return showResult(item.id).catch(error => { if (generation === epoch) message(error.message); }); }; row.append(button); }
      if (item.state === 'requires_reconciliation') row.append(node('p', 'The outcome needs reconciliation. Contact your product team; do not run a replacement.'));
      const help = node('button', 'Get help with this run'); help.type = 'button'; help.className = 'secondary'; const helpGeneration = epoch; help.onclick = () => { if (helpGeneration === epoch) helpControls.open('run', item.id); }; row.append(help);
      $('work').append(row);
    }
  }
  async function showResult(id, check = () => {}) {
    if (runControls?.selected && runControls.selected !== id) runControls.reset();
    clearResult();
    const generation = epoch, request = resultRequest;
    let item;
    try { item = await follow(id); current(generation); }
    catch (error) { if (generation !== epoch || request !== resultRequest) return; throw error; }
    if (request !== resultRequest) return;
    const selected = () => { current(generation); check(); if (request !== resultRequest) throw new Error('The selected run result changed.'); };
    selected();
    if (!item || item.state !== 'succeeded') return;
    $('result').hidden = false; $('result').replaceChildren(node('code', JSON.stringify(item.result, null, 2)));
    for (const artifact of item.artifacts || []) { const button = node('button', `Download ${artifact.name}`); button.onclick = async () => { try { selected(); await download(id, artifact, selected); } catch (error) { if (generation === epoch && request === resultRequest) message(error.message); } }; $('result').append(button); }
  }
  async function download(id, artifact, check = () => {}) {
    if (!allowed('view_work')) throw new Error('Your role does not include work access.');
    const generation = epoch;
    const blob = await api(`${base()}/operations/${encodeURIComponent(id)}/artifacts/${encodeURIComponent(artifact.id)}`, {}, true); current(generation); check();
    const url = URL.createObjectURL(blob), link = node('a'); link.href = url; link.download = artifact.name.replace(/[^a-zA-Z0-9._-]/g, '_');
    link.click(); setTimeout(() => URL.revokeObjectURL(url), 30000);
  }
  async function cancel(id) { if (!allowed('run_work')) throw new Error('Your role does not allow cancelling work.'); const generation = epoch, item = await follow(id); current(generation); if (!item || terminal(item.state) || !window.confirm('Request cancellation? The monthly start is still consumed.')) return; const cancelled = await api(`${base()}/operations/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: JSON.stringify({ expected_generation: item.generation }) }); current(generation); work.set(id, cancelled); renderWork(); }
  function schedule(generation) {
    clearTimeout(timer); if (generation !== epoch || !view) return;
    timer = setTimeout(async () => {
      try {
        await refresh(false, generation); await helpControls?.poll(); await notificationControls?.poll();
        // Retained receipts can outlive the current feature's history binding.
        for (const item of work.values()) if (allowed('view_work') && !terminal(item.state) && !view.operations.some(recent => recent.id === item.id)) await follow(item.id);
        current(generation); $('live-status').textContent = 'Progress updates automatically.';
      } catch (error) {
        if (generation !== epoch) return;
        if (error.status === 401 || error.status === 403) { clear(); message('Your customer session or workspace access ended. Sign in again.'); return; }
        $('live-status').textContent = 'Connection interrupted. Reconnecting without restarting work.';
      }
      schedule(generation);
    }, 4000);
  }
  planControls = new GregaleProductPlans({ $, api, base, view: () => view, epoch: () => epoch, current, amount, message, storage: localStorage, locks: navigator.locks, crypto, maxBytes: () => recoveryMaxBytes, confirm: text => window.confirm(text), changed: () => refresh(true) });
  teamControls = new GregaleProductTeam({ $, api, base, view: () => view, epoch: () => epoch, current, message, storage: localStorage, locks: navigator.locks, crypto, maxBytes: () => recoveryMaxBytes, confirm: text => window.confirm(text) });
  guidanceControls = new GregaleProductGuidance({ $, view: () => view, reviewPlans: () => planControls.action(() => planControls.load()), help: feature => helpControls.open('feature', feature) });
  helpControls = new GregaleProductHelp({ $, api, base, view: () => view, epoch: () => epoch, current, storage: localStorage, locks: navigator.locks, crypto, organization: () => organization, maxBytes: () => recoveryMaxBytes, textMax: () => helpTextMax, pageSize: () => helpPageSize, denied: () => { clear(); message('Your workspace access changed. Sign in again to continue.'); } });
  runControls = new GregaleProductRun({ $, api, base, view: () => view, epoch: () => epoch, current, amount,
    clearResult, openResult: showResult, openHelp: id => helpControls.open('run', id),
    openBilling: async (period, check) => { check(); $('billing-period').value = period; await loadBilling(false, check); check(); $('billing-section').scrollIntoView?.({ block: 'start' }); },
    denied: () => { clear(); message('Your workspace access changed. Sign in again to continue.'); } });
  historyControls = new GregaleProductHistory({ $, api, base, view: () => view, epoch: () => epoch, current, pageSize: () => historyPageSize, cursorMax: () => historyCursorMax, resetRun: () => runControls?.reset(), openRun: id => runControls.show(id),
    denied: () => { clear(); message('Your workspace access changed. Sign in again to continue.'); } });
  notificationControls = new GregaleProductNotifications({ $, api: (path, options) => api(path, options, false, options?.method !== 'POST'), base, view: () => view, epoch: () => epoch, current, pageSize: () => notificationPageSize, openWork: async id => { await showResult(id); $('work-section').scrollIntoView?.({ block: 'start' }); }, openHelp: id => helpControls.show(id), denied: () => { clear(); message('Your workspace access changed. Sign in again to continue.'); } });
  activityControls = new GregaleProductActivity({ $, api, base, view: () => view, epoch: () => epoch, current, pageSize: () => activityPageSize, cursorMax: () => activityCursorMax, amount,
    openWork: id => runControls.show(id),
    openPlan: id => planControls.show(id), openHelp: id => helpControls.show(id),
    openBilling: async period => { $('billing-period').value = period; await loadBilling(); $('billing-section').scrollIntoView?.({ block: 'start' }); },
    denied: () => { clear(); message('Your workspace access changed. Sign in again to continue.'); } });
  emailControls = new GregaleProductEmail({ $, api: (path, options) => api(path, options, false, options?.method !== 'PUT'), base, view: () => view, epoch: () => epoch, current });
  signupControls = new GregaleProductSignup({ $, raw, problem, api, base, app: () => app, epoch: () => epoch, current, amount, message, storage: localStorage, crypto, confirm: text => window.confirm(text), reconnect: () => connect() });
  paymentControls = new GregaleProductPayment({ $, api, base, view: () => view, allowed, organization: () => organization, epoch: () => epoch, current,
    href: () => location.href, replaceURL: url => history.replaceState(null, '', url), navigate: url => location.assign(url),
    // Reading or saving the card records it on the workspace's plan; refresh
    // the plan when that changed what it shows.
    rendered: async onFile => { if (!view) return; if (onFile === Boolean(view.customer.payment_method_at)) renderTrialConversion(); else await refresh(); } });
  const channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel('gregale.product.session') : null;
  if (channel) channel.onmessage = event => { if (event.data?.app === app) { clear(); message('Your customer session changed in another tab. Sign in to continue.'); } };
  const updateRecoveryLink = () => {
    const selectedApp = $('app').value.trim();
    const valid = /^[a-z][a-z0-9-]{0,62}$/.test(selectedApp);
    $('account-recovery').href = valid ? `/v1/product-control/customer/${encodeURIComponent(selectedApp)}/account-recovery` : '#';
    $('account-recovery').ariaDisabled = String(!valid);
    return valid;
  };
  $('app').oninput = updateRecoveryLink;
  updateRecoveryLink();
  $('account-recovery').onclick = event => {
    if (!updateRecoveryLink()) { event.preventDefault(); message('Enter your product name to recover your password.'); return; }
    $('password').value = '';
    message('Password recovery opens in a new tab. After resetting your password, return here to sign in.');
  };
  $('connect').onsubmit = async event => {
    event.preventDefault(); const selectedApp = $('app').value.trim(), email = $('email').value, password = $('password').value;
    if (!/^[a-z][a-z0-9-]{0,62}$/.test(selectedApp)) { message('Enter your product name.'); return; }
    clear(); app = selectedApp; const generation = epoch; $('sign-in').disabled = true; $('password').value = '';
    try {
      await sessionLock(async () => { current(generation); const response = await raw(`${base()}/session`, { method: 'POST', body: JSON.stringify({ email, password }) }, false); if (!response.ok) throw await problem(response); });
      current(generation); channel?.postMessage({ app }); const url = new URL(location.href); url.searchParams.set('app', app); history.replaceState(null, '', url); await connect();
    } catch (error) { if (generation === epoch) message(error.message); }
    finally { if (generation === epoch) $('sign-in').disabled = false; }
  };
  $('refresh').onclick = () => { const generation = epoch; return refresh(true).catch(error => { if (generation === epoch) message(error.message); }); };
  $('organization').onchange = async () => {
    const selected = $('organization').value; epoch++; planControls?.reset(); teamControls?.reset(); guidanceControls?.reset(); helpControls?.reset(); notificationControls?.reset(); activityControls?.reset(); historyControls?.reset(); runControls?.reset(); emailControls?.reset(); paymentControls?.reset(); clearBilling(); clearBillingDetails(); clearTimeout(timer); controller.abort(); controller = new AbortController(); organization = selected; inputs.clear(); work.clear(); cards.clear(); view = undefined; $('result').hidden = true; $('result').textContent = '';
    const generation = epoch; try { await refresh(true, generation); await recover(generation); await planControls.connect(); await teamControls.connect(); await helpControls.connect(); await notificationControls.connect(); await activityControls.connect(); await emailControls.connect(); await paymentControls.connect(); schedule(generation); } catch (error) { if (generation === epoch) { clear(); message(error.message); } }
  };
  $('disconnect').onclick = async () => {
    const sessionBase = base(), sessionApp = app; clear(); const generation = epoch;
    try { await sessionLock(async () => { const response = await raw(`${sessionBase}/session`, { method: 'DELETE' }, false); if (!response.ok) throw await problem(response); }, sessionApp); channel?.postMessage({ app: sessionApp }); current(generation); message('Signed out. Saved request recovery details remain available after you sign back in.'); }
    catch (error) { if (generation === epoch) message(`The local workspace is closed. Sign-out could not be confirmed: ${error.message}`); }
  };
  if (app) { const generation = epoch; connect().catch(error => { if (generation === epoch) { clear(); message(error.status === 401 ? 'Sign in to continue.' : error.message); } }); }
})();
