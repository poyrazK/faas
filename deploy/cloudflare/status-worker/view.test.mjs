import assert from "node:assert/strict";
import test from "node:test";

import { COMPONENTS, STALE_AFTER_MS, deriveView, worse } from "./public/view.mjs";

const NOW = Date.UTC(2026, 10, 15, 9, 0, 0);
const iso = (offsetMs) => new Date(NOW + offsetMs).toISOString();

function statusDoc({ ageMs = 30_000, overall = "operational", componentStatus = "operational", active = [] } = {}) {
  return {
    schema: 1,
    published_at: iso(-ageMs),
    status: {
      overall_status: overall,
      data_status: "fresh",
      updated_at: iso(-ageMs),
      components: COMPONENTS.map(({ id, name }) => ({ id, name, status: componentStatus, uptime_30d_pct: 100, coverage_30d_pct: 99.6, daily: [] })),
      indicators: [{ id: "api_availability", sample_status: "available", value: 99.99 }],
      active_events: active,
      upcoming_maintenance: [],
      resolved_incidents: [],
    },
  };
}

function probeDoc({ ageMs = 20_000, ok = true, failures = 0 } = {}) {
  return {
    schema: 1,
    checked_at: iso(-ageMs),
    checks: [{ id: "api", name: "API", components: ["api_console"], ok, status: ok ? 200 : 0, consecutive_failures: failures }],
  };
}

test("fresh apid status is shown as-is", () => {
  const view = deriveView({ status: statusDoc(), probe: probeDoc(), now: NOW });
  assert.equal(view.overall, "operational");
  assert.equal(view.banner, null);
  assert.equal(view.components.every((c) => c.status === "operational"), true);
  assert.equal(view.indicators.length, 1);
});

test("stale status with failing outside checks is a major outage, not the last green state", () => {
  const view = deriveView({ status: statusDoc({ ageMs: STALE_AFTER_MS + 60_000 }), probe: probeDoc({ ok: false, failures: 3 }), now: NOW });
  assert.equal(view.overall, "major_outage");
  assert.equal(view.banner.level, "bad");
  assert.match(view.banner.text, /not reachable from outside/);
  assert.equal(view.components.find((c) => c.id === "api_console").status, "major_outage");
  assert.equal(view.components.find((c) => c.id === "deployments").status, "unknown");
  assert.deepEqual(view.indicators, []);
});

test("stale status with passing checks is unknown with a delay banner", () => {
  const view = deriveView({ status: statusDoc({ ageMs: STALE_AFTER_MS + 1 }), probe: probeDoc(), now: NOW });
  assert.equal(view.overall, "unknown");
  assert.equal(view.banner.level, "warn");
  assert.match(view.banner.text, /delayed/);
});

test("nothing fresh at all is unknown", () => {
  const view = deriveView({ status: null, probe: null, now: NOW });
  assert.equal(view.overall, "unknown");
  assert.match(view.banner.text, /monitoring is currently unavailable/);
  assert.equal(view.components.length, 5);
});

test("a single probe failure is not an outage", () => {
  const view = deriveView({ status: statusDoc(), probe: probeDoc({ ok: false, failures: 1 }), now: NOW });
  assert.equal(view.overall, "operational");
  assert.equal(view.banner, null);
});

test("failing outside checks raise a fresh green status", () => {
  const view = deriveView({ status: statusDoc(), probe: probeDoc({ ok: false, failures: 2 }), now: NOW });
  assert.equal(view.overall, "major_outage");
  assert.match(view.banner.text, /Outside checks are failing: API/);
});

test("offline notices raise components and appear first among active events", () => {
  const notices = {
    schema: 1,
    events: [
      { id: "n1", kind: "incident", title: "Deploys stuck", impact: "partial_outage", components: ["deployments"], state: "investigating", updates: [] },
      { id: "n2", kind: "incident", title: "Old", impact: "major_outage", components: ["networking"], state: "resolved", resolved_at: iso(-3600_000), updates: [] },
    ],
  };
  const apidEvent = { id: "a1", kind: "incident", title: "API errors", impact: "degraded", components: ["api_console"], state: "identified", updates: [] };
  const view = deriveView({ status: statusDoc({ active: [apidEvent] }), probe: probeDoc(), notices, now: NOW });
  assert.equal(view.overall, "partial_outage");
  assert.equal(view.components.find((c) => c.id === "deployments").status, "partial_outage");
  assert.equal(view.components.find((c) => c.id === "networking").status, "operational");
  assert.deepEqual(view.events.active.map((e) => e.id), ["n1", "a1"]);
  assert.equal(view.events.active[0].offline, true);
  assert.deepEqual(view.events.resolved.map((e) => e.id), ["n2"]);
});

test("notices still show while apid is stale", () => {
  const notices = { schema: 1, events: [{ id: "n1", impact: "major_outage", components: ["api_console"], state: "investigating", updates: [] }] };
  const view = deriveView({ status: statusDoc({ ageMs: STALE_AFTER_MS * 2 }), probe: null, notices, now: NOW });
  assert.equal(view.overall, "major_outage");
  assert.equal(view.components.find((c) => c.id === "api_console").status, "major_outage");
});

test("documents with an unknown schema or a future timestamp are ignored", () => {
  assert.equal(deriveView({ status: { ...statusDoc(), schema: 2 }, now: NOW }).statusFresh, false);
  assert.equal(deriveView({ status: { ...statusDoc(), published_at: iso(STALE_AFTER_MS * 2) }, now: NOW }).statusFresh, false);
});

test("worse orders states and treats unknown as weakest", () => {
  assert.equal(worse("operational", "degraded"), "degraded");
  assert.equal(worse("major_outage", "degraded"), "major_outage");
  assert.equal(worse("unknown", "degraded"), "degraded");
  assert.equal(worse("operational", "unknown"), "operational");
});
