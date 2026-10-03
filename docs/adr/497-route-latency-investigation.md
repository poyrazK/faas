# ADR-497: Route latency investigation

## Status

Accepted — 2026-10-03.

## Context

Route health already identifies relative p95 slowdowns and absolute latency
budget violations. ADR-496 investigation selects failing responses, so slow
successful requests and their dependency evidence require manual debugger
filters. Trace retention and collapsed request rows limit what those samples
can establish.

## Decision

Extend the existing read-only investigation with `signal=latency`, requiring a
configured relative check or positive p95 budget. Reject a nonzero status filter.
Preserve version 1 and omit additive fields for existing error selections. SDKs
omit the new signal filter when callers use the existing default investigation.
Use the selected latency verdict and all HTTP statuses, including successful
responses. Keep the exact candidate/stable pair, windows, request-time customer
scope and full weighted route p95 in the same repeatable-read transaction.

Return at most three slowest request examples per side/window. Independently
select the newest 32 retained rows per side/window for diagnostics, including
rows without spans. Parse at most 100 spans per row with shared debugger helpers.
Group by normalized dependency type/kind only; exclude names, statements,
attributes and destination identities. Compare weighted nearest-rank retained
span p95 and exclusive p95, subtracting overlapping direct children. Suppress
exclusive percentiles when retained timing is incomplete or spans are capped.
Rank by positive comparable p95 change, then candidate p95 and type/kind, and
return at most 16 groups. One-sided measurements have no delta. Each group has
at most three request references per deployment for authenticated inspection.

Guest p95 uses measured rows with publisher weights, including measured zero.
Cold-boot counts use request weights. Wake boot p95 counts distinct wakes once
per side/window, using scheduler boot events with the same wake ID, app ID and
recorded instance ID as the selected deployment's request rows. Bound event
lookup to 24 hours before and 30 seconds after a request; require an ordered
complete pair no longer than 24 hours. Missing or pruned evidence remains absent.
No current instance or customer membership join rewrites historical attribution.

CLI validation binds counts, sample limits, deltas, measurement presence and
example paths to the selected context before output/export. Go, Node and Python
SDKs expose the additive typed fields. No migration or telemetry writer changes
are needed. Shared debugger parsing and timing behavior remain unchanged.

## Consequences

Customers can connect an observed route slowdown to retained dependency and
execution evidence with one command, including one affected customer. Full route
p95 and recent sample percentiles describe different populations; neither they
nor stage/dependency percentiles are additive or causal attribution. Missing,
partial and capped samples stay explicit. This feature changes no rollout,
recovery, request replay, saved decision or history state.
