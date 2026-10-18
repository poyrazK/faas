# Command-cron admission operator observation

Status: accepted

## Context

App-task diagnostics begin after a command is materialized. They cannot show a
stalled cron scan or an unconsumed schedule boundary. Spec section 12 permits
private diagnostics; section 17 and Appendix D do not authorize changing
schedule execution or policy to satisfy an alert.

## Decision

Read all command crons in the scheduler ownership scope through an optional
SQLC-backed read interface and a single-lock MemStore reader. Include disabled
and suspended intent explicitly; exclude HTTP crons. Exact owner placement and
legacy local fallback match the corresponding scheduler listing/ownership
rules. Never sum overlapping replicas. No execution interfaces are widened.

Classify ten bounded cohorts using canonical timezone/DST parsing and current
account, deployment, overlap and binding evidence. Compute only the first
unconsumed nominal boundary after cursor or creation, never enumerate catch-up
history in the observer. Disabled, suspended, account-held and not-due ages
remain unknown. Invalid/future/nonfinite timing, policy, schedule and binding
evidence cannot establish age. Age is schedule history, not continuous
eligibility or proof the command should execute. Overlap, deployment and lane
facts do not predict the actual deadline or admission disposition.

Observe independently before loop LISTEN/recovery: immediately, then every 30
seconds with a three-second context budget and 120-second freshness from read
start. Register bounded zero cohorts only after successful reads. Failed/stale
observations withdraw counts/ages; shutdown withdraws expectation. Scrapes do
no I/O and release locks before emission. Centralize these budgets in api.

Instrument actual cron enumeration/owner lookup progress (including empty
scans), duration, pre-admission/legacy decisions and occurrence-store returns.
Only committed returned dispositions count as queued, exclusive pending,
coalesced, overlap skip or missed deadline. Stale CAS, rejection, cancellation,
deployment unavailable and error remain separate. Deployment unavailable does
not assert the separate suspension write succeeded. Admission evaluation lag is
returned CreatedAt minus ScheduledFor after commit, including policy history.
The current stores return evaluation time; PostgreSQL row creation time can
differ. This is not a database commit timer. Invalid lag increments
unknown rather than observing zero. No payload, IDs, command/output, arbitrary
returned status, business key or error text becomes a metric label.

Add three recordings, six internal warnings, fixtures, a runbook and generated
operator catalogs. Complete fresh cohorts and initialized counters/gauges gate
workload/progress alerts. Intentional inactive schedules do not trigger delay
warnings. Unknown telemetry is distinct from a known empty backlog. Preserve
all execution calls, timezone/DST behavior, catch-up caps, CAS, retries, overlap,
exclusive fencing and VM lifecycle order.

## Consequences and adversarial review

The reader covers the full scoped backlog and must be monitored against its
budget; no bounded scan cost is promised. Go parsing can check context between
rows, not interrupt an individual parser call. Counts expose only current
observed facts; no migration invents class-entry history. Bound lane readiness
is deliberately not predicted. Metrics are private diagnostic evidence, not a
delivery or execution SLO. The shared cron sweep includes inline HTTP work, so
a stalled scan alone cannot identify a command runner failure.

Review exercises memory/PostgreSQL parity, empty and over-page-size backlogs,
ownership and legacy unplaced scope, disabled/suspended/account holds, canonical
DST boundaries, actual committed policy dispositions, stale snapshots,
replacement cancellation, nonfinite/future evidence, scrape independence,
freshness, cancellation, registry lifecycle and alert holds/missing evidence.
Only instrumentation/read paths change. Native VM acceptance is not claimed.
