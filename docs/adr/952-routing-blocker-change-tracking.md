# ADR-952: Routing blocker change tracking

## Status
Accepted.

## Context
Exact diagnostics and freshness controls show current routing evidence, but an operator needs to understand how confirmed blockers changed between samples without confusing routing with handler execution.

## Decision
Add `comparison`, `previous_observation` and `changes` to recipient diagnostics. The previous observation is a compact, acceptance-bound sample containing its exact query and observation time, known routing state and, when available, waiting reason, recorded capacity scope, target availability and ordering predecessor identity/state. No event envelopes, counters, moving ages, leases or links enter this comparison record. Keep only the latest pair, with a fixed set of ten comparable fields, rather than accumulating history.

Compare only `available` or `not_waiting` observations with the same source, event ID, subscription and precise acceptance guard. Require a strictly increasing observation timestamp, preserving nanosecond precision. Compare status and routing state across both known statuses. Compare waiting reason, capacity scope, target availability and predecessor source/ID/subscription/acceptance/state only when both observations are available. Equivalent timestamp encodings and subscription casing do not produce ordering identity changes.

A first sample or invalid prior baseline reports `baseline_unavailable`. Missing, replaced, failed or inconsistent current evidence reports `evidence_unavailable`. Equal or decreasing timestamps report `non_monotonic_timestamp`. These cases emit no changes. Evidence gaps break comparison continuity; the next confirmed sample establishes a new baseline. This avoids treating missing evidence as blocker removal. An available-to-not_waiting transition describes departure from routing wait only, never handler completion.

On resume, bind saved selections and attempts first, validate the compact previous query and shape, remove unknown fields, and recompute comparisons rather than trusting saved change arrays. Retained samples preserve the latest valid comparison and both timestamps, but are never reported as new changes. `diagnostics_progress.compared` and `changed` count newly sampled comparable recipients and newly changed recipients in the current pass. Ordinary results include the fields; CLI watch prints changed recipients and per-pass counts, suppressing repeated notifications for the same observation pair.

## Consequences
The record remains bounded to one previous sample and ten possible changes per recipient. Existing batch budgets, coverage, freshness, checkpoints, receipt change tracking and settlement semantics remain independent. No backend endpoint, SQL, migration, compliance registry or new service action is introduced. Comparisons are observations from polling, not a complete history or proof that readiness persisted between samples.

An explicit diagnostic refresh begins a new baseline, consistent with existing refresh behavior; ordinary resume preserves the latest validated comparison.
