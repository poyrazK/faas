# ADR: Data API usage reporting and owner monthly caps

Date: 2026-10-19
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR 20261018170742](20261018170742-data-api-access-modes-and-clients.md), [ADR-348](348-customer-after-restore-hook.md)

## Context

Owners cannot see how a Data API is used beyond recent logs: how many requests
come from signed-in users, anonymous visitors and server keys, how many rows
and bytes they read, or how this month compares with the last. With public
reads and public RPCs, a popular or abused page can also drive unbounded
anonymous traffic to the owner's database.

A Data API app is already billed like any app (plan GB-hours, with traffic
metered per app VM), so usage counts are not needed to price it, and no plan
quota for Data API requests exists in the financial model. This decision adds
owner-facing usage and owner-chosen caps, not plan limits.

## Decision

### Reporting

- The runtime counts each request (except health checks and preflights) into
  UTC-hour buckets. Counters: admitted requests by access kind
  (`user`, `anonymous`, `server`), rejections (4xx), errors (5xx), requests
  refused by caps, rows the engine reported in `Content-Range`, and response
  bytes streamed from the engine or catalog.
- It reports deltas to `POST /v1/runtime/data-api/usage` within 15 seconds of
  new traffic, under a `gregale:data-api:usage` workload token. The token,
  not the body, names the account, app and instance. Hours must be aligned
  and at most 48 hours old.
- Reporting is on when `DATA_API_USAGE_URL` names the platform's HTTPS origin.
  `data-api create` sets it when the CLI targets an HTTPS API;
  `data-api configure --usage-reporting on|off` changes it.

### Snapshot restores

A parked app restores as several instances from one memory image (ADR-348),
so counts held in memory would otherwise be reported once per instance.

- Each report has an id built from the instance id in its workload token, a
  per-process nonce and a sequence number. apid applies each id once
  (`data_api_usage_reports`, kept for 48 hours) and adds the deltas to
  `data_api_usage_hourly` in the same transaction. A report in flight at
  snapshot time and retried by several restored instances is counted once.
- Reporting within 15 seconds means little is pending when an idle app parks.
  The runtime detects a restore as the wall clock jumping more than 5 seconds
  against the monotonic clock, and drops counts still pending then rather
  than reporting them per instance. The cost is undercounting traffic whose
  report failed just before a park.

### Trust

The runtime runs in the customer's own VM, so a modified runtime could report
anything for its own app. The counts are therefore owner telemetry, never
billing records or plan enforcement. apid accepts them only for the token's
app in the token's account, bounds every value, and prunes this app's rows
older than 90 days and dedup ids older than 48 hours, a bounded batch per
report, so no sweeper is needed.

### Owner caps

- `DATA_API_MONTHLY_CAPS` (`data-api configure --monthly-caps
  requests=N[,anonymous=N]|off`) caps admitted requests per UTC calendar
  month: all of them, anonymous ones, or both. Caps need reporting; the
  runtime refuses to start with caps and no `DATA_API_USAGE_URL`.
- Each report response carries month-to-date totals. The runtime compares
  them plus its own unreported counts with the caps, before admission. A
  reached cap answers `429 data_api_monthly_cap_reached` with the cap name,
  limit, observed value, a docs link, and `Retry-After` until the next UTC
  month. A reached anonymous cap refuses only anonymous requests; signed-in
  users and server keys continue.
- Enforcement is approximate. Several instances can each overshoot by their
  unreported traffic, about one report interval. A runtime that has not yet
  heard from the platform enforces against its own counts alone.

### Owner view

`GET /v1/apps/{slug}/data-api/usage?since=…&granularity=hour|day` returns
zero-filled buckets, window totals and month-to-date totals, at most 90 days
and at most 48 hours hourly. `gregale data-api usage NAME` prints them.
`data-api diagnostics` counts cap refusals.

## Consequences

- Two new tables, written only by apid, both cascading on app deletion.
- Usage appears only for runtimes deployed after this change with reporting
  on. Existing Data APIs need `configure --usage-reporting on`.
- Cap values are app secrets and are not readable back; the usage view shows
  month-to-date totals and capped requests instead.
- Counts can undercount around parks and failed reports and can overshoot
  caps by about one report interval per instance.

## Rejected alternatives

- **Counting at the edge.** The gateway's request analytics already count
  requests per app, but cannot see access kinds, rows or Data API
  rejections. Adding those dimensions to gatewayd would put Data API
  semantics into the edge.
- **Cumulative counters per instance.** Replace-on-report rows keyed by
  instance would double-count everything a snapshot copied into several
  instances.
- **Plan quotas.** No Data API request quota exists in the financial model;
  owner caps protect owners without inventing business numbers.
