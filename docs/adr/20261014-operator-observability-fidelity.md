# Operator observability fidelity

Status: Accepted, 2026-10-14. Follows ADR-091.

The operator overview, incident inbox, and console must distinguish an empty
observation from an unavailable source. An HTTP success alone does not establish
that the underlying telemetry is available or that a snapshot is current.

The overview loads actual organization, durable audit, failure-kind, rate-limit,
and tenant workload counts. Store errors fail the read instead of manufacturing
zero counts. Audit trace coverage comes directly from the durable audit table;
an observed zero remains zero, and an empty window is one. Prometheus counters
use their emitted `apid_` names. Missing counter series report unavailable.

Incident filtering and keyset pagination happen in a bounded SQL projection
before its limit. Active deployments, active jobs, and unhealthy nodes remain
visible even when they started before the historical look-back window. Recent
failed and dead-lettered jobs are included. A missing Prometheus source is
reported in `unavailable_sources` while available database evidence remains
readable. Each firing alert retains its identity, component, severity, and
activation time; watchdog and info-only rules are excluded from incident triage.
No incident writes or controller ownership changes are introduced.

Per-node wake histograms are grouped by `node_id`, use five-minute increases
for sample counts, and retain the existing `quantiles` response contract.
Unavailable Prometheus data is explicit. The operator console consumes this
contract, displays the five-minute window, uses the backend incident inbox, and
surfaces polling failures and overdue snapshots with their last successful
refresh time. Existing response fields are retained; availability fields are
additive. Deploy the backend before the updated console.

Regression coverage includes the 201-row pagination boundary, severity filters,
failed jobs, node cursors, zero audit coverage, failed tenant reads, per-node wake
series, individual alerts, refresh failure/recovery, and console rendering with
legacy null overview summaries. PostgreSQL and in-memory projections are checked
against the same expectations.

Per-signal health refinement, 2026-10-15: `/v1/admin/obs/health` includes an
additive `signals` map for its two audit counters and firing-alert count. Each
entry carries `state` (`healthy`, `no_samples`, `stale`, or `unavailable`),
`window_seconds`, `max_age_seconds`, an optional underlying `observed_at`, and a
closed reason code. Healthy describes fresh source evidence, not a healthy
metric value. SQL-derived coverage retains its existing 503 failure contract.
The overall `prometheus_available` flag is true only when all three signals are
healthy; healthy values survive another signal's failure. Non-healthy numeric
fields remain zero compatibility fallbacks. Consumers must use the matching
signal state, falling back to the overall flag on older servers.

Counter freshness uses `timestamp(counter)` inside a 15-second subquery over
15 minutes, then the minimum of each series' most recent observation. This
preserves scrape timestamps even when `increase()` produces a fresh output
timestamp from old samples. Alert freshness uses the minimum last evaluation
value of the `faas.rules.yml` rule groups, scraped from Prometheus itself. An
empty `ALERTS` vector is a healthy zero only with that evaluation heartbeat.
Observations older than 120 seconds are stale; timestamps more than 30 seconds
in the future are invalid. Missing observations and insufficient counter
samples are distinct from query failure. Retired series or groups can
conservatively keep the signal stale until the bounded history expires.

All six queries share one three-second budget and the caller's deadline.
No last-good values are cached, and reason codes never include upstream error
bodies. `gregalectl obs health` prints per-signal state, observation time,
window, threshold, and reason; unavailable and stale values are displayed as
unavailable rather than healthy zeroes. Real Prometheus 2.54.1 fixtures pin
underlying timestamps, stopped scrapes, mixed fresh/stale series, missing rule
heartbeats, and the freshness boundary. Domain, HTTP, and CLI tests cover
partial failures, zero activity, missing samples, deadlines, and recovery.
