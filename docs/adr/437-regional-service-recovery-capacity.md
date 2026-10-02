# ADR-437: Regional service recovery capacity

- **Status:** accepted; fleet enable requires native qualification
- **Date:** 2026-10-02
- **Milestone:** Multi-region compute placement
- **Related:** ADR-422, ADR-424, ADR-425, ADR-426

## Problem

ADR-422 counts recovery slots across the fleet. ADR-424 pins a selected app to
one compute region. Capacity in a different region cannot recover that app.
Unrestricted legacy apps complicate the certificate: their healthy replicas
stay on their current hosts during recovery, and accepting pending legacy
intent must leave a safe way to place it without relocating those survivors.

For example, two regions each have two hosts with four slots per host and a
pinned target of four. The fleet has twelve slots after the largest host loss,
but accepting one unrestricted replica is unsafe: placing it in either region
would consume that region's pinned recovery capacity.

## Decision

Extend the durable ADR-422 certificate and serialized admission boundary.
Keep its conservative fleet-wide slot shape, historical resident bounds,
scope and rollout accounting, host eligibility and disabled default. No new
quota or provider provisioning mechanism is introduced.

For each physical region, let `S_i` be a healthy host's service slots after
ordinary workloads, `L_i` its resident unrestricted service replicas, `P_i`
its resident pinned replicas, and `R` the region's pinned reservation.
Healthy survivors are fixed. With `C = sum(S_i)` and `L = sum(L_i)`, current
pinned capacity is `C-L`. The selected-region host-loss certificate is:

```
R <= C-L-max(S_i-L_i)
```

The lost host's unrestricted replicas can use the rest of the fleet; healthy
unrestricted replicas continue occupying their current slots. Require two
healthy hosts for each region with a positive pinned reservation, valid local
residency and no excess replicas. Retain the original global certificate to
cover recovery of unrestricted replicas after a host is lost.

A certificate must also admit a stable placement for all pending unrestricted
intent. For a final regional unrestricted population `T`, each host must have:

```
minimum_L_i(T) = max(L_i, T-(C-S_i-R))
minimum_L_i(T) <= S_i-P_i
sum(minimum_L_i(T)) <= T
```

The first bound ensures that removing any host leaves room for the complete
pinned reservation beside fixed legacy survivors. The second preserves
existing pinned survivors. The final bound determines whether these minima
can fit into the proposed unrestricted population. Normal capacity follows
from the host-loss bound; pending pinned replicas can fill the remaining
slots without moving current replicas. A region without pinned intent only
needs normal placement capacity.

Above the current legacy population these constraints form a feasible interval.
Find its maximum using integer binary search, bounded by `C-R-max(P_i)`.
The fleet's stable unrestricted placement capacity is the smaller of the sum
of regional maxima and the global failover slots minus pinned reservations.
The complete unrestricted reservation must fit this capacity. Unlabelled hosts
participate in an internal unrestricted cell; no public empty region is added.
This is conservative uniform-slot feasibility, not resource bin packing.

## Admission and scheduling

Postgres statement triggers continue locking the singleton capacity policy.
New intent, ordinary resource growth and excess service replicas require a
protected result. While currently protected, freezing another service survivor
must also preserve protection: a pinned placement can block pending legacy
intent even when its host still has physical room. Direct SQL and older clients
are fenced by the same trigger. Node region changes take the policy lock as
well as the existing region identity fences.

During a degraded incident, existing declarations may recover on healthy hosts
without requiring another host-loss margin. An unrestricted placement cannot
reduce a pinned region's current available slots below its reservation.
Regional recovery continues even when another region is short of capacity.
Releases, scale-down and stops remain possible. Recovery never borrows foreign
capacity for a pinned app or moves healthy survivors to make the proof fit.

The scheduler receives separate pinned and unrestricted candidate flags from
the same projection. A candidate is eligible only if freezing that survivor
still leaves a stable placement for pending intent; degraded hints preserve
current regional recovery room. These flags are read-only placement hints.
Atomic database admission rechecks the write after any concurrent mutation.
The projection avoids enumerating placements in production. Resolved owner
and snapshot locality hints survive the placement snapshot; regional capacity
eligibility takes priority over those hints.

## Operator surface and rollout

The existing admin capacity API and CLI retain aggregate fleet fields and add
stable unrestricted reservation/capacity and sorted named-region reports.
Regional reports expose pinned desired/reserved replicas, healthy hosts,
fleet/available/failover slots, unrestricted resident count and stable
unrestricted placement capacity. Private node maps and app/deployment IDs stay
inside the admission projection. A region can be protected while the aggregate
is degraded because pending unrestricted intent has no safe final placement.

Migration `20261002181905649_regional_service_capacity.sql` preserves the
operator's enabled setting. Both migration directions lock the capacity policy
before replacing the certificate, serializing with in-flight admissions and
enable. An enabled fleet can therefore become degraded
when its former global certificate overestimated regional recovery. The same
incident rules apply; upgrade does not release declarations or disable the
policy. Migration replay preserves intent and policy. Downgrade requires
explicitly disabling protection before discarding the regional certificate.

Native Linux x86_64 KVM host-loss/routing tests, admission-throughput
qualification, `test-metal` and `leakcheck` remain required before fleet enable.
One-host recovery is the guarantee; a whole-region failure, independent
regional control planes and storage/control-plane durability require separate
work. This certificate does not change those boundaries.

## Validation

Shared MemStore/Postgres cases cover refusal to enable a sole pinned host
with abundant foreign capacity, same-region peer enable, regional scope and
ordinary growth, pending unrestricted intent without safe placement,
concurrent regional intent, unsafe pinned survivor refusal, same-region
recovery with fixed legacy survivors, foreign-region declared recovery during
an incident and scale-down. A deterministic exhaustive oracle enumerates small
fleets' final unrestricted placements and host losses to verify the binary
solver and candidate hints. Scheduler, API, direct SQL, restart, populated
migration replay and guarded downgrade tests exercise their public boundaries.
Portable results establish implementation behavior; native fleet qualification
is a separate deployment gate.
