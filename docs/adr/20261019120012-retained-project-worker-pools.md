# ADR-20261019120012: Retained project worker pools for pinned jobs

## Status

Implemented for outstanding ADR-20261019120011 production job pins. Native mixed-worker
qualification remains pending; ADR-20261019120013 connects a default-off progressive canary admission contract.

## Context

Durable release pins prevent a retry from changing graphs, but the worker
reconciler previously planned one deployment per environment. After cutover it
could drain the old deployment and could not restart it for pinned jobs. Merely
holding existing instances would not recover a failed worker or a restarted schedd.

## Decision

Derive retained worker eligibility from durable pins and the invocation ledger.
A deployment is eligible while its graph is active or unexpired, the deployment
is live and still the pinned graph member, its named production push binding is
enabled and unretired, the owning app is active, and an owned root or descendant
invocation is pending or dispatching. Failed, dead-lettered, completed and cancelled
jobs do not hold a worker. A future-due pending retry does hold one until graph
retention expires. Pins remain available for explicit operator replay, but terminal
pins alone never authorize another resident.

PostgreSQL exposes this predicate through a view shared by target discovery,
instance admission and drain claims. MemStore evaluates the equivalent predicate
under its existing mutex. No pin or deployment intent is written by the scheduler;
schedd remains the only instance lifecycle writer.

Keep the normal active graph pool and create a separate one-replica recovery plan
for each eligible retained deployment. Attribute residents by deployment rather
than assigning both deployments to a single environment target. Resolve each
deployment's own runtime settings before mutation. Recover missing retained pools
before ordinary scale-out consumes the shared budget, in deterministic order.
All demand reads must finish before any fleet mutation; an eligibility read failure
cannot become permission to drain or wake a worker.

Use existing account worker quotas, placement, RAM/CPU budgets and application
concurrency admission. This policy does not increase any capacity limit. The
existing single rollout overlap grant remains bounded and disappears when the
old pool drains. Insufficient capacity can delay recovery or the active pool;
it must never authorize moving a pinned job to a different deployment. Multiple
retained generations are still subject to these shared limits.

Drain claims recheck eligibility under the project handoff lock. Outstanding jobs
protect the last available retained worker. Excess replicas may drain only while
another unclaimed running worker of the same deployment remains. This prevents
retaining an entire former fleet from consuming all account capacity. The retained
plan uses the current active graph target as its drain CAS token, so a concurrent
promotion or rollback cannot authorize stopping an unrelated generation.

Once the final job reaches a terminal state, ordinary reconciliation drains that
retained pool. An operator replay within retention makes the deployment eligible
again. Expired graphs, disabled/retired bindings and inactive apps do not authorize
new retained residents. Existing retired-binding in-flight holds remain in force.

## Validation and limits

Memory and migrated PostgreSQL tests cover pin eligibility, retry holds, admission,
excess-replica draining, last-worker protection and terminal cleanup. Scheduler tests
exercise separate old/current pools, retained-worker loss plus schedd restart,
job deployment identity, bounded overlap, grant retirement and read failures.

Native metal smoke preflight was attempted but requires root on a designated native
x86_64 KVM host. Leakcheck was attempted but cannot inspect this container's missing
native loop devices. These results do not qualify VM execution or prove no native
resource leaks. Keep the existing production worker release admission gate off until
native restart, configuration, job retry, rollback and drain acceptance passes.

This decision does not itself stage graphs or assign traffic percentages.
[ADR-20261019120013](20261019120013-staged-project-canary-publication.md) now connects staged candidate
eligibility, gateway cohorts, checkpoint persistence and transactional effects.
The progressive deploy contract remains behind a default-off qualification gate.
