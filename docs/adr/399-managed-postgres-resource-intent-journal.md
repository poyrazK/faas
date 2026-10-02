# ADR-399: Persist vmmd lease intent and process incarnations

Date: 2026-10-02

Status: Accepted; native lifecycle acceptance pending, customer cutover disabled

## Context

ADR-398 quarantines observed restart survivors without inventing ownership.
Observation alone misses a lease whose partial resource creation left no visible
process, and cannot distinguish a reused PID from the original guest. ADR-395
retains resource identity through failed cleanup only in the running Manager.
A replacement needs durable provenance before ownership recovery is possible.

## Decision

Linux vmmd opens an exclusive, private resource journal before restart inventory,
prepared-network allocation or RPC service. Default storage is
`/var/lib/faas/vmmd-resources`; `resource_journal_dir` in vmmd.toml can select
operator-provisioned persistent storage. The generated service provisions both
the failure outbox and resource journal with StateDirectoryMode=0700. Override
directories need a writable persistent location and an existing parent hierarchy;
they are not automatically added to the service's filesystem permissions.

After lease allocation and early request validation, commit lease intent before any
new network setup, artifact staging or guest launch. Prepared-network adoption
can precede the intent write; those existing links retain ADR-398 observation
protection. Store allocator-derived identifiers, plan/cgroup selection and boot
policy. Do not store environment, commands, credentials or artifact contents.
Live CPU quota is mutable policy and does not form a cleanup ownership identity.

The real JailerVMM captures kernel boot ID, PID and /proc start ticks immediately
after cmd.Start, before the child watchdog can reap it. This closes the PID-reuse
gap even for a child that exits immediately. Publish the checkpoint before boot
returns. A checkpoint failure fails the boot through its normal confirmed
cleanup path. A subsequent launch with the same intent requires the previous
recorded process incarnation to be absent before touching boot resources.

Writes use private temporary files, file fsync, atomic rename and directory
fsync. Initial opening also fsyncs the existing parent directory. An exclusive
flock prevents two daemon writers. Reject symlinks, foreign owners, extra links,
nonprivate files, unknown versions/fields, oversized records, inconsistent
allocator identity and duplicate slots. Interrupted uncommitted temporary writes
are discarded; committed records are never discarded because they are old.

The original Manager retires a record only after confirmed VMM/artifact/cgroup
and network cleanup, then fsyncs removal before releasing its allocator lease.
A failed intent/checkpoint or retirement commit retains ownership as needed for
cleanup retry. An uncertain unlink remains pending in memory until a later
successful directory fsync. Physical cleanup already completed before removal;
an absent record is not used to acknowledge an unknown guest stop.

## Restart boundary

Inventory reserves every journal lease slot and instance ID, including intent
with no process and a missing or mismatched process. Process matches additionally
require kernel boot ID, start ticks, instance argv and actual/destination UID.
Different boot, reused PID, UID or instance ID never establishes a match.
Unreadable required process provenance fails startup before allocator mutation.
Logs expose journal record and process-match counts separately from observations.

These matches are provenance, not reconstructed lifecycle ownership. Journal
records do not populate live/pending-cleanup maps or authorize recovered failure
report replay. Quarantine remains held for this Manager's lifetime. The journal
does not yet record mounts, materialized artifacts, namespace incarnations or
cleanup receipts for a replacement daemon. Serving/routing/watchdog recovery,
verified restart cleanup, ownership epochs and scheduler reconciliation remain
pending. Legacy binaries or lost journal storage retain observation-only
limitations. Customer cutover stays disabled.

## Validation

Portable regressions cover exclusive access, concurrent persistence/reopen,
write-before-network ordering, failed fsync and confirmed retirement/retry,
live CPU policy changes, intent-only quarantine, process incarnation mismatches,
corrupt/ambiguous storage and configuration defaults/override.

The metal regression boots a real guest with a committed process checkpoint,
reopens journal storage, verifies process provenance in a fresh Manager, rejects
unowned teardown and boots a second guest with another slot/UID. Only the original
Manager retains watchdog/mount/artifact ownership for fixture cleanup and journal
retirement. It does not simulate a full daemon crash, power loss or recovered
serving. Supported native x86_64 Linux KVM and leakcheck remain release gates.

Source provenance, test scope, logs and recovery limitations are recorded in the
[internal-node evidence](../ops/evidence/20261002-managed-postgres-resource-journal/README.md).
The nested diagnostic passed 21 selected top-level tests (55 including subtests),
three leak checks, bounded Linux race regressions and changed-code Linux lint.
Four complete portable package race suites also passed. Native lifecycle and
filesystem power-loss qualification remain pending.
