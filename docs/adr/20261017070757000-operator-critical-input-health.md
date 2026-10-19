# Operator critical telemetry input health

Status: accepted

## Context

Definition coverage and successful rule evaluation can coexist with empty metric
inputs. Existing compute discovery gauges describe the producer's last registry
snapshot; `up` describes configured scrapes. Neither establishes every intended
target's identity or health. Traffic counters can legitimately be absent at idle.
Spec section 12 defines telemetry; section 17 and Appendix D do not authorize an
automatic deployment-role inference or changes to VM ownership or acceptance.

## Decision

Add a private read-only `/v1/admin/obs/input-health` endpoint and `gregalectl obs
input-health`, using the existing operator read scope, MFA, allowlist and rate
limits. Require an explicit optional `expected_role` selection. Without one,
advertise `single-box-v1` and `control-plane-v1`, return unknown and read no source.
These are operator-selected expectations, not evidence of installed topology.

Both roles require a minimum of one apid, public gateway (`gatewayd` scrape job)
and control-plane schedd target. Single-box also requires one vmmd target.
Control-plane adds gatewayd-internal, schedd-compute and vmmd with expected counts
from fresh apid compute discovery evidence. Extra static targets are allowed;
this minimum does not detect every missing replica. Dynamic counts also cannot
prove membership: an unexpected target can replace a missing target numerically.

Read all four discovery gauges (returned targets, registry nodes, omitted invalid
targets, last successful refresh), require one consistent producer series, fresh
scrape timestamps, a fresh nonzero refresh timestamp, nonnegative integral counts
and returned plus invalid equal registry nodes. Reject multiple producers rather
than aggregate conflicting snapshots. Account for default Prometheus job-label
collision: select `job="apid",exported_job="<compute-job>"`. A nonstandard label
mapping remains unknown. Invalid omitted targets degrade discovery independently
of scrape coverage. Missing, stale or invalid discovery is unknown, never zero.
A fresh zero registry is a legitimate idle expectation, not a missing daemon.

Pin all Prometheus queries to one evaluation time. Strict vectors reject partial
warning/info results and malformed envelopes or samples. Values and original
`timestamp(up)` observations join by complete target labels, excluding only the
metric name. Distinguish fresh successful scrapes, fresh failed scrapes, stale
scrape evidence, missing minimum targets and unexpected dynamic counts. Reject
duplicate identities, malformed timestamps and non-boolean up values. Unknown
expectations do not conceal observed failed scrapes or manufacture missing counts.

For each job inspect one vetted always-instantiated daemon readiness gauge with
the exact owning daemon label: apid, gatewayd_public, gatewayd, schedd or vmmd
prefixes. Only sample presence and freshness are checked; readiness value zero
is a valid input. No traffic counter is required. Check every fresh up target
independently; a fresh sibling cannot hide a missing or stale sibling. Join by
the complete scrape labels minus metric name and selected daemon label. Recover
original sample timestamps using a ten-minute subquery at fifteen-second steps.
Missing means none found within that bounded lookback, not never emitted. Queries
are diagnostics for this small explicit gauge set, not all alert AST inputs.
No fresh up targets means unknown metric health, except a known zero expectation
with no observed targets, where checks are not applicable. Down targets remain
visible in independent scrape evidence.

Use a shared three-second budget, at most six fixed concurrent job readers, each
bounded by strict Prometheus vector limits (eight MiB, 4096 series). Completed
jobs retain evidence when another source fails. There is no last-good cache.
Return aggregate counts only; no target addresses, raw labels, queries, source
URLs or error bodies. Root and job status prioritize known degradation; otherwise
mixed known and unknown evidence is partial. Definition, evaluation, readiness,
notification and input health remain distinct. No database, alert rules,
deployment configuration, collector, frontend or rollout changes.

Synchronize canonical and embedded OpenAPI, private SDK exclusions, DTO scans and
generated Node types. The CLI validates role/job/gauge inventories, closed reason
codes, evidence/count partitions and aggregate states before any text or JSON.

## Adversarial review

An installed catalog or scrape series cannot establish intended topology: use
explicit profiles. A zero refresh at boot cannot establish an empty registry:
require fresh successful discovery. Two apids cannot silently sum expectations:
multiple sources stay unknown. Last-scrape success cannot hide old original
samples: query timestamp inside the lookback, pin evaluation times. A successful
scrape cannot prove readiness: accept both readiness gauge values as telemetry.
Missing one sibling cannot be hidden by aggregation: compare full target labels.
Discovery counts cannot prove membership; keep that limitation explicit. Historical
removed targets may appear in lookback but cannot satisfy a different current
target identity. Prometheus outages cannot reuse cached healthy evidence.

## Validation

Test both explicit roles, no-selection/no-read behavior, partial source failures,
timeouts, malformed/duplicate evidence, fresh/stale/future samples, per-target
gaps, idle zero registries, omitted invalid targets, multiple producers and the
default exported_job wire mapping. Exercise actual local Prometheus scrapes and
queries, operator authorization/MFA and strict queries, CLI output and malformed
responses, OpenAPI/Node generation, existing observability regressions and lint.
