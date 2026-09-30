import test from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { apiClient } from "../tools/client.js";
import { draftMonth, monthPeriods, summarizeStatements, finalizeStatement, handoffStatement } from "../tools/billing.js";

function billingService() {
  const tenant = randomUUID(), appIDs = [randomUUID(), randomUUID()], consumers = [randomUUID(), randomUUID()];
  const rates = [randomUUID(), randomUUID()], inventory = new Map(), coverageByStatement = new Map(), calls = [];
  let lostResponse = false;
  const usage = [];
  // Every minute on two apps: the server retains 89,280 private coverage rows
  // while exposing only compact invoice lines.
  for (let minute = 0; minute < 31 * 1440; minute++) {
    for (let app = 0; app < 2; app++) usage.push({ app_id: appIDs[app], consumer_id: consumers[app],
      window_start: new Date(Date.UTC(2026, 7, 1) + minute * 60000).toISOString(), billable_units: 1,
      rate_card_id: rates[app], currency: "EUR", price_millicents_per_unit: app + 1, amount_millicents: app + 1 });
  }
  const api = apiClient("https://api.gregale.dev", "owner-only", async (url, options) => {
    const path = url.pathname, body = options.body && JSON.parse(options.body);
    calls.push({ path, method: options.method, body });
    assert.equal(options.headers.authorization, "Bearer owner-only");
    assert.ok(path.startsWith(`/v1/account/platform-tenants/${tenant}/`));
    let data;
    if (path.endsWith("/usage")) data = { tenant_id: tenant, buckets: usage };
    else if (path.endsWith("/usage-statements")) {
      const start = body?.period_start || url.searchParams.get("period_start");
      const end = body?.period_end || url.searchParams.get("period_end");
      const revisions = inventory.get(start) || [];
      if (options.method === "GET") data = { statements: revisions };
      else {
        const current = usage.filter((line) => line.window_start >= start && line.window_start < end);
        const covered = new Map();
        for (const revision of revisions.filter((s) => s.status === "finalized")) {
          for (const minute of coverageByStatement.get(revision.id) || []) {
            const key = `${minute.app_id}\0${minute.consumer_id}\0${minute.surface_id}\0${minute.jwt_authorization_rule_id}\0${minute.window_start}`;
            covered.set(key, (covered.get(key) || 0) + minute.billable_units);
          }
        }
        const deltaCoverage = [], grouped = new Map();
        for (const line of current) {
          const coverageKey = `${line.app_id}\0${line.consumer_id}\0${line.surface_id}\0${line.jwt_authorization_rule_id}\0${line.window_start}`;
          const priorUnits = covered.get(coverageKey) || 0;
          if (line.billable_units < priorUnits) throw new Error("usage regressed below finalized coverage");
          covered.delete(coverageKey);
          const units = line.billable_units - priorUnits;
          if (!units) continue;
          const minute = { ...line, billable_units: units, amount_millicents: units * line.price_millicents_per_unit };
          deltaCoverage.push({ app_id: line.app_id, consumer_id: line.consumer_id, surface_id: line.surface_id,
            jwt_authorization_rule_id: line.jwt_authorization_rule_id, window_start: line.window_start, billable_units: units });
          const groupKey = JSON.stringify([line.app_id, line.consumer_id, line.surface_id, line.jwt_authorization_rule_id,
            line.rate_card_id, line.platform_tenant_rate_card_id, line.currency, line.price_millicents_per_unit]);
          const group = grouped.get(groupKey) || { ...minute, window_end: minute.window_start, billable_units: 0, amount_millicents: 0 };
          group.window_start = group.window_start < minute.window_start ? group.window_start : minute.window_start;
          const minuteEnd = new Date(new Date(minute.window_start).getTime() + 60000).toISOString();
          group.window_end = group.window_end > minuteEnd ? group.window_end : minuteEnd;
          group.billable_units += units;
          group.amount_millicents += minute.amount_millicents;
          grouped.set(groupKey, group);
        }
        if ([...covered.values()].some((units) => units > 0)) throw new Error("finalized coverage is missing from usage");
        const delta = [...grouped.values()].sort((a, b) => a.window_start.localeCompare(b.window_start) || a.app_id.localeCompare(b.app_id));
        const latest = revisions.at(-1);
        const latestCoverage = latest && coverageByStatement.get(latest.id);
        if (!delta.length || (latest?.status === "draft" && JSON.stringify(latest.lines) === JSON.stringify(delta) &&
            JSON.stringify(latestCoverage) === JSON.stringify(deltaCoverage))) data = latest;
        else {
          if (latest?.status === "draft") latest.status = "superseded";
          data = { id: randomUUID(), tenant_id: tenant, period_start: start, period_end: end,
            revision: revisions.length + 1, status: "draft", currency: "EUR", unpriced_units: 0,
            billable_units: delta.reduce((n, line) => n + line.billable_units, 0),
            amount_millicents: delta.reduce((n, line) => n + line.amount_millicents, 0), lines: delta };
          coverageByStatement.set(data.id, deltaCoverage);
          revisions.push(data); inventory.set(start, revisions);
        }
        if (lostResponse) { lostResponse = false; throw new Error("Response lost after commit"); }
      }
    } else if (path.endsWith("/finalize")) {
      const statement = [...inventory.values()].flat().find((s) => path.includes(s.id));
      statement.status = "finalized"; data = statement;
    } else if (path.endsWith("/handoff")) data = { external_invoice_id: body.external_invoice_id };
    else throw new Error("Unexpected API operation");
    return new Response(JSON.stringify(data), { status: 200 });
  });
  return { api, tenant, inventory, calls, usage, loseNextResponse() { lostResponse = true; } };
}

