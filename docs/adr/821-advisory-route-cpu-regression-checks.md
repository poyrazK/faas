# ADR-821: Advisory route CPU regression checks

Status: Accepted

## Context

Aggregate canary CPU can hide regressions affecting a critical route. Route
attribution and deployment-scoped request counts now allow sampled CPU/request
comparisons for explicitly selected routes.

## Decision

Add an optional list of up to ten unique declared method/path labels to regression
options, shared by saved investigations and automatic deployment/canary policies.
Each canary stage pins the policy revision and selected routes. Empty lists
preserve aggregate-only behavior.

Assess each selected route independently using the policy's relative and absolute
CPU/request increase thresholds, minimum observed requests per route/window,
and existing capture coverage requirements. This uses CPU/request even when the
aggregate policy metric is CPU/s. Both increase thresholds must be met.

Missing route samples or request telemetry, sparse route traffic, incompatible
profiles or inadequate collection coverage produce insufficient_data. Never
substitute zero CPU for a missing route. Selected routes outside the bounded
request-mix summary can obtain exact deployment/window/route counts under a
shared optional-read deadline. Plan and telemetry retention restrictions apply.

Retain bounded route summaries and counts with assessments and canary stage
history; copying a completed canary assessment into an investigation also copies
its route summaries. Generate differential flamegraph URLs at read time from the
parent's frozen windows. No new tables or backend series are required.

## Consequences

Route checks are advisory and do not change aggregate outcomes, retry behavior,
advancement or rollback. Collection coverage is not proof of complete request
instrumentation. Inherited goroutine labels and missing telemetry can distort
CPU/request; these observations do not establish deployment causality. A missing
route can require manual investigation even when the aggregate check succeeds.
