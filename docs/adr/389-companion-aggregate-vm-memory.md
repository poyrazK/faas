# ADR-389 · Companion aggregate VM memory

- **Status:** accepted
- **Date:** 2026-09-30

## Context

Scheduler admission and billing include main RAM, positive companion RAM
allocations, and host overhead. VM boot and the host fence previously used
only main RAM, leaving companions competing within an undersized VM despite
having their memory reserved.

## Decision

Allocate physical guest RAM as main RAM plus positive companion allocations,
using the same arithmetic as scheduler admission. Zero companion RAM retains
shared-memory semantics. Apply that aggregate to the lease's early host fence
and the post-boot fence; host overhead is added once by the existing cgroup
policy. Keep main and companion workload manifests and guest limits at their
individual allocations. CPU policy is unchanged.

For companion deployments, restore only when the snapshot's logical memory
length equals the requested aggregate guest RAM. Unknown or mismatched sizes
use the existing cold fallback: restoring an older undersized VM cannot grow
its RAM. Single-workload snapshot selection retains its existing behavior.

## Validation and rollout

Portable VM-manager tests cover physical RAM, aggregate host memory fencing,
individual main/companion limits, and cold fallback for unknown/old snapshot
sizes while matching companion snapshots restore normally. Native companion
OOM isolation and park/restore acceptance still require Linux KVM. Deploy
vmmd with the updated guest artifacts; old companion snapshots may incur one
cold boot before replacement. This change aligns runtime allocation with
existing admission and billing rather than introducing new allocations.
