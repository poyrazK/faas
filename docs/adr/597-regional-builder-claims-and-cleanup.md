# ADR-597: Regional builder claims and VM cleanup

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

Apps select an immutable compute region under ADR-591. Builder claims previously
used only a temporary preference for the node holding the latest successful
build cache. Disabling that preference or waiting for its grace period to expire
made regional builds claimable across the fleet. Cancellation and timeout cleanup
also used a global queue against each builder's local vmmd connection.

## Decision

Notification and polling claims use one SQLC placement contract. They join the
build's deployment to its persisted app and require an active registered compute
node in the selected region. Cache affinity and account fairness apply inside
that eligibility set. An expired or disabled cache grace never removes the region
predicate. Region admission pauses concern new apps; existing builds can proceed.
Unidentified claim methods select only apps with empty legacy placement.

Claims persist the canonical compute-node UUID in `builds.builder_node_id`.
Provenance continues to use the configured node name. PostgreSQL rejects direct
regional starts without an eligible owner and prevents changing a running or
terminal build's deployment or owner until it has been requeued. Builderd checks
regional ownership before reading customer source or dispatching a VM.

Vmmd checks durable `build-<UUID>` claims before cold boot and warm restore. Its
registered node must own the running claim; regional claims additionally require
an active node in the persisted app region. Missing claims, wrong targets,
cancelled claims and database outages cannot reach the VMM. Production wiring
installs this check on registered compute hosts. Existing non-durable platform
probes and development-session builder identities retain their separate legacy
path; this increment does not give those sessions regional placement.

Every existing cancellation, supersede and timeout transaction captures the
claim's canonical owner when it inserts its durable VM cleanup obligation.
Only that node can claim the obligation, even after becoming inactive. Cleanup
tokens continue to fence late retry results. Cancellation notifications check
the same owner before using the local VM driver. A pending cleanup obligation
prevents another claim from reusing the build ID for a new VM.

Legacy unidentified builds and cleanup rows retain their compatibility path.
An unidentified cleanup row for a regional app is retained for operator repair;
it is never sent to an arbitrary node. Drain existing builder work before
upgrading, then register and configure each regional vmmd and builderd with the
same compute-node identity. An older builder can continue legacy work but cannot
claim new regional builds.

## Validation and rollout

Shared memory/PostgreSQL tests cover notification and polling claims, disabled
and expired affinity, paused admission, inactive and unknown nodes, concurrent
claims, canonical owner persistence, owner-only cleanup and retry tokens.
PostgreSQL tests additionally exercise direct-write and pending-cleanup guards.
Portable daemon tests cover cancellation ownership and both cold and warm vmmd
dispatch before any VM lifecycle work occurs.

This is builder placement and recovery groundwork. Regional source uploads,
remote build-export handoff to imaged, logs, metadata and persistent services
remain separate work. Keep regional compute internal/default-off until the full
build-to-runtime flow, native Linux x86_64/KVM multi-region acceptance, affected
`test-metal` coverage and `leakcheck` are qualified.
