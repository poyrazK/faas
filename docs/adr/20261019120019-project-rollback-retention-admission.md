# ADR-20261019120019: Rollback retention admission and native qualification

## Status

Implemented. Native execution remains pending; production admission is unchanged.

## Context

A retained release may be parked with only snapshots and artifacts available.
Rollback must be able to cold boot it with paired configuration when snapshots
are unavailable. Once retention expires, accepting a fresh operator recovery
request gives a misleading acknowledgement for a baseline that cannot be restored.

## Decision

At full traffic, new abort/recover decisions require an existing baseline with a
future retention timestamp. Memory and PostgreSQL enforce this after ownership,
configuration and worker guards while holding their existing mutation lock. The
API rejects an unavailable or expired baseline with HTTP 410 and stable code
`project_release_baseline_expired`, directing the operator to deploy a compatible
replacement. Rejection preserves the checkpoint, request history, production
graph and configuration. Replay of an already accepted request retains its
historical decision even after retention expires.

The independent `project-rollback-retention-only` lane qualifies two real native
scenarios: parked baseline restoration with a measured cold boot after the
existing fresh-restart API invalidates snapshots, and rejection after the actual
retention timestamp expires. PostgreSQL observation queries are read-only. Native
clocks, receipt health, lifecycle state and retention timestamps are not edited.
The report binds the source SHA and log hash and requires both scenarios, final
leakcheck and successful runner completion. CI-safe compilation and persistence
checks are not native qualification.

## Consequences

Recovery acknowledgement now reflects whether the retained baseline is eligible
when the request commits. Retention can still expire afterward; execution must
continue to enforce its existing guard. This does not extend retention, remove
code fences, run SQL recovery, or qualify physical artifact garbage collection.
