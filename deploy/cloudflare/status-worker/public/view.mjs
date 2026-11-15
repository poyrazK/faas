// Pure view model for the off-platform status page (ADR-20261115090000).
//
// The page combines three documents from the status mirror bucket:
//   status.json  — apid's /v1/status, pushed every minute while Gregale runs
//   probe.json   — this Worker's outside checks, written every minute
//   notices.json — incidents operators post without apid
//
// The rule that matters: a status copy that stops updating is evidence of an
// outage, never a reason to keep showing the last green state.

export const STALE_AFTER_MS = 3 * 60 * 1000;
export const PROBE_FAILURES_FOR_OUTAGE = 2;

export const COMPONENTS = [
  { id: "api_console", name: "API & Console" },
  { id: "deployments", name: "Deployments" },
  { id: "app_execution", name: "App execution" },
  { id: "networking", name: "Networking" },
  { id: "observability", name: "Observability" },
];

const RANK = { operational: 0, maintenance: 1, degraded: 2, partial_outage: 3, major_outage: 4 };

export function worse(a, b) {
  if (!(a in RANK)) return b in RANK ? b : "unknown";
  if (!(b in RANK)) return a;
  return RANK[b] > RANK[a] ? b : a;
}

function timeMs(value) {
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

function fresh(at, now) {
  const ms = timeMs(at);
  return ms != null && now - ms <= STALE_AFTER_MS && ms - now <= STALE_AFTER_MS;
}

function documentOK(doc) {
  return doc != null && typeof doc === "object" && doc.schema === 1;
}

/**
 * @param {{status?: object|null, probe?: object|null, notices?: object|null, now: number}} input
 */
export function deriveView({ status = null, probe = null, notices = null, now }) {
  const statusDoc = documentOK(status) ? status : null;
  const probeDoc = documentOK(probe) ? probe : null;
  const noticesDoc = documentOK(notices) ? notices : null;
  const overview = statusDoc?.status && typeof statusDoc.status === "object" ? statusDoc.status : null;

  const statusFresh = statusDoc != null && fresh(statusDoc.published_at, now);
  const probeFresh = probeDoc != null && fresh(probeDoc.checked_at, now);
  const checks = probeFresh && Array.isArray(probeDoc.checks) ? probeDoc.checks : [];
  const failing = checks.filter((check) => !check.ok && Number(check.consecutive_failures) >= PROBE_FAILURES_FOR_OUTAGE);
  const allFailing = checks.length > 0 && failing.length === checks.length;

  const noticeEvents = Array.isArray(noticesDoc?.events) ? noticesDoc.events.map((event) => ({ ...event, offline: true })) : [];
  const openNotices = noticeEvents.filter((event) => !event.resolved_at);
  const resolvedNotices = noticeEvents.filter((event) => event.resolved_at);

  // Components: apid's view while it is current, unknown once it is not.
  const known = new Map((Array.isArray(overview?.components) ? overview.components : []).map((c) => [c.id, c]));
  const components = COMPONENTS.map(({ id, name }) => {
    const base = known.get(id) || { id, name, uptime_30d_pct: null, coverage_30d_pct: null, daily: [] };
    return { ...base, id, name: base.name || name, status: statusFresh ? base.status || "unknown" : "unknown" };
  });
  const byID = new Map(components.map((c) => [c.id, c]));
  const raise = (id, state) => {
    const component = byID.get(id);
    if (component) component.status = component.status === "unknown" ? state : worse(component.status, state);
  };
  for (const check of failing) {
    for (const id of check.components || []) raise(id, "major_outage");
  }
  for (const event of openNotices) {
    for (const id of event.components || []) raise(id, event.impact);
  }

  // Overall status.
  let external = "operational";
  if (failing.length > 0) external = allFailing ? "major_outage" : "partial_outage";
  for (const event of openNotices) external = worse(external, event.impact);
  let overall;
  if (statusFresh) {
    overall = worse(overview?.overall_status || "unknown", external);
  } else if (external !== "operational") {
    overall = external;
  } else {
    overall = "unknown";
  }

  // Banner explaining anything the page inferred rather than read.
  const statusAge = statusDoc ? ageLabel(now - (timeMs(statusDoc.published_at) ?? now)) : null;
  const failingNames = failing.map((check) => check.name || check.id).join(", ");
  let banner = null;
  if (!statusFresh && failing.length > 0) {
    banner = { level: "bad", text: `Gregale is not reachable from outside (${failingNames} failing). Live platform telemetry is unavailable${statusAge ? `; last report ${statusAge} ago` : ""}.` };
  } else if (!statusFresh && probeFresh) {
    banner = { level: "warn", text: `Live platform telemetry is delayed${statusAge ? ` (last report ${statusAge} ago)` : ""}. Outside checks are passing.` };
  } else if (!statusFresh) {
    banner = { level: "warn", text: "Status monitoring is currently unavailable. Component states are unknown." };
  } else if (failing.length > 0) {
    banner = { level: "bad", text: `Outside checks are failing: ${failingNames}.` };
  }

  const fromOverview = (key) => (statusDoc && Array.isArray(overview?.[key]) ? overview[key] : []);
  return {
    overall,
    banner,
    statusFresh,
    probeFresh,
    publishedAt: statusDoc?.published_at || null,
    telemetryUpdatedAt: overview?.updated_at || null,
    dataStatus: statusFresh ? overview?.data_status || "unknown" : "stale",
    components,
    indicators: statusFresh ? fromOverview("indicators") : [],
    checks,
    events: {
      active: [...openNotices, ...fromOverview("active_events")],
      upcoming: fromOverview("upcoming_maintenance"),
      resolved: [...resolvedNotices, ...fromOverview("resolved_incidents")],
    },
  };
}

export function ageLabel(ms) {
  const seconds = Math.max(0, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 120) return `${minutes}m`;
  return `${Math.round(minutes / 60)}h`;
}
