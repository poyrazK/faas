# ADR-498: Advisory production route budgets and saved incidents

- Status: Accepted
- Date: 2026-10-03
- Related: ADR-454, ADR-456, ADR-496, ADR-497, ADR-344

## Context

Canary health requires an in-flight candidate and a serving predecessor. A
promoted release still needs observed error/latency monitoring. Continuous
policy checks cover captured contracts and configured limits; they do not
monitor runtime request health.

## Decision

Add separate versioned, opt-in app intent for exact normalized telemetry routes.
Each route selects an absolute 5xx budget in basis points, a p95 millisecond
budget, or both. An omitted error budget disables errors; zero selects a
zero-error budget. A zero latency budget disables latency. Threshold equality
is healthy. Retain canary intent and behavior independently.

APID periodically evaluates the sole fully serving, default-scope live deployment
with completed canary steps, including deployments that never used a canary.
Preview/other scopes and retired deployment traffic are excluded. Splits and
ambiguous/missing serving contexts are unknown. Two closed minute windows use
the existing 30-second ingestion allowance and publisher weights. Both must start
after configuration, deployment creation, canary stage and recorded rollout
completion anchors. Error windows need 20 represented requests and at least two
errors to confirm an exceeded budget; one over-budget error remains unknown.
Latency needs 100 represented requests and the existing interpolated weighted
bucket p95. Signals must violate independently in both windows. Recovery requires
all selected signals healthy in both windows. This is observed evidence, not an
SLO or full-capture guarantee.

The durable next-check timestamp is app intent owned by APID. Work is bounded
and serialized under account, app and monitor locks in that order. Report reads
use read-only repeatable-read snapshots. Worker evaluations use repeatable-read
transactions, recheck due time and current intent under locks, and commit the
incident, next check and webhook recipient snapshot together. Process restarts
and replicas cannot duplicate a committed transition. Serialization failures
retry from current state. Failed attempts defer their exact due/revision pair by
one minute; the update cannot postpone edited intent or another worker's success. There is no request replay or traffic mutation.

Open one aggregate incident per app/configuration/deployment context on the first
confirmed violation. Retain its original budgets, windows, counts and commit.
Capture at most three violated route/signal diagnostic entries in configuration
order (errors before latency), with explicit omission. Examples use the existing
three-row cap per window; latency uses the existing independent 32-row sample,
100-span parser cap, 16 normalized dependency groups and measured guest/wake
stages. Existing sqlc queries read a single deployment population; shared debugger
validation binds samples to opening windows and counts. Diagnostic candidate
means the monitored deployment; stable is empty and no delta is claimed. Names,
SQL, raw destinations, headers/bodies and customer identifiers are excluded.

Only comparable healthy evidence appends recovery. Unknown, telemetry loss and
entitlement loss leave an incident open. Intent edits/disable supersede an open
incident immediately; a newly fully serving deployment supersedes the prior
context. Neither emits a recovery event. A split retains the prior incident
until a new unambiguous serving context exists. Recurrence after recovery opens
a new UUID. Repeated violations are quiet; the aggregate incident's opening
snapshot does not represent every subsequent failure.

Emit `routes.monitor.violated` and `routes.monitor.recovered` through the existing
app webhook outbox, with app-only recipients snapshotted in the incident
transaction. Payloads are metadata and an authenticated incident path. Existing
signatures, delivery retries and replay apply. Older route-tail CHECK vocabulary
is extended only for replay compatibility with retained new events. The new
migration is additive, replayable and preserves evidence on down migrations.

Read/write APIs require MFA and their existing app-read/deployment-write scopes.
Enabling and saved incident reads require request telemetry entitlement. Disabling
remains possible after a downgrade. History retains the active incident plus the
newest 100 closed entries within 8 MiB; each entry is bounded to 512 KiB. Cursors
are app/account scoped and pruned IDs return not found. Opening metadata remains
readable while retained; following request links rechecks current debugger access
and request retention. CLI response validation and owner-only create-new exports
reuse existing protections. Go, Node and Python expose the typed endpoints.

## Validation

Pure evaluator tests cover strict thresholds, zero/omitted budgets, weighted
count overflow, independent mixed signals, sparse windows and response tampering.
Memory/API tests cover optimistic revision checks, no-ops, scope, MFA, strict
inputs and unknown telemetry. Real PostgreSQL tests cover promotion, method/path
and deployment scope, immutable saved diagnostic evidence, lost telemetry,
recurrence, recipient snapshots, outbox rollback, concurrent/restarted workers,
deployment/context changes, downgrade and retention. CLI and SDK transport tests
share an incident fixture produced by the PostgreSQL evaluator. Migration replay,
sqlc generation, OpenAPI parity, SDK generation and changed-code lint are checked.
