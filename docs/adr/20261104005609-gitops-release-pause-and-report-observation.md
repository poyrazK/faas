# ADR-20261104005609: GitOps release pause and report observation

- **Status:** accepted
- **Date:** 2026-11-04
- **Related:** ADR-20261022140000, ADR-568, ADR-20261103200212

## Context

Empty workload retirement publishes a release separately from candidate graph
activation. It bypassed the platform release pause. Separately, report mode never
finalized committed runtime effects, even after the original fleet became fresh
or cold, so switching to report mode could permanently block source detach.

## Decision

1. Check the platform release pause before both empty and nonempty release
   cutover. Keep the environment unconverged while paused. Continue observing
   runtime work and draining retirements whose cutover already committed.
2. A report worker observes committed runtime effects before planning, including
   when the current definition has drift or blockers. Finalize an effect only
   when its original scope is already fresh. Do not apply customer intent,
   invalidate snapshots, advance freshness boundaries or wake IDs, request
   refreshes, or replay edge-policy effects in report mode. The persisted source
   mode, under the existing source/lease locks, authorizes execution side effects.
3. Keep stale runtime work pending and retain its source-detach fence. Later
   natural drain or fresh runtime receipts allow observation to finalize it.
   Preserve existing enforcement, lease, generation, native qualification and
   serving gates. This adds an optional in-process observer capability without
   changing stored rows or a daemon protocol.

## Validation

Exercise the production backend with MemStore and PostgreSQL. A paused empty
retirement stays planned, publishes no release and leaves admission open;
unpause resumes it. A cutover already committed may finish cleanup under pause.
Report workers can detach after observing fresh effects with matching, changed
or removed desired workloads. Stale residents and snapshots remain pending;
reporting emits neither direct nor durable refresh requests and preserves effect
identity. Existing worker tests verify observation precedes drift/blocked planning,
and existing qualification/retirement tests retain their execution fences.
