// Consumer upgrade page (ADR-20261124094745). Static: everything it shows
// comes from the same-origin JSON API and is written with textContent. The
// link's token proves who the customer is; it is never stored.
"use strict";

(function () {
  const parts = window.location.pathname.split("/");
  const slug = decodeURIComponent(parts[3] || "");
  const base = "/v1/consumer-signup/" + encodeURIComponent(slug) + "/upgrade";
  const params = new URLSearchParams(window.location.search);
  const token = params.get("token") || "";
  const $ = (id) => document.getElementById(id);
  let current = "";
  let hasCard = false;
  let freePlans = {};

  function show(id) {
    for (const view of ["form-view", "message-view"]) {
      $(view).hidden = view !== id;
    }
  }

  function message(title, text) {
    $("title").textContent = title;
    $("message").textContent = text;
    show("message-view");
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

  function priceLines(plan) {
    const lines = [];
    for (const p of plan.prices || []) {
      const unit = p.unit.indexOf("app:") === 0 ? p.unit.slice(4) : p.unit;
      let line = p.tiered ? "Tiered pricing per " + unit.replace(/_/g, " ") : money(p.currency, p.price_millicents_per_unit) + " per " + unit.replace(/_/g, " ");
      if (p.included_units_per_month) line += ", " + p.included_units_per_month.toLocaleString() + " included each month";
      lines.push(line);
      if (p.base_fee_millicents_per_month) lines.push(money(p.currency, p.base_fee_millicents_per_month) + " per month");
      if (p.minimum_millicents_per_month) lines.push("Minimum " + money(p.currency, p.minimum_millicents_per_month) + " per month");
    }
    if (plan.max_units_per_month) lines.push("Up to " + plan.max_units_per_month.toLocaleString() + " units a month");
    if (plan.max_requests_per_minute) lines.push("Up to " + plan.max_requests_per_minute.toLocaleString() + " requests a minute");
    if (plan.free) lines.unshift("Free");
    return lines;
  }

  function selected() {
    const plan = document.querySelector('input[name="plan"]:checked');
    return plan ? plan.value : "";
  }

  function updateNote() {
    const plan = selected();
    $("card-note").hidden = hasCard || !plan || freePlans[plan];
    $("submit").disabled = !plan || plan === current;
  }

  function render(view) {
    document.title = "Change your " + view.display_name + " plan";
    $("title").textContent = "Change your " + view.display_name + " plan";
    $("testmode").hidden = !!view.livemode;
    current = view.current_plan_id || "";
    hasCard = !!view.has_card;
    const fieldset = $("plans");
    for (const plan of view.plans) {
      if (!plan.id) continue;
      freePlans[plan.id] = !!plan.free;
      const label = document.createElement("label");
      label.className = "plan";
      const input = document.createElement("input");
      input.type = "radio";
      input.name = "plan";
      input.value = plan.id;
      input.checked = plan.id === current;
      input.addEventListener("change", updateNote);
      const body = document.createElement("span");
      const name = document.createElement("strong");
      name.textContent = plan.name + (plan.id === current ? " (current)" : "");
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
    }
    updateNote();
    show("form-view");
  }

  function changed(result) {
    window.history.replaceState(null, "", window.location.pathname);
    const when = result.effective_from ? " from " + new Date(result.effective_from).toLocaleTimeString() : "";
    message("Plan changed", "You are on the new plan" + when + ". You can close this page and keep using the API.");
  }

  async function submit(event) {
    event.preventDefault();
    const error = $("form-error");
    error.hidden = true;
    $("submit").disabled = true;
    const res = await call("POST", "", { token: token, plan_id: selected() });
    if (res.status !== 200) {
      $("submit").disabled = false;
      error.textContent = res.data.detail || "The plan could not be changed. Try again.";
      error.hidden = false;
      return;
    }
    if (res.data.checkout_url) {
      window.location.assign(res.data.checkout_url);
      return;
    }
    changed(res.data);
  }

  async function complete(completeToken, session) {
    message("Finishing your upgrade…", "Checking your card with Stripe…");
    for (let attempt = 0; attempt < 5; attempt++) {
      const res = await call("POST", "/complete", { token: token, complete_token: completeToken, checkout_session_id: session });
      if (res.status === 200) {
        changed(res.data);
        return;
      }
      // Only an unconfirmed checkout is worth waiting for.
      if (res.data.code !== "consumer_signup_checkout_incomplete") {
        message("Upgrade not finished", res.data.detail || "The upgrade could not be completed.");
        return;
      }
      await new Promise(function (resolve) { setTimeout(resolve, 1500 * (attempt + 1)); });
    }
    message("Upgrade not finished", "Stripe has not confirmed your card yet. Reload this page in a minute.");
  }

  async function start() {
    if (!token) {
      message("Link incomplete", "Open the full upgrade link from the API's error message.");
      return;
    }
    $("upgrade-form").addEventListener("submit", submit);
    if (params.get("complete") && params.get("session")) {
      await complete(params.get("complete"), params.get("session"));
      return;
    }
    const res = await call("GET", "/plans?token=" + encodeURIComponent(token));
    if (res.status !== 200) {
      message("Link unavailable", res.data.detail || "This upgrade link cannot be used.");
      return;
    }
    if (params.get("canceled")) {
      $("form-error").textContent = "The card was not saved. Choose a plan to try again.";
      $("form-error").hidden = false;
    }
    render(res.data);
  }

  start();
})();
