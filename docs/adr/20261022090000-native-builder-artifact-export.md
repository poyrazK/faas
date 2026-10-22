# ADR-20261022090000: Native builder artifact export

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** ADR-568 ("Native artifact export now explicitly refuses the legacy
  mount/copy path; its destination authority and complete producer coverage
  still need implementation")
- **Related:** ADR-003, ADR-20261021080847

## Context

A builder VM writes `build-done.json` and `/build/out/*` to its writable
drive and powers off. vmmd then loop-mounts the drive and copies those files
into the export directory that builderd named when it created the builder.
Under native process recovery that step refused to run, so every source
build on a native host failed at teardown with "artifact export producer
provenance is not implemented". That blocked source-built GitOps workloads
and any host that turns on `native_process_recovery`.

The legacy copy is unsafe under native recovery for three reasons:

- the mount is not journaled, so a vmmd that dies mid-export leaves a loop
  device and mount that recovery cannot attribute;
- the writer is vmmd itself, an in-process producer, and recovery has to
  prove that the old incarnation has exited before it removes what that
  incarnation was using;
- root writes into a path chosen by another daemon.

## Decision

1. **Producer enrollment.** Every native builder lease gets a version-2 launch
   record whose producer is the preparing vmmd incarnation, under protocol
   `builder-export-v1`. The record is written before any native effect.
   - Validation accepts this protocol only for builder leases that are not
     networkless.
   - Launch, staging and helper gates accept only that same incarnation.
   - Retirement after a restart waits for the recorded incarnation to exit,
     as it already does for repair producers.
   - Startup repair reconciliation counts only repair-protocol producers.
   - Builder records need no repair parent.
2. **Export at retirement.** `DestroyWithExport` on a native host waits for
   the guest to exit and then retires the VM with the export directory.
   Retirement does the following, in order:
   1. revoke the record and confirm exit;
   2. check producer retirement;
   3. retire host helpers and device namespaces;
   4. export;
   5. retire loop mounts, TUN binds and image sources;
   6. remove the chroot and cgroup.

   An export error is returned only after every resource has been released,
   as on the legacy path.
3. **Journaled read-only mount.** The export opens a loop-mount frame marked
   `read_only` on the exited builder's canonical drive. The frame is valid
   only while the owner is revoked, exit-confirmed, a builder, and enrolled by
   the incarnation now acting, and it is durable before the loop is attached.
   The mount is `ro,noload,nodev,nosuid,noexec`, so untrusted build code
   cannot make root replay a journal. A filesystem that rejects `noload`
   mounts plain read-only. The session always unmounts and retires the frame
   before it returns. If vmmd dies in between, the existing retirement path
   removes the frame once the producer has exited. Frames without `read_only`
   decode as before.
4. **Destination authority.** vmmd uses only the export directory that
   builderd supplied when it created the builder, which vmmd holds for that
   instance. Before writing as root, vmmd checks that the directory:
   - is absolute and clean, and is not `/`;
   - lies outside the jail tree and the native journal;
   - has a parent reached through real directories only;
   - is either created by vmmd or found empty.

   Ownership is then handed back to the parent's owner and group, as on the
   legacy path. A builder recovered after a restart has no remembered
   destination, so its export is still refused and builderd retries the build.

## Consequences

- Source builds and source-built GitOps workloads can run on native hosts.
- A crash mid-export leaves no unattributed kernel state. The partial export
  directory belongs to builderd's existing failed-build cleanup.
- A vmmd release older than this one cannot decode export frames that carry
  `read_only`, and refuses to start rather than guess. Such a frame exists
  only while an export is in flight, so rolling back right after a crash may
  need the newer vmmd to start once first.
- Non-native hosts keep the legacy export unchanged.

## Validation

- Portable tests (`pkg/fcvm/native_build_export_test.go`):
  - destination authority: relative, unclean, root, inside-jail,
    symlinked-parent, missing-parent, populated and non-directory
    destinations are refused;
  - builder producer validation;
  - startup repair reconciliation ignores builder producers;
  - export frame decoding, including older frames without `read_only`.
- Linux tests (`pkg/fcvm/native_build_export_linux_test.go`, loop fixture):
  - the frame is durable before the reader runs;
  - the mount is read-only, the files are copied and the frame is retired;
  - unenrolled builders and application VMs are refused before any loop is
    attached.
- The n2 native source-Job journey exercises the real builder export end to end.
