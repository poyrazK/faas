# ADR-496: Route regression investigation

## Status

Accepted — 2026-10-03.

## Context

Route and customer health identify sustained 5xx or selected 4xx increases.
Existing debugger inspection exposes retained request evidence and traces, but
developers must manually reconstruct the deployment, time, route and identity
filters connecting these surfaces. A generic lookback can select evidence from
another stage or customer and misrepresent the detected comparison.

## Decision

Add a read-only live investigation API, CLI and typed SDK method. Require one
configured exact method/path and either all 5xx or one watched 4xx code. Compute
the aggregate report, optional directly selected customer finding and request-row
inventory in one read-only repeatable-read transaction. Reuse the immutable
deployment pair, closed windows and stage/configuration anchor. Preserve existing
rate thresholds and optional latency evidence; this version does not select
latency-only or individual 5xx signals.

Customer UUID selection explicitly exposes that UUID. Validate ownership within
account/app scope, allow revoked consumers, and filter recorded identity columns.
Never resolve request history through current membership. Compute the selected
cohort directly so the normal top-20 report cap cannot hide its investigation.
Existing aggregate query callers use empty identity filters and retain their
original behavior. No migration or telemetry ingestion change is required.

Count matching publisher weights and rows before output bounds. Return at most
three metadata rows per deployment/window, preferring trace-linked rows, then
newest timestamp and descending UUID. Return explicit truncation, weighted count,
row count and evidence availability. Keep collapsed rows intact. A trace reference
is not a claim of retained spans or individual traces for all represented requests.

Link each example to the existing authenticated debugger evidence path. Do not
embed raw span/log content, customer names, unselected IDs, headers, payloads or
URLs. Following links rechecks retention and authorization. CLI reads validate
scope, finding verdicts, window alignment, weighted inventories, bounds and exact
evidence paths before rendering or writing a new owner-only JSON artifact.

## Consequences

Customers can move from a rate regression to relevant production examples with
one command, including a cohort outside the usual bounded report. Existing
debugger dependency/trace views remain the evidence inspection surface. The
investigation captures a live comparison, is advisory and changes no rollout,
replay, recovery or saved decision state. Unknown or pre-anchor findings may
still have examples; coverage remains observed_only and does not establish
causation or complete capture. Investigation artifacts retain references, which
can expire after export.
