# ADR-477: Durable prepared-network intent and alias handoff

Date: 2026-10-03

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-476 fences live host-link cleanup, but prepared spares have no durable
record. A daemon crash during creation or after namespace alias movement can
leave an unrecorded reservation or lose the intended guest identity. Startup's
name-based spare reaper cannot establish resource ownership.

## Decision

Introduce version 5 records for prepared networks; continue reading versions
1–4 conservatively. Older binaries reject version 5. Commit the spare's exact
network-only slot lease before physical setup, then journal namespace and veth
intent/checkpoints through the existing guarded creation path. A spare has no
plan, VM memory or process identity and consumes no VM admission. It may contain
only its namespace and primary host veth. Execution-only wakes bypass the pool.

One UUID-qualified spare identity remains the record's permanent filename key
throughout its lifetime. The record progresses through these states:

1. **Spare:** the lease belongs to the source and has no guest state.
2. **Transfer intent:** commit a target identity before binding the new alias or
   detaching the old one. Retain the source lease and creation checkpoints.
3. **Guest:** after Wake validates and stamps its full lease, check the original
   live namespace and host-link observations. Atomically replace that same
   record with the guest lease, new alias mount checkpoint and unchanged nsfs
   inode/veth provenance before staging, policy retarget or guest launch.

Atomic replacement of one stable file avoids a duplicate-slot record pair and
an unlink/rename gap between source and guest filenames. Directory fsync remains
mandatory. The in-memory cache follows a successful rename even if directory
fsync fails, while the caller fails closed and retains cleanup ownership.
Guest asset additions and process checkpoints preserve version 5 and its source.
Reject duplicate slots, overlapping source/target identities, malformed metadata,
incorrect filenames, incomplete transfer checkpoints and spare guest launches.

Retire the stable record only after guarded physical removal and confirmed
directory sync, then release the reservation. Failed creation, transfer,
adoption or retirement retains the slot when cleanup cannot be confirmed. A
Wake validation failure before guest adoption resolves the pending target record
and retires it after cleanup. Cleanup of an unmoved spare uses its source identity
even if the allocator already adopted the slot.

## Restart and boundaries

Restart inventory quarantines the slot, source and any committed target even
when no physical resources are visible. It never reconstructs live observations
from the journal or returns recovered spares to the ready pool. Journal-enabled
daemon startup skips the name-based prepared reaper; survivors require verified
reconciliation. Legacy injected wiring without a journal retains its old path.

This closes the durable identity gap during handoff, including the dual-alias
window, through conservative quarantine. It does not implement automatic
restart reclamation, serving recovery or an atomic physical alias transaction.
Power-loss durability of nsfs marker/bind operations remains unqualified.
ADR-476's privileged-mutation/index-reuse limitations remain. Complete peer/TUN,
loop and parent mount incarnations, jail staging, snapshot publication and
all-node drain proof remain pending. Customer activation stays disabled.

No schema, RPC, quota, dependency or deployment environment changes are required.
`vmmd` remains the sole physical resource owner; `schedd` retains lifecycle and
ledger ownership.

## Validation

Portable regressions exercise intent ordering, spare admission, normal/custom
port Wake/Destroy, early validation and networkless bypass, fsync failures and
cleanup retry, stable record publication, malformed/overlapping records, launch
fencing and restart quarantine with no observable physical resources.

Linux metal diagnostics kill a separate journal-owning process at spare,
transfer-intent, dual-alias, moved-alias and committed-guest checkpoints with
real nsfs/veth resources alive. Reopening must preserve those resources, reserve
the slot and identities, and refuse destructive recovery. Results are recorded
in the [handoff evidence](../ops/evidence/20261003-managed-postgres-prepared-handoff/README.md).
The internal GCP node uses nested virtualization; supported native x86_64 KVM
acceptance remains pending.

Final nested diagnostics passed 71 selected top-level tests (212 including
subtests), five real process-crash checkpoints, full Linux/macOS race suites,
three lifecycle leak checks and two supplemental leak checks. Changed-code lint,
egress and generated deployment checks passed. Native acceptance remains pending.
