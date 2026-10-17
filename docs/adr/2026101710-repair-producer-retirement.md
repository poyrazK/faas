# ADR-2026101710: Daemon-bound producers for repair retirement

- Status: accepted for internal qualification; native execution evidence pending
- Date: 2026-10-17
- Scope: new Linux networkless selective-repair cold boots

## Problem

Confirmed Firecracker exit does not prove that boot, provisioning or other host
producers have stopped. An old vmmd can have in-process filesystem work, while
its gated child helpers can survive daemon death. Previous native generations
therefore retain their resources after restart when producer ownership is unknown.

## Decision

Enroll the fixed repair cold-boot path in a version-two native launch record.
Publish the original daemon PID, UID and kernel start time atomically with the
physical generation before registering a local producer. Bind that same identity,
physical generation, kernel boot and lease into the private repair task journal.
All fields are required, bounded and strictly decoded. Version-one frames remain
unchanged and acquire no new recovery authority.

Only the original private repair context can stage drives, open a VM launch gate
or start a child helper for this generation. These gates require the completed
task/physical binding and original daemon identity. A crash between physical
publication and task binding can be inventoried but cannot produce a helper.
The incoming repair identity cannot be replaced or used by compatibility staging.

Out-of-process effects continue to use the existing gated jailer/helper launch,
exclusive cgroups, image-source, loop-mount and TUN/device journals. In-process
producers belong to the original daemon's kernel incarnation. Recovery holds
the native daemon ownership lock and uses a Linux pidfd to prove that incarnation
has exited. It never signals a daemon PID. A different kernel start time proves
the old incarnation has gone without granting authority over the reused PID.
Read failures, changed credentials and a still-live original process are unknown.

After VM revocation and confirmed exit, cleanup requires the daemon proof before
retiring helper cgroups and resource references or deleting the jail and cgroup.
Existing exact-inode and generation checks remain authoritative. Only complete
resource removal produces a durable acknowledgement and releases the original
slot. Lost responses replay that acknowledgement; they never rerun the VM.

## Boundary and qualification

This protocol covers repair cold boots. Native builder export remains disabled,
and ordinary or older unenrolled native generations retain their cleanup guard.
It does not infer success from missing in-memory state, an expired deadline or
an operator declaration. Host/node replacement without original journals remains
unconfirmed.

Portable tests cover real owner-process death, pidfd/PID reuse, metadata round
trips and corruption, missing task history, the pre-binding crash window, failed
publication, helper retirement uncertainty and idempotent recovery. The native
repair gate additionally kills an owner with a real cgroup-contained helper and
checks retirement before slot reuse. A separate real-Firecracker suite now freezes
the production lifecycle in an owner subprocess after native boot, after request
delivery over vsock, after durable VM exit, and after durable resource removal.
It uses SIGKILL, recovers the original journals with a fresh Manager, checks replay
and allocator fences, and preserves source row versions and human edits.
The last checkpoint exercises a lost cleanup receipt. Only test wrappers supply
these pause points; no production kill switches or dispatch changes are added.

These fixtures keep the jail journal on tmpfs and put writable-image claims in
a private directory on the pinned layer's persistent filesystem. Failed cleanup
preserves both original roots. The owner hosts the production Manager and native
backends, rather than the serving vmmd/gRPC process. Actual KVM execution and leak
qualification on the designated native host remain pending before rollout;
serving-daemon restart and physical-node failure exercises remain separate.
