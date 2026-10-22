# ADR-940: Refresh Commit recovery status

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-938, ADR-939

## Context

Resume preserves successful observations so large selections can be inspected
within bounded calls. Operators also need a fresh observation of that fixed
selection and a comparison with the saved delivery evidence.

## Decision

Add CLI --refresh, Go RefreshCommitRecoveryStatus and Node/Python refresh
options. Refresh and resume are mutually exclusive. Validate the same source,
subscriber filter, ordered selection, publication identity and acceptance,
reviewed action and exact saved attempt before reading receipts.

Capture a compact previous_observation for each row and set refreshing to true.
Clear current evidence, timestamps and counters; unread rows become
not_observed. A new refresh takes its baseline from the supplied current
observations, without nesting older baselines. Resume retains that baseline,
reads unread rows before failed reads, and continues the same refresh pass.

Recompute counts, remaining and changes from rows rather than trusting saved
aggregates or differences. Compare status, reason, generation,
generation_relation, attribution, execution_id and execution_state. Each change
includes previous/current values and available observation timestamps.
Timestamp-only updates are not changes. Suppress comparisons when either
observation is not_observed or read_error. Missing or replaced evidence cannot
inherit a stale success. Explicit execution fields describe the selected
retained execution; older observations can derive those fields from their raw
recipient evidence. Selection snapshots remain required.

The original apply attempt never changes: an unknown request stays unknown
regardless of observed delivery progress. Attribution requires a queued
attempt whose acknowledged execution matches retained execution evidence.
Unsupported workflow recipients do not produce execution attribution.

Existing limits remain: 1..50 observation reads, 1..20 recipient pages,
overall timeout and 64 MiB per CLI input. All input values are copied. No
recovery is submitted and no saved action/history URL is followed. Source
ownership is revalidated; Node/Python retain the bounded publication
validation refresh used by recovery status.

## Consequences

Operators can compare fresh evidence over a bounded refresh/resume chain.
Complete remains observation coverage, not delivery success. Local files are
editable samples rather than signed evidence, and timestamps do not represent
an atomic snapshot. Live retention, interruption and delivery-transition
scenarios still require production qualification. No local tests are added
or run for this change.
