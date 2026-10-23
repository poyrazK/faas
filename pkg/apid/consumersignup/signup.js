// Consumer sign-up page (ADR-20261021143000). Static: everything it shows
// comes from the same-origin JSON API and is written with textContent.
"use strict";

(function () {
  const parts = window.location.pathname.split("/");
  const slug = decodeURIComponent(parts[3] || "");
  const base = "/v1/consumer-signup/" + encodeURIComponent(slug);
  const params = new URLSearchParams(window.location.search);
  const $ = (id) => document.getElementById(id);

  function show(id) {
    for (const view of ["form-view", "key-view", "message-view"]) {
      $(view).hidden = view !== id;
    }
  }

  function message(text, restart) {
    $("message").textContent = text;
    $("restart").hidden = !restart;
    show("message-view");
  }

  function claimKey(id) {
    return "gregale-signup:" + slug + ":" + id;
  }

  function storage() {
    try {
      return window.sessionStorage;
    } catch (e) {
      return null;
    }
  }

  async function call(method, path, body) {
    const res = await fetch(base + path, {
      method: method,
      headers: body ? { "Content-Type": "application/json" } : {},
      body: body ? JSON.stringify(body) : undefined,
      credentials: "omit",
      cache: "no-store",
    });
    let data = null;
    try {
      data = await res.json();
    } catch (e) {
      data = null;
    }
    return { status: res.status, data: data || {} };
  }

  function money(currency, millicents) {
    const amount = millicents / 100000;
    try {
      return new Intl.NumberFormat(undefined, { style: "currency", currency: currency, maximumFractionDigits: 6 }).format(amount);
    } catch (e) {
      return currency + " " + amount;
    }
  }

  function unitName(unit) {
    const names = { request: "request", compute_second: "compute second", response_megabyte: "response MB" };
    if (names[unit]) return names[unit];
    if (unit.indexOf("app:") === 0) return unit.slice(4).replace(/_/g, " ");
    return unit.replace(/_/g, " ");
  }

  function priceLines(plan) {
    const lines = [];
    for (const p of plan.prices || []) {
      let line = p.tiered ? "Tiered pricing per " + unitName(p.unit) : money(p.currency, p.price_millicents_per_unit) + " per " + unitName(p.unit);
      if (p.included_units_per_month) line += ", " + p.included_units_per_month.toLocaleString() + " included each month";
      lines.push(line);
      if (p.base_fee_millicents_per_month) lines.push(money(p.currency, p.base_fee_millicents_per_month) + " per month");
      if (p.minimum_millicents_per_month) lines.push("Minimum " + money(p.currency, p.minimum_millicents_per_month) + " per month");
    }
    if (plan.max_units_per_month) lines.push("Up to " + plan.max_units_per_month.toLocaleString() + " units a month");
    if (plan.max_requests_per_minute) lines.push("Up to " + plan.max_requests_per_minute.toLocaleString() + " requests a minute");
    if (!lines.length) lines.push("Usage-based pricing");
    return lines;
  }

  function renderForm(form) {
    document.title = "Sign up for " + form.display_name;
    $("title").textContent = "Sign up for " + form.display_name;
    $("testmode").hidden = !!form.livemode;
    const fieldset = $("plans");
    form.plans.forEach(function (plan, i) {
      const label = document.createElement("label");
      label.className = "plan";
      const input = document.createElement("input");
      input.type = "radio";
      input.name = "plan";
      input.value = plan.id || "";
      input.checked = i === 0;
      const body = document.createElement("span");
      const name = document.createElement("strong");
      name.textContent = plan.name;
      body.appendChild(name);
      for (const line of priceLines(plan)) {
        const detail = document.createElement("span");
        detail.className = "muted";
        detail.textContent = line;
        body.appendChild(detail);
      }
      label.appendChild(input);
      label.appendChild(body);
      fieldset.appendChild(label);
    });
    if (form.trial_days) {
      $("trial").textContent = "Your first " + form.trial_days + " days are free.";
      $("trial").hidden = false;
    }
    if (form.terms_url) {
      $("terms-link").href = form.terms_url;
      $("terms-row").hidden = false;
    }
    show("form-view");
  }

  async function submit(event) {
    event.preventDefault();
    const error = $("form-error");
    error.hidden = true;
    if (!$("terms-row").hidden && !$("terms").checked) {
      error.textContent = "Please accept the terms.";
      error.hidden = false;
      return;
    }
    const plan = document.querySelector('input[name="plan"]:checked');
    $("submit").disabled = true;
    const res = await call("POST", "", { email: $("email").value.trim(), name: $("name").value.trim(), plan_id: plan ? plan.value : "" });
    if (res.status !== 201) {
      $("submit").disabled = false;
      error.textContent = res.data.detail || "Sign-up failed. Try again.";
      error.hidden = false;
      return;
    }
    const store = storage();
    if (store) store.setItem(claimKey(res.data.id), res.data.claim_token);
    window.location.assign(res.data.checkout_url);
  }

  async function complete(id) {
    const store = storage();
    const token = store && store.getItem(claimKey(id));
    if (!token) {
      message("This sign-up was started in another browser tab or window. Finish it there, or start again.", true);
      return;
    }
    $("title").textContent = "Finishing your sign-up…";
    show("message-view");
    $("message").textContent = "Checking your card with Stripe…";
    for (let attempt = 0; attempt < 5; attempt++) {
      const res = await call("POST", "/signups/" + encodeURIComponent(id) + "/complete", { claim_token: token });
      if (res.status === 200) {
        store.removeItem(claimKey(id));
        window.history.replaceState(null, "", window.location.pathname);
        showKey(res.data);
        return;
      }
      if (res.status !== 409) {
        message(res.data.detail || "The sign-up could not be completed.", true);
        return;
      }
      await new Promise(function (resolve) { setTimeout(resolve, 1500 * (attempt + 1)); });
    }
    message("Stripe has not confirmed your card yet. Reload this page in a minute.", false);
  }

  function showKey(done) {
    $("title").textContent = "Welcome";
    $("key").textContent = done.key;
    if (done.trial_ends_at) {
      $("key-trial").textContent = "Your free trial ends " + new Date(done.trial_ends_at).toLocaleDateString() + ".";
      $("key-trial").hidden = false;
    }
    if (done.return_url) {
      $("return-link").href = done.return_url;
      $("return-row").hidden = false;
    }
    show("key-view");
  }

  async function copyKey() {
    try {
      await navigator.clipboard.writeText($("key").textContent);
      $("copy").textContent = "Copied";
    } catch (e) {
      const range = document.createRange();
      range.selectNodeContents($("key"));
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    }
  }

  async function start() {
    $("signup-form").addEventListener("submit", submit);
    $("copy").addEventListener("click", copyKey);
    $("restart").addEventListener("click", function () {
      window.location.assign(window.location.pathname);
    });
    const res = await call("GET", "");
    if (res.status !== 200) {
      $("title").textContent = "Sign-up unavailable";
      message(res.data.detail || "This API does not take sign-ups.", false);
      return;
    }
    const id = params.get("signup");
    if (id && params.get("canceled")) {
      const store = storage();
      if (store) store.removeItem(claimKey(id));
      window.history.replaceState(null, "", window.location.pathname);
      renderForm(res.data);
      $("form-error").textContent = "Checkout was canceled. No card was saved.";
      $("form-error").hidden = false;
      return;
    }
    if (id) {
      $("title").textContent = res.data.display_name;
      await complete(id);
      return;
    }
    renderForm(res.data);
  }

  document.addEventListener("DOMContentLoaded", start);
})();
