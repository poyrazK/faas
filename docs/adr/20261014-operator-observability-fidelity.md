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
