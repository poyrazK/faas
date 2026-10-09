# ADR-733: Scoped application health diagnostics

Status: accepted · 2026-10-05

## Context

ADR-732 introduced an observed health projection. Its readiness summary merges
node liveness and required probe failures, and its app-wide request metrics can
mix preview errors into default-scope health. Warning on every 5xx also obscures
the difference between an isolated failure and a substantial serving failure.

## Decision

Extend the existing read-only API, CLI and console with independent readiness
findings. Each finding includes a stable reason, safe detail, required source,
serving deployment ID, replica ID and recorded evidence time when available.
Node heartbeat and readiness transition times are labelled separately. Durable
ready/unready transitions remain valid without periodic probe heartbeats.
Optional companion probes do not gate the primary route. Known failures still
outrank missing evidence; no raw probe response, host address or backend error
is exposed. Findings prioritize known failures, with deterministic target ordering within
severity, and are capped at 64,
with explicit truncation. Capacity counts still use the complete bounded scan.

Replace the app-wide request health queries with five queries against the
existing bounded per-deployment histogram: request count, 5xx count, release
coverage, oldest release sample time and unattributed request count. Assess only
current traffic-bearing default-scope releases; previous, dark and other-scope
releases are excluded. Every selected release must have current sample presence and sufficient samples
to calculate an increase. Every observed status-class counter needs at least two
samples, preventing a newly introduced error counter from becoming a healthy
zero. Missing release samples, failed reads, invalid counts,
stale samples, deployment-label overflow and pre-routing requests remain
unconfirmed. Unattributed traffic cannot be assigned to a scope and therefore
prevents request confirmation, even when attributed serving-release samples are
available. Never substitute app-wide counters or interpret absent data as zero.
The five-query count is independent of replica count; the deployment selector
is capped at 256 IDs. Existing telemetry cardinality bounds are unchanged.

Expose confirmed request counts, rate, window, assessed deployment IDs and the
diagnostic policy in the response. The five-minute error policy requires at
least 50 requests and 5 server errors: 5% produces a warning and 25% a failure.
Errors below the warning threshold remain visible without degrading overall
health. Observed errors below 50 requests have unconfirmed severity. Observed
successes can confirm the available sample, while no requests remain explicitly
unexercised. These are diagnostic defaults, independent of customer SLOs; they
do not authorize recovery, rollback or any lifecycle operation.

All limits and thresholds live in `pkg/api/limits.go`. The new fields are
additive. Console findings link to the affected release and existing replica
inspector. CLI human output includes targets, sources and evidence times; JSON
retains the structured API response.

## Consequences

Default-scope health no longer incorporates attributed preview failures.
Incomplete telemetry may produce more explicit unknown results, particularly
after a new release or when deployment label admission overflows. The
assessment explains its coverage instead of claiming public reachability.
No database migration, routing change or VM lifecycle change is introduced.
Health transitions, history, notifications, arbitrary environment selection,
public probes and worker/job health remain subsequent slices.

ADR-734 adds independent background observations and bounded app-health history.