test("calendar billing is UTC, closed-month only, and includes leap days", () => {
  const leap = monthPeriods("2024-02", new Date("2024-03-01T00:00:00Z"));
  assert.equal(leap.length, 29);
  assert.equal(leap.at(-1).period_end, "2024-03-01T00:00:00.000Z");
  assert.equal(monthPeriods("2025-12", new Date("2026-01-01T00:00:00Z")).length, 31);
  for (const month of ["2026-13", "2026-2", "invalid", "0099-01"]) assert.throws(() => monthPeriods(month));
  assert.throws(() => monthPeriods("2026-09", new Date("2026-09-30T21:00:00Z")), /completed/);
});

test("full-month two-app review is compact, retryable, and never finalizes or hands off implicitly", async () => {
  const service = billingService();
  service.loseNextResponse();
  await assert.rejects(draftMonth(service.api, service.tenant, "2026-08"), /retry/);
  const result = await draftMonth(service.api, service.tenant, "2026-08");
  assert.equal(result.statements.length, 31);
  assert.equal(result.items.length, 2);
  assert.equal(result.billable_units, "89280");
  assert.equal(result.amount_millicents, "133920");
  assert.equal(result.draft_amount_millicents, "133920");
  assert.equal(result.finalized_amount_millicents, "0");
  assert.equal(result.unpriced_units, "0");
  assert.equal(result.currency, "EUR");
  const firstStatement = service.inventory.get(result.statements[0].period_start)[0];
  assert.equal(firstStatement.lines.length, 2);
  for (const line of firstStatement.lines) {
    assert.equal(line.billable_units, 1440);
    assert.equal(line.window_start, firstStatement.period_start);
    assert.equal(line.window_end, firstStatement.period_end);
  }
  assert.deepEqual(await draftMonth(service.api, service.tenant, "2026-08"), result);
  assert.equal(service.calls.some((call) => /\/(finalize|handoff)$/.test(call.path)), false);
});

