# ADR: Durable worker-pool recovery

Date: 2026-10-17
Status: Accepted
Supersedes: [ADR-1501](1501-worker-startup-reconcile.md)
Related: [ADR-420](420-continuous-service-recovery.md)

## Context

Worker boot retries use process-local failure counts and timers. The one-time
startup sweep repairs a missing timer, but an admission refusal that creates no
instance produces no event to retry. Capacity can return while a worker pool
remains empty. Queue-driven pools also need their existing environment, zero
floor, cooldown and account-budget rules preserved.

## Decision

Persist one `worker_recovery` row per reconciled app, with a desired revision,
failure count, next attempt, expiring app lease and random claim token. Discover
active owned worker apps, captured worker environments and unfinished worker
cleanup on startup and every five seconds. Exclude suspended and abuse-held
accounts and inactive apps. Past-due grace remains eligible.

Use the existing scoped worker planner and admission path. The lease coordinates
event, queue and periodic reconciliations across upgraded schedulers. Claiming
does not consume a database connection during a VM operation. An expired lease
can be reclaimed; only its current token can complete before lease expiry.
Existing atomic worker reservations and node-admission fences still protect
capacity, including direct admission and mixed-version callers.

A failed reconciliation waits five seconds, doubling to 300 seconds. Counts
are bounded at 32 and survive restart. Individual RUNNING notifications do not
reset a failing app's count. A successful full pass resets failures only after
checking the current resident fleet against the scoped plans. Healthy apps are
due every 30 seconds; starting/draining apps every five seconds. Missing demand
or other state reads retain a retry rather than authorizing scale-in.

Events may claim during cooldown to apply scale-in, including a zero floor.
They cannot admit during cooldown, increment failure counts repeatedly or move
the retry deadline. A partial-scope success cannot clear another environment's
retry. Changes to desired app configuration, scaling policy, ownership or live
deployment generation reset cooldown; transient queue samples and failed
instance identities do not. Captured settings remain pinned to deployments.
Stopping one managed instance leaves the app's desired capacity in force;
park the app or reduce its desired demand to keep the pool stopped.

Each attempt is bounded to 600 seconds. The recovery work kinds have one sweep
slot, eight app slots and a 32-app discovery batch. Saturated submissions remain
unclaimed and due for another sweep. Limits live in `pkg/api/limits.go`. Remove the memory
retry timers and one-time startup path.

## Rollout and evidence

Apply the additive migration before upgrading schedd. Older binaries ignore
the ledger and retain their old retry behavior until upgraded. Rollback leaves
the additive table in place; do not down-migrate while upgraded schedulers run.

Portable tests exercise restart without events, repeated boot failures, lease
contention/expiry, stale completion, saturation, environment scale-in, zero
floor and existing admission safeguards. Shared MemStore/PgStore contracts
cover discovery, claim fencing and persisted deadlines; the scheduler restart
scenario also runs against PostgreSQL. Native KVM recovery qualification is
separate from these deterministic scheduler/store tests.
