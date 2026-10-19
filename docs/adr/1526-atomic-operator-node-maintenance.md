# ADR-1526: Atomic operator node maintenance

Date: 2026-10-17
Status: Accepted; portable verification passed

## Context

Node drain, force-drain, activation and retirement changed compute-node lifecycle
before saving the operator result. A scheduler crash could leave an applied drain
running until reclaim; if recovery had already reached maintenance, replay then
reported failure. Legacy completion also accepted an intent ID without checking
which scheduler claim owned it. Outcome audit delivery was best effort.

## Decision

Schedd executes a node intent in one transaction. It locks the intent, validates
its nonzero claim token, locks the capacity-policy singleton then the target node,
rechecks lifecycle and retirement
preconditions, applies the transition, saves an immutable execution receipt,
completes the intent and enqueues the attributed outcome audit. Any write or
commit failure rolls everything back. Missing targets and invalid transitions
receive a durable failed result without changing the node. Retirement rechecks
live instances and protects default-local; retired nodes cannot be reactivated
by force-drain.

Receipts record the original target, kind, claim, previous/resulting lifecycle,
whether a transition occurred, result, error, completion time and audit handoff
ID. A terminal retry with its original claim returns that receipt before reading
current node state. Later maintenance cycles, target deletion and audit-outbox
pruning cannot change it. Reclaim clears the claim, fencing the old worker before
either mutation or completion. Legacy Mark succeeded/failed methods reject node
intents; a database guard also rejects terminal node writes without a matching
receipt and protects completed receipts/results from mutation.

Success acknowledges the lifecycle request, not completion of VM evacuation.
RecoveryRunner remains responsible for migration/recreation and for reaching
maintenance. Existing node drain-status and operator-intent polling retain their
contracts. This adds no VM RPC or public API field, and no new receipt TTL.
The existing audit-outbox delivery loop publishes outcomes after commit.

## Rollout and recovery

Stop old schedd dispatchers before migration and resume only upgraded writers.
The migration quarantines all unfinished legacy node intents as outcome_unknown:
even a pending row could have been reclaimed after an applied lifecycle change.
These rows require operator reconciliation and are never automatically replayed.
Already-terminal legacy history is preserved. Downgrade refuses to discard
receipts or unresolved node intents. See the
[node maintenance recovery guide](../ops/operator-node-maintenance.md).

## Validation

Shared memory/PostgreSQL tests cover lifecycle policy, durable failures, original
results after drain completion and later cycles, reclaim fencing, concurrent
workers, audit attribution and outbox retention. PostgreSQL additionally injects
node, receipt, audit, intent and deferred-commit failures, checks legacy-write
guards and migration quarantine/downgrade. Scheduler tests cover lost commit
replies, bounded persistence failure and worker replacement.

The shared memory/PostgreSQL suites pass, including suppressed writes, deferred
commit failures, both reclaim orderings, and capacity lock ordering with policy
enabled and disabled. Scheduler integration and existing operator-recovery
regressions, memory race detection, scoped production/test lint, SQLC parity,
restored-schema parity and ADR/link checks pass. No deployment was performed.
These are database execution guarantees; existing native migration/evacuation
qualification remains a separate fleet rollout gate.
