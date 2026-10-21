# ADR-20261019120011: Durable project worker release pins

## Status

Implemented in claimed production worker delivery. ADR-20261019120013 adds a default-off progressive canary admission contract; mixed-worker native qualification remains pending.

## Context

Named production queue jobs can reach delivery without a release header. Resolving
the active graph on every attempt moves such a job to a different graph after a
deployment. ADR-20261019120010's progressive policy needs a durable first-admission choice.

## Decision

Persist a graph/member pin on the job's ledger-owned replay root before forwarding
its first claimed production worker delivery. A PostgreSQL transaction locks the
owning project, verifies the live invocation lease, locks the ledger-derived root,
and resolves the retained graph before writing the immutable pin. Concurrent
resolvers read the same decision. The lease is checked again before commit.
MemStore implements equivalent behavior under its existing mutex.

The pin is operational data, excluded from environment clones. It contains only
root invocation, application, binding, graph and deployment identities plus creation
time. It stores no job payload or configuration values. Root deletion cascades to
the pin; graph and deployment references cannot be deleted while the pin exists.
Pins do not extend public graph or deployment retention: expired or unusable targets
fail closed rather than select today's graph. Operators must restore the retained
target or explicitly decide how to recover work that outlives the retention window.

Ordinary retries and in-place queue dead-letter replays reuse the pin across
attempt/replay-generation changes. A fresh controller/store process reads the same
PostgreSQL record. Replay root identity comes from storage, never the dispatch
envelope. Existing canonical release propagation keeps managed calls on that graph
and existing deployment configuration snapshots provide its configuration.

Conflicting release/revision headers, stale leases, foreign application/account or
binding identity, retired/disabled bindings, and missing roots cannot replace a pin.
Explicit enqueue-time graph headers retain their original intent. Explicit revision
pins continue through the existing revision resolver. Work without an active graph
keeps the existing unversioned behavior; this change does not manufacture a graph.

The named queue replay API currently reuses the same invocation row. Generic cloned
replay admission does not accept bound queue work, and this decision does not expand
that API. Root-keyed storage is prepared for an owned cloned-replay path when one is
introduced; it does not itself authorize such admission.

## Consequences

This adds one transactional pin lookup to claimed production project worker delivery,
but does not change enqueue, standalone apps, HTTP workers, private environment work,
or synthetic workflow/operation authority. No VM lifecycle writer changes here.

ADR-20261019120012 now adds retained-worker restart and drain protection for outstanding pins.
[ADR-20261019120013](20261019120013-staged-project-canary-publication.md) now adds staged graph/checkpoint persistence,
cohort routing and transactional controller effects behind explicit qualification gates.
Existing worker release admission remains disabled by default.
