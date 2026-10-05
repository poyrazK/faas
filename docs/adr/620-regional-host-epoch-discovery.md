# ADR-620: Regional host epoch discovery

Date: 2026-10-05
Status: Discovery implemented; permit issuance and runtime activation pending.

## Context

ADR-618 binds a physical permit to one registered compute node and immutable
host epoch. ADR-619 creates that epoch from the host's durable journal and
kernel boot identity. The scheduler needs to discover the current incarnation
before it can construct a permit; it cannot invent an epoch or reuse a node
ID as an incarnation.

## Decision

Expose the current node ID and host epoch through an optional field on the
existing vmmd Stats RPC. Return no epoch for a disabled, closed or poisoned
journal, without making independent-host metrics unavailable. The kernel boot
ID remains private to the host journal. Validate both identifiers as canonical
nonzero UUIDs; refuse malformed wire metadata. Older vmmds remain usable for
independent apps and cannot supply a regional permit destination.

Provide a scheduler accessor that requires the registered compute node ID and
refuses absent or cross-node samples. Return values by copy. Same-boot daemon
restart retains the epoch; kernel reboot changes it. A stale sample may produce
a rejected request, but the receiving host's exact epoch checks prevent it from
authorizing a different incarnation.

Discovery does not establish quota authority, native resource enumeration,
drain, or execution eligibility. Do not activate quota heads or infer admission
from the presence of this field. Scheduler physical permit and serving-seat
issuance, signed compute transport, native resource identity and teardown
verification, legacy inventory and the remaining ADR-619 activation gates are
still required. The complete multi-region feature retains explicit quota
consent, global billing and metering, app orchestration, and serving from all
selected healthy eligible regions by default.

## Validation

Exercise the real journal over the scheduler/server stats transport: daemon
restart retains identity, kernel reboot changes it, and closed storage removes
discovery without failing ordinary stats. Reject absent, malformed and
cross-node samples. Verify poison from uncertain persistence cannot advertise
an epoch. This metadata step performs no VM lifecycle operations; the pending
native Linux x86_64 KVM, test-metal, recovery and leakcheck gates for execution
remain unchanged.
