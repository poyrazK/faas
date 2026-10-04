# ADR-490: Scheduler regional runtime authority

Date: 2026-10-04
Status: Implementation in progress; durable capacity and native qualification pending.

## Context

ADR-489 rejects frozen quota authority at vmmd execution boundaries. The
scheduler can still queue work, allocate rows and attempt warm promotion before
that refusal. Its serving-concurrency ledger deliberately excludes paused warm
capacity, snapshot priming, migration and app tasks. A zero wake cap therefore
cannot represent a frozen global runtime allocation.

## Decision

The local quota-head reader is part of the state Store contract, including
decorators. Scheduler admission consults this durable local reader before wake
coordination, the existing-instance wake fast path, normal or explicit deployment
admission, priming, warm-pool scale-out and promotion, app-task restore resolution
and migration preparation. Migration uses the original durable instance's app
identity rather than trusting a runtime projection. Wake and prime dispatch
recheck after their preparation window. Storage failures propagate; a frozen
stream returns the existing retryable capacity-unavailable problem while
retaining its state sentinel. Independent apps without a quota stream retain
their existing admission rules. No directory request is made on these paths.

Warm-pool scale-in and ordinary teardown remain available. A refused migration
does not pause its source or allocate destination capacity. Scheduler refusal
does not prove that previously queued, booting or running guests have drained.
The vmmd boundaries remain necessary for races after these reads. Acceptance of
a journal stream still freezes all new execution; no allowance activation is
added. Durable capacity reservations, ownership transport to compute nodes
without database access, signed delivery/reconciliation, explicit quota consent,
organization billing and native Linux x86_64 KVM qualification remain required.

## Validation

Verify every admission path rejects an accepted signed quota stream before
instance creation, queue entry, runtime requests or ledger acquisition. Verify
warm residents and migration sources remain visible until teardown, independent
apps still admit, decorated stores retain the head reader, and unavailable local
authority never becomes a full-plan fallback. Run the portable scheduler race
suite and daemon identity contracts. Native lifecycle and leak acceptance remain
required before operational activation or drain-proof claims.
