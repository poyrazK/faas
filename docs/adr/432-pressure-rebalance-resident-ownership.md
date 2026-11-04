# ADR-432: Keep resident app ownership during pressure rebalance

Status: Accepted
Date: 2026-10-02

## Context

ADR-087's cheap pressure-rebalance path is a parked-only ownership transfer.
The peer-to-peer live handoff is not implemented on this path. Nevertheless,
`RebalancePressuredApps` changed `apps.node_id` without checking instances or
holding the app admission lock.

The production load investigation captured `e2e-probe` transferring from
fsn-2 to fsn-3 at 2026-10-02 17:29:00 UTC while resident instance rows existed.
Its prior owner can retain admission reservations after the new owner parks
those instances. The two schedulers have independent ledgers. A persistent
ledger/database discrepancy was observed, but its complete set of entries
has not been recovered; this change does not claim to explain every refusal.

## Decision

The cheap pressure path acquires the same per-app lock as wake, prime and
park before reading ownership and instances. It transfers only apps with
no instances or with every instance in PARKED, STOPPED or FAILED. Any other
state, including a resident VM placed on a peer or account-deletion cleanup,
keeps the app on its current owner. A failed instance-inventory read also
keeps ownership unchanged and returns an error.

This applies to all retained pressure-policy values until a real live
handoff is available. The existing `no_eligibility` metric reports a blocked
transfer. No quota, migration operation or VM teardown is introduced.

The engine lock serializes the single app owner's lifecycle operations;
the existing conditional ownership update remains the stale-owner race
guard. A boot outside the app lock has already inserted its resident row.
Terminal states are monotonic for these instance IDs, so a parked row is
not silently reused as a new resident VM after the check.

## Consequences and validation

Transient pressure no longer moves live ownership without transferring its
accounting. Resident apps wait for capacity or park through their current
owner. Parked apps can still transfer to a peer with headroom.

Regression coverage crosses every instance state, owner/peer placement and
pressure policy; it also checks inventory failure and concurrent admission.
The same tests fail on the prior implementation. Linux/KVM checks run on
the user-designated GCP internal test node are diagnostic qualification;
the repository's dedicated native-host acceptance remains a separate gate.

This prevents new unsafe transfers. It does not reset already-stale ledgers
or establish the cause of the independent Node/V8 function crashes.
