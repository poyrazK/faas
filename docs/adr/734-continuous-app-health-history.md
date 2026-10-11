# ADR-734: Continuous application health observations and history

Status: accepted · 2026-10-05

## Context

ADR-732 and ADR-733 explain current serving evidence when a customer reads the
health endpoint. Incidents between dashboard visits leave no application health
record. A saved healthy assessment must not imply continuous health during a
collector outage. Route-health history records rollout decisions; application
health needs independent observations without changing rollout decisions.

## Decision

Run a bounded background collector in apid, the existing control-plane runtime.
Extract evidence collection into `pkg/apphealth` so on-demand and background
reads use the same evaluator, safe diagnostic fields and metrics entitlement.
The collector reads recorded state and Prometheus; it never probes customer
endpoints, wakes workloads, changes replicas, or changes traffic. Request and
service execution modes are eligible; deleted apps and worker/job modes are
excluded. Structural observations remain available on every plan.

PostgreSQL claims one eligible due app at a time using `SKIP LOCKED`, with a
30-second durable lease. Completion checks the exact lease token, start time,
ownership, execution mode and expiry; a replaced or expired worker cannot
publish. A completed assessment schedules the next check at least 30 seconds
later. Each process checks at most 20 apps per sweep, sleeps one second between
sweeps, and gives each claim/read/persistence attempt a 10-second deadline.
The interval is a target spacing, not a fleet-wide delivery guarantee. Missing
app/account reads or persistence failures never publish a successful sample;
leases expire and allow retry. Collection outcome and duration metrics have
bounded labels and no customer identifiers.

Atomically retain the latest full assessment and append only a baseline or a
meaningful change: status, phase, serving/latest releases, check/finding status,
reason, target, omitted findings or diagnostic policy. Moving counters, prose,
evidence timestamps and ordering do not themselves create events. Full evidence
is retained on each event; the latest assessment advances even without an event.
Every accepted sample is at least as new as its claim and strictly newer than
the previously completed sample. JSON inputs are bounded before persistence.

If the previous sample expired before the next accepted observation, append a
`gap` at the previous expiry time, with unknown health and no invented evidence,
followed by the new observation even if its status is unchanged. A gap is an
evidence expiry boundary, not a newly performed probe or a confirmed outage.
The history read preserves original timestamps and reports collection freshness
independently of the health status. Stale latest assessments never become fresh
merely because somebody reads history. On-demand reads do not write history.

Retain the newest 100 entries within 4 MiB of encoded JSON per app; entries are
at most 64 KiB. Reads exclude entries older than 30 days. Completion prunes
count/byte/age excess, and each collector sweep deletes at most 100 expired
entries globally, including history of apps whose workload mode changed.
Latest assessment metadata remains separately bounded. Account/app deletion
cascades remove both tables. API history uses account-scoped, repeatable-read,
newest-first cursor pages. Missing, foreign, aged and pruned cursors return 404.

Expose `GET /v1/apps/{slug}/health/history`, CLI `app APP health --history`,
generated SDKs, and an expandable console timeline. Historical targets link to
existing inspectors; those targets may no longer exist. The UI labels times as
observations or expiry, hides stale collection confidence, and provides a fresh
pagination start when a cursor expires. It does not infer deployment causality,
exact incident duration, uninterrupted recovery, uptime or an SLO.

## Consequences

The history starts with observations collected after this feature is installed;
older snapshots are not reconstructed. Two tables and one forward-only migration
are added; no VM, scheduler or routing lifecycle changes are introduced. Fleet
load or dependency outages can delay collection and leave explicit unknown gaps.
Notifications, active public probes, arbitrary environment selection, worker/job
health and automatic recovery remain separate subsequent slices.
