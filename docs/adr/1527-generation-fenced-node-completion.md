# ADR-1527: Generation-fenced node lifecycle completion

Date: 2026-10-17
Status: Accepted; implemented and verified with portable transaction tests

## Context

RecoveryRunner read remaining instances separately from its lifecycle completion
write. Completion checked only node ID and lifecycle. A delayed sweep could
therefore complete a later drain/recovery cycle after the lifecycle returned to
the same value. New work could also arrive between the check and the write, and
the completion audit was best effort after the transition.

## Decision

1. Each compute node has a separate, positive lifecycle generation, initially 1.
   A database trigger advances it on lifecycle changes and on changes to drain
   or recovery initiation timestamps, including same-state cycle restarts.
   Metadata-only writes preserve it; callers cannot reset or assign it. This is
   independent of the existing release/configuration generation. MemStore mirrors
   the same rules for every lifecycle writer.
2. Schedd completes a drain or recovery with the generation and lifecycle read
   before its sweep. The transaction acquires the existing capacity-policy lock
   before the node lock, validates that cycle, and rechecks completion eligibility
   against current instance rows while instance admission/state writers are
   serialized by the same capacity lock. Drain requires zero resident instances,
   including warm/frozen rows; recovery retains the current rule that migrating
   or snapshotting work prevents completion.
3. Lifecycle transition, immutable cycle receipt and attributed completion audit
   handoff commit together. Receipts are keyed by node ID and source generation
   and retain the original lifecycle, resulting generation, timestamps, counts
   and outbox ID independently of node deletion and audit-outbox pruning.
   Concurrent controllers and lost commit replies replay the original result;
   a stale uncompleted cycle cannot mutate the current node or emit completion.
4. Legacy unfenced completion setters refuse mutation. The database also rejects
   draining/force-draining to maintenance and recovering to active without the
   matching transactional receipt. Ordinary operator activation from unavailable
   or maintenance remains available. Receipt replay precedes current node checks.
5. The audit outbox owns persistent delivery. Existing recovery SSE/metrics may
   observe a newly committed completion without appending a second event. Live
   fan-out remains best effort; receipt replay never emits a duplicate completion
   audit. VM migration/recreation policy and RPCs remain with their current owners.

## Rollout and recovery

Quiesce old recovery schedulers before migration and resume only upgraded writers.
Existing nodes begin at generation 1 and upgraded sweeps reread them; old unfenced
completion writes are rejected by the database guard. No pre-upgrade cycle is
invented as a proven receipt. Downgrade refuses while completion receipts or
advanced generations exist. See the
[rollout guide](../ops/node-lifecycle-completion.md).

## Validation

Shared memory/PostgreSQL tests cover stale same-state cycles, generation changes,
metadata-only writes, completion preconditions, concurrent controllers, immutable
replay and audit retention. PostgreSQL adds node/receipt/audit write failure and
deferred-commit injection, admission/completion serialization, direct-writer
guards, schema parity and downgrade preservation. Scheduler tests pause old
sweeps across later cycles and inject lost commit replies and persistence failure.

These targeted suites pass, together with memory-store and recovery-runner race
detection, existing heartbeat/operator recovery regressions, event/statetest
regressions, scoped production/test lint, SQLC compile/diff and restored-schema
comparison against fresh migrations. Nothing deployed. Native VM evacuation
qualification remains a separate fleet acceptance gate.
