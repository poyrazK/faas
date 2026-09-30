import { id } from "./client.js";

const day = 24 * 60 * 60 * 1000;

export function monthPeriods(month, now = new Date()) {
  if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(month || "") || month < "2000-01") {
    throw new Error("Supply a completed UTC calendar month as YYYY-MM");
  }
  const start = new Date(`${month}-01T00:00:00.000Z`);
  const end = new Date(start); end.setUTCMonth(end.getUTCMonth() + 1);
  if (end > now) throw new Error("Billing requires a completed UTC calendar month");
  const periods = [];
  for (let at = +start; at < +end; at += day) {
    periods.push({ period_start: new Date(at).toISOString(), period_end: new Date(at + day).toISOString() });
  }
  return periods;
}

function quantity(value) {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error("Statement quantities must be non-negative safe integers; use an exact-integer billing client for larger values");
  }
  return BigInt(value);
}

// Keep minute evidence on the server. These groups are a review projection,
// never coverage records or a replacement for immutable statement revisions.
export function summarizeStatements(tenantID, month, statements) {
  const currencies = new Set(), seen = new Set(), groups = new Map();
  let units = 0n, unpriced = 0n, amount = 0n, draftAmount = 0n, finalizedAmount = 0n;
  const receipts = [];
  for (const statement of statements) {
    if (statement.status === "superseded") continue;
    if (statement.tenant_id !== tenantID || seen.has(statement.id) ||
        !["draft", "finalized"].includes(statement.status)) {
      throw new Error("Invalid or duplicate tenant statement in billing review");
    }
    id(statement.id); seen.add(statement.id);
    if (statement.currency) currencies.add(statement.currency);
    let lineUnits = 0n, lineUnpriced = 0n, lineAmount = 0n;
    for (const line of statement.lines) {
      const billable = quantity(line.billable_units), charge = quantity(line.amount_millicents);
      lineUnits += billable; lineAmount += charge;
      if (!line.rate_card_id && !line.platform_tenant_rate_card_id) lineUnpriced += billable;
      const identity = { app_id: line.app_id, consumer_id: line.consumer_id || "",
        surface_id: line.surface_id || "", jwt_authorization_rule_id: line.jwt_authorization_rule_id || "",
        rate_card_id: line.rate_card_id || "", platform_tenant_rate_card_id: line.platform_tenant_rate_card_id || "",
        currency: line.currency || "", price_millicents_per_unit: quantity(line.price_millicents_per_unit || 0).toString() };
      const key = JSON.stringify(identity);
      const group = groups.get(key) || { ...identity, units: 0n, amount: 0n };
      group.units += billable; group.amount += charge; groups.set(key, group);
    }
    if (lineUnits !== quantity(statement.billable_units) || lineUnpriced !== quantity(statement.unpriced_units) ||
        lineAmount !== quantity(statement.amount_millicents)) {
      throw new Error("Statement evidence does not match its totals");
    }
    units += lineUnits; unpriced += lineUnpriced; amount += lineAmount;
    if (statement.status === "draft") draftAmount += lineAmount;
    else finalizedAmount += lineAmount;
    receipts.push({ id: statement.id, revision: statement.revision, status: statement.status,
      period_start: statement.period_start, period_end: statement.period_end,
      billable_units: lineUnits.toString(), unpriced_units: lineUnpriced.toString(),
      amount_millicents: lineAmount.toString() });
  }
  if (currencies.size > 1) throw new Error("Monthly review spans currencies; resolve pricing before invoicing");
  return { tenant_id: tenantID, month, currency: [...currencies][0] || "",
    billable_units: units.toString(), unpriced_units: unpriced.toString(), amount_millicents: amount.toString(),
    draft_amount_millicents: draftAmount.toString(), finalized_amount_millicents: finalizedAmount.toString(),
    statements: receipts, items: [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([, group]) => {
      const { units: count, amount: charge, ...identity } = group;
      return { ...identity, billable_units: count.toString(), amount_millicents: charge.toString() };
    }) };
}

export async function draftMonth(api, tenantID, month, now) {
  id(tenantID);
  const periods = monthPeriods(month, now), root = `/v1/account/platform-tenants/${tenantID}`;
  // Usage is only an empty-day hint. Prices and coverage always come from the
  // statement API. Existing periods are rechecked even when usage reads zero.
  const usageQuery = new URLSearchParams({ since: periods[0].period_start, until: periods.at(-1).period_end });
  const usage = await api.request("GET", `${root}/usage?${usageQuery}`);
  if (usage.tenant_id !== tenantID) throw new Error("Usage response belongs to another tenant");
  const activeDays = new Set();
  for (const bucket of usage.buckets) {
    if (quantity(bucket.billable_units) > 0n) activeDays.add(bucket.window_start.slice(0, 10));
  }
  const statements = [];
  for (const period of periods) {
    const query = new URLSearchParams(period);
    let inventory = await api.request("GET", `${root}/usage-statements?${query}`);
    if (activeDays.has(period.period_start.slice(0, 10)) || inventory.statements.length) {
      await api.request("POST", `${root}/usage-statements`, period);
      inventory = await api.request("GET", `${root}/usage-statements?${query}`);
    }
    for (const statement of inventory.statements) {
      if (+new Date(statement.period_start) !== +new Date(period.period_start) ||
          +new Date(statement.period_end) !== +new Date(period.period_end)) {
        throw new Error("Statement response does not match the requested billing period");
      }
    }
    statements.push(...inventory.statements);
  }
  return summarizeStatements(tenantID, month, statements);
}

export async function finalizeStatement(api, tenantID, statementID) {
  return api.request("POST", `/v1/account/platform-tenants/${id(tenantID)}/usage-statements/${id(statementID)}/finalize`);
}

export async function handoffStatement(api, tenantID, statementID, externalInvoiceID) {
  if (!externalInvoiceID || externalInvoiceID.trim() !== externalInvoiceID ||
      Buffer.byteLength(externalInvoiceID) > 255) throw new Error("Supply an external invoice reference of 1–255 bytes");
  return api.request("POST", `/v1/account/platform-tenants/${id(tenantID)}/usage-statements/${id(statementID)}/handoff`,
    { external_invoice_id: externalInvoiceID });
}