test("finalized monthly totals retain old revisions and include only new units in adjustments", async () => {
  const service = billingService();
  const initial = await draftMonth(service.api, service.tenant, "2026-08");
  for (const statement of initial.statements) await finalizeStatement(service.api, service.tenant, statement.id);
  service.usage[0].billable_units += 2; service.usage[0].amount_millicents += 2;
  const adjusted = await draftMonth(service.api, service.tenant, "2026-08");
  assert.equal(adjusted.statements.length, 32);
  assert.equal(adjusted.billable_units, "89282");
  assert.equal(adjusted.amount_millicents, "133922");
  assert.equal(adjusted.draft_amount_millicents, "2");
  assert.equal(adjusted.finalized_amount_millicents, "133920");
  const draft = adjusted.statements.find((s) => s.status === "draft");
  assert.equal(draft.billable_units, "2");
  assert.equal(draft.revision, 2);
  await finalizeStatement(service.api, service.tenant, draft.id);
  assert.deepEqual(await handoffStatement(service.api, service.tenant, draft.id, `invoice-42/${draft.id}`),
    { external_invoice_id: `invoice-42/${draft.id}` });
  assert.equal((await draftMonth(service.api, service.tenant, "2026-08")).billable_units, "89282");
});

test("empty days create no statements and invalid tenant IDs make no API calls", async () => {
  const service = billingService(); service.usage.length = 0;
  const result = await draftMonth(service.api, service.tenant, "2026-08");
  assert.equal(result.billable_units, "0"); assert.deepEqual(result.statements, []);
  assert.equal(service.calls.some((call) => call.method === "POST"), false);
  const before = service.calls.length;
  await assert.rejects(draftMonth(service.api, "not-a-tenant", "2026-08"), /UUID/);
  assert.equal(service.calls.length, before);
});

test("review rejects cross-tenant evidence, mixed currencies, and unsafe numbers", () => {
  const tenant = randomUUID();
  const statement = { id: randomUUID(), tenant_id: tenant, status: "draft", currency: "EUR",
    billable_units: 1, unpriced_units: 1, amount_millicents: 0,
    lines: [{ app_id: randomUUID(), billable_units: 1, amount_millicents: 0 }] };
  const foreign = { ...statement, tenant_id: randomUUID() };
  assert.throws(() => summarizeStatements(tenant, "2026-08", [foreign]), /tenant statement/);
  assert.throws(() => summarizeStatements(tenant, "2026-08", [statement, statement]), /duplicate/);
  assert.throws(() => summarizeStatements(tenant, "2026-08", [statement,
    { ...statement, id: randomUUID(), currency: "USD" }]), /currencies/);
  assert.throws(() => summarizeStatements(tenant, "2026-08", [{ ...statement, billable_units: 2 }]), /totals/);
  assert.throws(() => summarizeStatements(tenant, "2026-08", [{ ...statement,
    lines: [{ ...statement.lines[0], billable_units: Number.MAX_SAFE_INTEGER + 1 }] }]), /safe integers/);
  assert.equal(summarizeStatements(tenant, "2026-08", [statement]).unpriced_units, "1");
});

test("repriced drafts exclude superseded evidence and preserve distinct price sources", () => {
  const tenant = randomUUID(), app = randomUUID(), consumer = randomUUID(), card = randomUUID();
  const line = { app_id: app, consumer_id: consumer, rate_card_id: card, currency: "EUR",
    billable_units: 2, price_millicents_per_unit: 5, amount_millicents: 10 };
  const draft = { id: randomUUID(), tenant_id: tenant, status: "draft", currency: "EUR",
    billable_units: 2, unpriced_units: 0, amount_millicents: 10, lines: [line] };
  const other = { ...draft, id: randomUUID(), lines: [{ ...line, rate_card_id: "",
    platform_tenant_rate_card_id: randomUUID() }] };
  const result = summarizeStatements(tenant, "2026-08", [
    { ...draft, id: randomUUID(), status: "superseded" }, draft, other,
  ]);
  assert.equal(result.statements.length, 2);
  assert.equal(result.items.length, 2);
  assert.equal(result.billable_units, "4");
  assert.equal(result.draft_amount_millicents, "20");
});
