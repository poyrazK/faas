# ADR-730: Saved sunset report comparisons

Status: Accepted

## Context

ADR-729 produces sunset queues. Recurring reviews need to identify observed
regressions without confusing overlapping telemetry, changed policy or lost
evidence with migration progress.

## Decision

Add local `routes sunsets diff` for bounded version 1 snapshot files. Require
same app/baseline deployment, monotonic generation/window times, valid operation
and caller identities, and observations inside their half-open windows.

Expose added/missing operations, changed lifecycle dates, URLs, explicit mappings,
contract captures/status and telemetry degradation. Keep before/after rows and
caller observations. Never infer successful migration from an absent row.

New old-route callers, resumed activity and recorded post-sunset requests are
observed regressions. Later requests by callers already using the old route
are evidence updates; they do not prove a return after migration. Claim advisory
aggregate progress only across equal-length non-overlapping windows with
unchanged import/captures/mappings and usable telemetry. Increased successor
usage remains distinct from proof of adoption.

Apply configurable freshness to the current report/window, and to historical
telemetry relative to the historical generation time. CI regression and
incompleteness flags are independent so positive observations do not conceal
incomplete evidence. Emit reports before CI exits and preserve existing files.

## Consequences

Comparisons are local, reversible and require no new server endpoint or schema.
Telemetry remains observed-only. Changes in sampled or lost requests can alter
counts; these comparisons never authorize route removal.
