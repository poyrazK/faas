# ADR-933: Retire dead same-boot instance records after verified absence

Date: 2026-10-10

Status: Accepted. Amends ADR-472 (restart quarantine) and extends ADR-478 (prior-boot spare retirement).

## Context

Every resource-journal record quarantines its slot for the lifetime of the vmmd
that recovers it (ADR-472), and no path retires a dead instance record. Every
rollout restarts vmmd, so records of guests whose process died across a restart
build up for good. After rc.251, production-us fsn-2 carried 13 such records,
quarantining 13 of its 32 slots, and fsn-3 carried 11. Their materialised
snapshot files and layer clones stayed on disk, and one of them later matched a
reused inode and wedged a build VM's cleanup (PR #4411).

ADR-478 retires only prepared spares from an earlier kernel boot. It notes that
path absence alone cannot rule out a same-boot setup child, a renamed link or a
held namespace.

## Decision

Before journal reconciliation installs quarantine, vmmd retires a non-prepared
instance record only when all of the following are true:

1. The record carries process provenance, and that process is gone: its PID is
   absent, or names a process with other start ticks or another kernel boot.
2. No current process holds the slot UID in any of its four UID fields. This
   rules out setup children that are still running.
3. No slot-addressed link, namespace alias, jail or guest argv names the slot
   or instance (the ADR-472 inventory and the ADR-478 absence check).
4. No link has the recorded veth's ifindex and MAC address, which catches renamed
   links. No process network namespace and no nsfs mount has the recorded
   namespace inode, which catches held namespaces.
5. Every recorded jail and bind path is absent.
6. Every asset is of a known kind and checkpointed. An uncheckpointed veth or
   namespace, or an uncheckpointed temporary file that is still present, keeps
   the record.

A retirement removes the record's materialised and clone files only where the
path still names the recorded inode. A path that names another file is left
alone. After that, under the journal lock, vmmd checks that the record is
unchanged, unlinks it and fsyncs the journal directory. If files cannot be
removed, the record stays. If the unlink fails, startup fails. If the host
inventory cannot be read, every record is kept.

The retired count appears as `reclaimed_dead_records` in the existing startup
log and the recovery report.

## Boundaries

This path never deletes links, namespaces, jails, mounts or processes, never
adopts guests, and writes no scheduler or ledger state. Records whose kernel
resources survive, such as a guest torn down mid-cleanup, stay quarantined. The
same applies to records without process provenance and to any record that
matches a held identity, including a namespace inode the kernel has reused.
Verified physical teardown of such leftovers remains pending.

## Validation

Portable tests cover retirement with identity-verified file removal and slot
reuse, a reused file path that is left in place, and ten hazards that keep the
record quarantined:

- live process
- slot UID held
- veth name present
- renamed link present
- namespace held by a process
- namespace held by an nsfs mount
- jail present
- no process provenance
- uncheckpointed namespace
- unreadable mountinfo

A read-only dry run of these rules against the production-us journals on
2026-10-10 retires 12 of 16 records on fsn-2 and 7 of 13 on fsn-3. The kept
records are live guests, one wedged build VM whose jail is still present, and
held or uncheckpointed namespaces.
