# ADR 20261117084505 · Product analytics: events, flag breakdowns, funnels and retention

- **Status:** accepted
- **Date:** 2026-11-17
- **Amends:** ADR-20261115163113 (flag experiments), ADR-20261116190208
  (experiment guardrails and numeric metrics)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Runtime SDKs already send `track()` events for flag experiments. apid keeps
only one row per customer, event and UTC day (`flag_conversion_daily`), and
deletes it with the plan's request telemetry retention (3, 7 or 14 days). A
builder can therefore test a variant but cannot ask what customers do: how
many reach checkout after signing up, how many come back a week later, or
whether either changed for customers who saw the new variant. Builders answer
these with a second product (PostHog, Amplitude, Mixpanel) and send the same
events twice.

The owner chose before this record:

1. **Retention:** product analytics keeps raw events for its own per-plan
   retention, longer than request telemetry, so 30-day retention curves work.
2. **Properties:** an event carries up to 10 string properties for breakdowns
   and filters.
3. **Unit:** the verified customer by default; an app may also attach an
   opaque end-user subject, which Gregale stores only as a hash.
4. **Pricing:** analytics events count against the existing monthly event
   quota (`FlagEventsPerMonth`). Events over the quota are dropped and
   counted, never billed.

## Decision

### 1. One event, two uses

`track()` stays the only call. An event is:

```json
{"name": "order_completed", "customer_id": "<uuid>", "value": 4200,
 "at": "2026-11-17T08:00:00Z", "subject": "user-81",
 "properties": {"plan": "pro", "source": "email"},
 "flags": {"checkout": "new", "export": "true"}}
```

- `subject` is optional: the opaque subject the request's flag evaluation
  used (`withSubject`, ADR-377), or one passed to `track()`. apid stores
  `sha256(environment_id ‖ subject)`, never the subject itself.
- `properties` holds at most `AnalyticsEventMaxProperties` (10) string
  values. Keys match `^[a-z][a-z0-9_]{0,63}$` and values are at most 256
  bytes. An environment holds at most `AnalyticsPropertyKeysMaxPerEnvironment`
  (50) distinct keys; an event with a new key over that bound is kept without
  that property.
- The SDK sets `flags` itself, with every flag decision the request has made
  so far (flag key → served value as a string). It is bounded by the 32
  decisions a request may propagate.

apid validates the batch as it does today, takes the quota once, and then:

- folds the event into `flag_conversion_daily` for experiments, unchanged;
- and, when the `product_analytics` platform flag is on for the account and
  the plan's `AnalyticsRetentionDays` is above 0, inserts the raw event into
  `analytics_events`.

The fold and the insert share the batch's transaction, so they never
disagree.

### 2. Retention, storage and limits

| Plan | Event retention | Monthly events (shared quota) |
|---|---|---|
| Free | 0 (off) | 0 |
| Hobby | 30 days | 100,000 |
| Pro | 90 days | 1,000,000 |
| Scale | 365 days | 10,000,000 |

These are provisional until the financial model carries an analytics storage
row. They live in `pkg/api/limits.go` with the other plan limits.

`analytics_events` is a plain Postgres table with one row per event, indexed
by environment, name and time, and by account and day for the sweep. The
reconciler that sweeps conversion rows also deletes events older than the
plan's retention, in batches of `AnalyticsSweepBatch` rows. When an account
moves to a plan with shorter retention, the next sweep shortens its history.
At 10 M events a month a Scale account holds about 120 M rows at the end of
its retention. Partitioning or a columnar store is left for when an account
actually reaches that, and needs its own record.

### 3. Queries

Every query is read-only, bounded to the plan's retention and to
`AnalyticsQueryMaxDays` (90), runs under a 15 s statement deadline, and
returns at most `AnalyticsMaxBreakdownValues` (20) breakdown values plus
`(other)`.

- **Event series:** counts and unique units per day or hour, for one event
  name, optionally filtered by property values and broken down by a property
  or a flag.
- **Funnel:** 2 to 6 ordered steps (an event name plus property filters each)
  completed within a conversion window (1 hour to 30 days). A unit enters at
  its first step-1 event in the range and completes step *k* with the first
  matching step-*k* event after its step *k−1* time and within the window of
  its step-1 time. The result gives units per step and conversion from the
  first and the previous step, overall and per breakdown value.
- **Retention:** units whose first start event in the range falls in a day or
  week cohort, and the share of each cohort that sent the return event in
  each following period, up to 12 periods.
- **Requests by variant:** request count, server error rate and p95 latency
  per value of one flag. These come from request telemetry
  (`flag_evidence`), so they follow request telemetry retention, not
  analytics retention.

**Unit.** Queries count either customers or subjects. A subject query ignores
events without a subject.

**Breakdown by flag.** A unit's value for a flag is the one recorded on its
earliest event in the range that carries that flag. A unit with no such
event is `(none)`. Tracking an event in the request that evaluates the flag,
such as a page view, attributes every unit exactly. This is exact for what
each request saw. It is not a re-evaluation of today's configuration, which
would misplace units whose rules use request attributes.

A funnel or retention query reads at most `AnalyticsQueryMaxUnits` (200,000)
units and says so when it is truncated.

### 4. Surfaces

- API, under `/v1/projects/{slug}/environments/{environment}/insights`:
  `GET events`, `POST funnel`, `POST retention`, `GET requests`. All are read
  routes (read scope), behind `product_analytics`.
- CLI: `gregale insight event list`, `insight funnel get`,
  `insight retention get` and `insight request list`. `gregale analytics`
  already names request analytics.
- SDKs (Node, Python, Go): `track(name, {value, properties, subject})`
  attaches the request's subject and flag decisions automatically. The
  capability is `track-v2`; older SDKs keep working without properties or
  flags.

## Consequences

- One instrumentation call serves experiments and analytics, and both share
  the same quota and customer verification.
- Raw events are customer data kept for months. Account deletion cascades
  to them, and a platform tenant's deletion removes its events. Subject
  hashes cannot be reversed to the app's identifiers. Account export does
  not include them yet.
- Postgres carries the volume up to the quotas above. A columnar store is a
  later decision.
- Breakdowns by flag depend on the app tracking events in requests that
  evaluate the flag. Units without such an event are reported as `(none)`.
  The surfaces and docs say so rather than guessing.
