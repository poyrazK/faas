# ADR-1523: Durable operator cold-boot recovery

Date: 2026-10-17
Status: Accepted; portable verification passed, native qualification pending

## Context

`force_cold_boot` previously looked up each snapshot tier, marked it stale,
and saved its result separately. Lookup and invalidation errors were dropped.
A delayed capture from the suspect VM could publish after invalidation and
become restorable. Readiness time cannot establish that a VM booted from
healthy inputs: an older restore may finish after the operator action.

## Decision

1. Schedd executes a claimed cold-boot intent in one database transaction.
   It locks the intent and checks the current claim UUID, locks the app then
   deployment, advances its snapshot recovery generation, invalidates all
   non-stale snapshots and replica metadata, clears snapshot-miss backoff,
   saves the exact affected IDs and generation, completes the intent, and
   enqueues its attributed outcome audit. Failures roll everything back.
   Missing or mismatched-account targets receive an atomic failed result
   and audit without changing a deployment.
2. Terminal retries return the original IDs, generation and audit outbox ID.
   The receipt lives on the intent independently of outbox retention. An old
   claim cannot execute or complete after reclaim. Legacy Mark succeeded/
   failed methods reject cold-boot intents, as they already reject restarts.
3. Normal wake and prime bind immutable instance/wake lineage to the current
   deployment generation before invoking vmmd. Restore admission also checks
   that its chosen snapshot is current and usable. A selection that loses to
   invalidation fails before VM dispatch and releases its provisional row;
   a subsequent wake selects the cold path. Rebinding the same admitted wake
   cannot advance its generation.
4. Imaged's source-checked publication inherits that generation, never a
   caller-supplied value. After recovery, an absent, unbound, different-wake
   or old-generation source is stale. Both tiers follow this rule. The
   existing app/deployment parent locks order publication and invalidation:
   publication first is included in invalidation; invalidation first rejects
   the delayed publication.
5. A database trigger rejects non-stale inserts/reactivation at a different
   generation, including legacy direct writers. It locks the deployment with
   NOWAIT to avoid a child-row/parent-lock deadlock for direct updates. Latest
   snapshot queries also require the current generation. Public CreateSnapshot
   cannot claim nonzero lineage; production recovery publication must use the
   source-checked method.
6. Warm-pool and frozen candidates from an older generation are discarded
   through existing lifecycle cleanup. Runtime publication checks their
   generation again under the parent locks, closing the resume/commit race.
   Schedd also avoids knowingly capturing older sources.

Generation zero preserves existing deployments and legacy capture behavior.
After a boundary advances, the first eligible snapshot must descend from a
cold boot admitted in the new generation. Later restores inherit it normally.
Failed boots do not consume the boundary; there is no destructive pop or
readiness timestamp that can silently clear the requirement.

The action changes cache policy. It does not interrupt currently running
instances or cancel boots admitted before the boundary. Such processes retain
their old lineage and cannot repopulate the cache. Operator success means the
policy and audit committed, not that a customer has already triggered a wake.

## Recovery and rollout

See the [cold-boot recovery guide](../ops/operator-cold-boot-recovery.md).
The legacy unary ForceColdBootNextWake now applies atomic generation/invalidation
and propagates errors; each invocation is a new action. Use HTTP operator intents
for replayable execution and completion. No force-park or restart protocol is
expanded by this decision.

## Validation

Memory and PostgreSQL share receipt replay, claim fencing, lineage inheritance,
late readiness, missing-source refusal, direct-writer refusal, concurrent
execution, empty-cache and failed-target cases. PostgreSQL adds write suppression,
deferred commit failures, both publication orderings and downgrade preservation.
Scheduler tests exercise lost acknowledgements, bounded persistence failures,
worker replacement, actual cold/restore selection and warm/frozen bypass refusal.
The HTTP polling response exposes the committed generation without claim tokens.

The shared memory/PostgreSQL recovery suites, restart and operator CRUD
regressions, scheduler/publisher/RPC tests, HTTP polling/action tests and memory
race check pass. SQLC regeneration, OpenAPI parsing, restored-schema comparison,
scoped production/test lint and linked schedd/imaged/apid/vmmd/CLI builds pass.

Native KVM lifecycle and leak qualification remains required before deployment.
This workspace has neither `/dev/kvm` nor staged runtime images; portable tests
do not establish native restore latency, teardown or leak behavior.
