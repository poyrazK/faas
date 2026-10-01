# ADR-395: Retain VM ownership until teardown is confirmed

Date: 2026-10-02

Status: Accepted; customer cutover activation remains disabled

## Context

ADR-394 fences new boots and resumes while a durable app fence is held. A drain
also needs trustworthy stop acknowledgements (spec §6.2 and §11). Previously,
Manager deleted the live identity before stopping Firecracker, swallowed cleanup
errors and released the slot even when Kill failed. JailerVMM discarded its
process record and continued cleanup after a watchdog timeout. A second stop
could acknowledge an instance whose first stop had not finished. A failed boot
had no live entry from which to retry cleanup.

## Decision

Register a per-instance stop reservation before cancelling and joining boot or
resumable operations. Keep it through teardown. Other stop requests wait for the
owner, or return their own deadline error; they cannot acknowledge early. Boots,
resumes and captures reject a reserved instance. Park participates in the live
operation registry so Destroy also joins a capture that ends in teardown.

Keep the live instance, CID association and allocator lease until cleanup
succeeds. Retain a separate cleanup identity containing the lease, network and
workload names, including when an unsuccessful boot never reached the live map.
A failed cleanup remains retryable through Destroy and blocks boot/resume reuse
of that identity. Retried cleanup uses the original plan, builder scope and
network ownership. Release the allocator slot and remove the manager identity
under the same manager lock so a new occupant cannot lose its CID association.

A successful SIGKILL syscall is not an exit receipt. JailerVMM waits for its
single process watchdog and surfaces a missing watchdog, failed Wait, kill error
or watchdog timeout. It retains the process record until exit and resource
cleanup are confirmed. Image unmounts, jail removal, materialised file removal
and cgroup removal surface failures; failed mount/file registrations remain
available for retry. Repeated deletion tolerates already-absent resources.
Manager checks that the owned namespace and public/private host veth identities
are absent before releasing the slot. Partial network setup and already-absent
resources can make teardown commands fail without constituting a leak.

Cleanup after failed boots and Park survives the caller's cancelled context.
A failed restore must confirm Kill before cold-boot fallback; otherwise Wake
fails with the cleanup error and retains its lease. Destroy and graceful stop
surface both the operation error and any cleanup error. A failed artifact export
can return an error after the dead builder's cleanup succeeds and releases its
lease. Process-exit fallback uses the same stop owner rather than deleting live
state separately, and expected teardown exits do not trigger liveness failures.

Builder interruption is deliberately separate: Stop interrupts the child without
waiting behind a Destroy that is waiting for export. The existing export owner
retains the record and resources until its own cleanup finishes. An interruption
response is not a drain acknowledgement.

## Limits and acceptance

These are in-process ownership and teardown guarantees. They do not recover
surviving guests after vmmd crashes, enumerate an entire fleet, persist drain
receipts, attest node capabilities or fence stale owners after admission reopens.
An unknown instance response cannot establish that a restarted node has no
source writers. No new durable drain state, RPC capability receipt, binding
activation or snapshot invalidation is added. schedd remains the sole instance
state writer; vmmd remains the sole VM/resource owner. Customer activation stays
disabled until the controller can establish durable ownership and all-node drain
proof. External SQL sessions and writes after the restore point require their own
cutover policy. Some existing scheduler watchdog, liveness and OOM paths still
release their ledger or publish terminal state around best-effort destruction.
Those rows are not drain receipts; retaining scheduler RAM/concurrency accounting
through failed teardown remains a follow-up prerequisite.

Portable regressions cover concurrent stops and waiter deadlines, rejection of
resumes/boots during teardown, failed live/boot/job/Park cleanup and retry,
restore fallback ordering, missing/failed watchdog receipts, retained failed
file/cgroup removal and builder interruption during an owned export. Existing
boot/resume cancellation, graceful stop and builder export tests remain required.

The metal regression leaves a real Firecracker guest running after an injected
stop failure, checks retained network/live/lease identity and blocked resume,
then delays a retry to check concurrent-stop deadlines before confirmed cleanup
and leakcheck. Native x86_64 Linux KVM acceptance remains required by CLAUDE.md
and spec §14; nested internal-node diagnostics cannot satisfy that release gate.

[Internal KVM diagnostic evidence](../ops/evidence/20261002-managed-postgres-confirmed-teardown/README.md)
records 606 passing package/batch checks, 20 fixture/platform skips, the explicit
baseline capacity-case exclusion and four passing leak checks. This nested node
run does not replace the supported native acceptance gate.
