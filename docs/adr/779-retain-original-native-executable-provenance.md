# ADR-779: Retain original native executable provenance

Date: 2026-10-07
Status: Implemented locally; native lifecycle acceptance pending.

## Context

The native launch journal retains the original physical lease, regional permit,
resource footprint, generation and PID/start-time incarnation. Recovery also
pins a pidfd, checks the managed UID and cgroup, and binds machine sockets to
that task. Command arguments and a recognizable Firecracker basename do not
establish that the task executes the binary selected by the original producer.
A replacement executable could otherwise supply plausible machine API replies.

## Decision

Before native `startJailer` creates its child, vmmd hashes the selected resolved,
protected executable through a nofollow descriptor. It requires ELF input,
checks stable device/inode, length, mode and modification/change timestamps
around hashing, and synchronizes a versioned per-generation sidecar under the
original owner lock. The sidecar retains the complete prepared owner and exact
source length/SHA-256. A retry cannot change that generation's selected bytes.
It cannot be backfilled after authorization. Jailer copies the binary, so the
source inode is not mistaken for the running copy's inode. The generic launch
gate primitive remains independently testable without reconstructing provenance.

Each scoped native machine API session reads the retained original sidecar.
Producer sessions require it. Once the original task has exec'd, vmmd opens the
trusted procfs `exe` link while retaining the original pidfd/incarnation and
owner lock, compares the actual copy's bytes to the original source, and retains
that descriptor until the session ends. Request boundaries recheck the source
record, descriptor metadata, current procfs executable inode and task liveness.
The binary is hashed once per fresh session; no long-lived authorization cache
is introduced. Replacement, in-place mutation, malformed records and unrelated
owner bindings refuse before further machine requests. Retirement still uses
its original task handle and does not require executable provenance.

Read-only inventory can still observe historical tasks with missing source
records, but marks them unresolved and supplies no executable identity.
Known source records with mismatching bytes are not treated as historical.
Fresh inventory exports an optional executable observation; omission preserves
old wire representations and cannot qualify an advisory regional hold match.
Its copied value is independent of the consumer's payload. Original generation
sidecars survive retirement/replacement for host-lifetime recovery evidence.

This is original executable provenance, not complete physical inventory,
guest/run authority, row deduplication, capacity release or regional activation.
External root mutation and natural task exit cannot be frozen by observation.
Hashing cost and actual jailer copy behavior still need dedicated native x86_64
KVM qualification. Regional heads remain frozen. Escaped helper inventory,
original run/task row binding, capacity reconciliation, native capture/migration,
shared authorization/billing, regional provisioning/stores/replication, cached
ingress serving all selected healthy eligible regions, and native fleet failure
acceptance remain in the end-to-end scope.

## Validation

Portable fixtures model source/copy byte identity, retained owner binding,
identical retries, forbidden backfill, missing historical records, bounded strict
decoding, cancellation, replacement, mutation, task exit and descriptor closure.
Synthetic ELF bytes are identity fixtures, not executed Firecracker binaries.
Linux procfs/pidfd diagnostics are distinct from native KVM lifecycle acceptance.
