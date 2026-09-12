# Runbook · StatusPageDegraded

> **Trigger:** `/status/slo.json` returns `degraded: true` or
> surfaces any open incident with `severity ∈ {degraded, partial_outage, full_outage}`.
> **Endpoint:** public apid `/status/slo.json` (issue #276 / ADR-130).
> **Storage:** Postgres `status_incidents` table (migrations/00412).

## Symptom

The public status page (`deploy/statuspage/index.html`) is
showing a red banner + an active incident. Customers see this on
the landing page before they see anything else. Time-to-detect
for an outage is now "the moment the operator filed the incident"
instead of "when the first customer tweeted about it".

## Why now?

- An operator filed an incident via
  `gregale status incident post --component=<X> --severity=<Y> --message=<Z>`.
- OR a customer-impacting event triggered an automated post (the
  `degraded: true` flag on the slo.json response — driven by
  meterd's loopback Prometheus exporter tripping an SLO).
- OR the operator forgot to resolve a prior incident that's now
  stale.

## Triage (3-signal ladder)

1. **Read the incident on the page.** The page lists recent and
   still-open incidents in posted-at DESC order — most recent first.
   Each incident carries `component`, `severity`, `summary`,
   `started_at`, and nullable `resolved_at`.
2. **Cross-reference with the alerting pipeline.** Check whether
   any of the page's alerts are also firing
   (`/v1/alerts/active.json` or the equivalent Prometheus
   alertmanager view). The page and the alerts should
   agree on which components are degraded.
3. **Check the SLO ratios.** `api_availability`,
   `wake_latency_p95`, `build_success_ratio` come from meterd's
   loopback Prometheus exporter. A degraded: true flag means
   at least one SLO is below target.

## Mitigate

- **Resolve the incident** when the underlying issue is fixed:
  `gregale status incident resolve <id>`. The page re-fetches
  on the next load (15s TTL via `Cache-Control: max-age=15`)
  and drops the banner.
- **Update the message** in-place if the situation changes
  (no CLI surface for this yet — open a follow-up issue if
  the need recurs).
- **File a follow-up incident** if the underlying issue spans
  multiple components — link the two incidents via the
  `message` field.

## Follow-up

- Review the open-incident list weekly; stale incidents drift
  into "this is just the way it is" territory.
- Track mean-time-to-resolve (MTTR) per component as a quarterly
  KPI.
- Add automated posting of incidents when the alertmanager
  `severity: page` route group fires (currently manual — a
  follow-on PR).
