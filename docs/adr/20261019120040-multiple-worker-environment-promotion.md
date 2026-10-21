# ADR-20261019120040: Multiple-worker environment promotion

## Status

Implemented locally; native KVM qualification remains pending. Production flags
remain off. The user cannot provide a native host at this time.

## Context

Environment promotion previously supported exactly one push consumer. Applications
with separate email, billing and webhook workers could not promote their complete
release together. Existing graph publication and job pins already cover every
member; the remaining single-worker assumptions were in policy capture, health,
startup holds, restoration and migration recovery.

## Decision

Allow up to `ProjectPromotionMaxWorkers` (initially eight) workers per verified
production environment promotion. Each worker retains the existing restrictions:
one enabled production push binding and its owned consumer, tested source snapshot
or current runtime proof, matching worker execution/drain settings, no worker-owned
release command, sidecars, pull consumers, job workers or work policies.

Capture the complete set, sorted by app identity, including every binding/consumer
hash, retained deployment, drain policy and source qualification. Bind this set to
the approval token and immutable destination health policy. Single-worker operations
retain their existing JSON `worker` and `worker_health` fields and hash shape.
Multiple-worker operations use `policy.workers` and `workers[]` progress objects;
they omit the singular fields. Duplicate identities/bindings, mixed representations,
partial sets, and unsupported configurations are rejected.

Every candidate worker receives its own durable startup hold after any migration
succeeds. Each worker must have a current configuration/startup proof and sufficient
healthy retained deliveries before cutover. Publication selects the complete graph,
configuration and new-job admission atomically. Existing/retried jobs retain their
original graph/deployment pins. Verification reads disjoint attributed delivery
windows for each worker. A healthy subset cannot produce a successful release.
A qualified HTTP or worker regression may restore the full graph only while all
retained worker baselines are healthy; restoration waits for every retained worker
to start. Candidate uncertainty never becomes a healthy round.

PostgreSQL derives holds from every immutable worker policy. Publication locks
selected runtimes and requires a transaction-local, generation-bound all-worker
startup guard. Older controllers cannot publish or restore a worker set without
that guard, erase its policy, or downgrade the view while receipts remain.

Migration execution remains single-owner and precedes all candidate workers. Resume
restarts preflight with the complete policy. Replacement resolution rechecks all
worker consumers/startup proofs and collects each worker's before/after-request
ledger windows. Its immutable private receipt stores every worker proof; one
missing or unhealthy worker keeps resolution pending. No SQL is replayed and the
original task outcome remains unchanged.

New multi-worker admissions require the separate signed `worker_set` scope and
reviewed `worker-set-cutover`, `worker-set-rollback`, `worker-set-recovery` gates,
in addition to worker/coordinated/service-graph scopes as applicable. The existing
worker boot flag governs this scope; existing one-worker approvals cannot silently
authorize a worker set. Recovery of accepted intent remains independent of new
admission switches. No runtime lifecycle owner changes or new boot flags are needed.

## Validation and limits

Portable memory/PostgreSQL cases exercise three workers, each with its own consumer,
both with and without migration. They cover every startup hold, missing final-worker
startup/baseline/delivery evidence, consumer drift, full rollback, missing retained startup,
independent queue pins, immutable policies, resume, replacement recovery and durable
per-worker receipts. API/CLI/SDK contracts cover legacy and set representations;
rollout checks require explicit new scope. Database tests reject older-controller
cutover/restoration, and API retries preserve accepted intent after admission is
disabled. Real VM execution and final leakcheck
remain pending. Existing single-worker native reports do not qualify the added
worker-set gates; those require the explicit multi-worker runbook scenarios.
