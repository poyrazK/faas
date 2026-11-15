# ADR-20261113210904: Node SDK build and deployment waiters

- **Status:** accepted
- **Date:** 2026-11-13
- **Milestone:** SDK developer and agent integration
- **Amends:** [ADR-20261113173610](20261113173610-node-sdk-client-isolation.md)
- **Related:** [ADR-20261113202200](20261113202200-node-sdk-request-options-and-diagnostics.md), [ADR-20261022120744](20261022120744-release-page.md)

## Context

Developers and agents can submit work and list its history, but still implement
status polling themselves. Builds have explicit lifecycle states. Deployments
have a server-authored release timeline that distinguishes rollout, scheduled
release, approval, holds, watch completion, rollback, and supersession. A release
can stay serving after a recorded regression when automatic rollback cannot run.

## Decision

Add waitForBuild and waitForDeployment on isolated public and preview
client.deployments. Observe only existing resource IDs through generated GET
methods. Capture one client configuration and copied policy at invocation.
Preserve existing generated methods, return types, and legacy static services.

Require an explicit positive total timeout. Use one monotonic budget and owned
abort controller across header resolution, reads, HTTP retry waits, poll delays,
and asynchronous progress callbacks. Poll immediately, then use capped
exponential jitter with configurable positive delays and optional maxPolls.
Configured HTTP retries count within one poll; HTTP failures otherwise propagate
unchanged. The explicit waiter budget overrides client timeouts on status reads.

Build success is succeeded; failed/cancelled are terminal failures. Deployment
success requires phase released, status live, positive traffic, and no recorded
watch_regressed event. Building, scheduled, rolling_out, and watching continue.
Holds and approval waits stop with a blocked outcome by default; callers may opt
to keep waiting. Failed, cancelled, rolled_back, superseded, not_serving, and
regressed stop observation. This release condition does not imply full traffic
or application-specific health. Unknown states and invalid or mismatched
resource responses fail closed without overwriting the last valid observation.

Expose WaiterError with typed failure code, resource ID, poll count, and a deeply
frozen last observation. Timeouts use the TimeoutError name. Progress callbacks
receive separate frozen snapshots and poll/time metadata, and are awaited.
Exceptions and caller abort retain identity. Race the whole loop against abort
so a stalled read or progress promise cannot prevent timely completion. A late
callback cannot schedule further work after stop. Retire listeners and timers on
every exit. Resource cancellation remains a separate API operation. Resume by
starting a new waiter with the retained resource ID.

The scoped transport now owns its response reader directly. A TransformStream
pipe can stall cancellation under backpressure when aborted after headers but
before decoding begins. Owning the reader permits immediate body cancellation
in this race, while preserving legacy raw-response behavior.

## Consequences and verification

Automation can react to completion, failure, and intervention requirements with
bounded observation work. Polling is read-only; no resubmission, promotion,
approval, rollback, or resource cancellation is performed by a waiter.

Public/preview and packed-package tests cover typed results, immutable progress,
terminal/blocked states, watch regressions, resuming by ID, unknown states,
explicit budgets, HTTP error identity, Retry-After, capped jitter, callback
deadlines, cancellation before/during headers and bodies, poll limits,
configuration rotation, timer/listener cleanup, and concurrent HTTP accounts.
Shared transport regressions pin cancellation before decoding starts.
