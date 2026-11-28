// MCP server listing page (ADR-20261126175800). Static: everything it shows
// comes from the same-origin JSON API and is written with textContent, so a
// seller's title, summary or tool text can never become markup.
"use strict";

(function () {
  const parts = window.location.pathname.split("/");
  const slug = decodeURIComponent(parts[3] || "");
  const $ = (id) => document.getElementById(id);

  function message(title, text) {
    $("title").textContent = title;
    $("message").textContent = text;
    $("listing-view").hidden = true;
    $("message-view").hidden = false;
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
    if (plan.free) lines.push("Free");
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

  function item(list, head, headTag, lines) {
    const li = document.createElement("li");
    const h = document.createElement(headTag);
    h.textContent = head;
    li.appendChild(h);
    for (const line of lines) {
      if (!line) continue;
      const span = document.createElement("span");
      span.textContent = line;
      li.appendChild(span);
    }
    list.appendChild(li);
  }

  // Only https links from the API become hrefs.
  function link(el, url) {
    if (typeof url === "string" && url.indexOf("https://") === 0) {
      el.href = url;
      el.hidden = false;
    } else {
      el.hidden = true;
    }
  }

  function render(l) {
    document.title = l.title;
    $("title").textContent = l.title;
    $("summary").textContent = l.summary || "";
    if (typeof l.customers === "number") {
      $("customers").textContent = l.customers.toLocaleString() + (l.customers === 1 ? " customer" : " customers");
      $("customers").hidden = false;
    }
    $("endpoint").textContent = l.endpoint;
    link($("cursor"), l.install && l.install.cursor_url);
    link($("vscode"), l.install && l.install.vscode_url);
    $("config").textContent = JSON.stringify(l.install ? l.install.client_config : {}, null, 2);

    const plans = l.plans || [];
    if (plans.length) {
      for (const plan of plans) item($("plans"), plan.name, "strong", priceLines(plan));
      $("testmode").hidden = l.livemode;
      if (l.trial_days) {
        $("trial").textContent = l.trial_days + "-day free trial";
        $("trial").hidden = false;
      }
      $("plans-section").hidden = false;
    }
    if (l.signup_open && l.signup_url) {
      $("signup-link").href = l.signup_url;
      $("signup").hidden = false;
      $("plans-section").hidden = false;
    }

    const tools = l.tools || [];
    if (!tools.length) item($("tools"), "No tools listed", "span", []);
    for (const t of tools) item($("tools"), t.name, "code", [t.title, t.description]);
    $("published").textContent = "Tools as of " + new Date(l.published_at).toLocaleDateString();
    renderChangelog(l.changelog || []);
    $("listing-view").hidden = false;
  }

  // ADR-20261127191645: tool contract changes shipped since publishing.
  function renderChangelog(entries) {
    const words = { compatible: "Added or widened", review: "Changed", breaking: "Breaking change", unverified: "Unverified release" };
    for (const e of entries) {
      const head = new Date(e.date).toLocaleDateString() + " · " + (words[e.verdict] || e.verdict);
      const tools = (e.tools || []).join(", ");
      item($("changelog"), head, "strong", [tools ? "Tools: " + tools : "", e.changes ? e.changes + (e.changes === 1 ? " change" : " changes") : ""]);
    }
    $("changelog-section").hidden = entries.length === 0;
  }

  async function copyEndpoint() {
    try {
      await navigator.clipboard.writeText($("endpoint").textContent);
      $("copy-endpoint").textContent = "Copied";
    } catch (e) {
      $("copy-endpoint").textContent = "Select and copy";
    }
  }

  async function start() {
    $("copy-endpoint").addEventListener("click", copyEndpoint);
    let res;
    try {
      res = await fetch("/v1/consumer-signup/" + encodeURIComponent(slug) + "/listing", { credentials: "omit", cache: "no-store" });
    } catch (e) {
      message("Unavailable", "Could not load this server. Check your connection and reload.");
      return;
    }
    if (!res.ok) {
      message("Not found", "This MCP server is not listed.");
      return;
    }
    try {
      render(await res.json());
    } catch (e) {
      message("Unavailable", "Could not read this server's listing. Reload to try again.");
    }
  }

  start();
})();
