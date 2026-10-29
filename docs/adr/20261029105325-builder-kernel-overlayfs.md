# ADR-20261029105325 · Builder BuildKit may use kernel overlayfs

- **Status:** Proposed
- **Date:** 2026-10-29
- **Amends:** the builder guest's BuildKit worker settings
  (`guest/init`, introduced with the split-box builder in August 2026).
  ADR-003, under which builds run only inside builder microVMs, is unchanged.
- **Switch:** builderd `FAAS_BUILD_KERNEL_OVERLAY`, default off.

## Context

The builder guest runs rootless BuildKit with
`--oci-worker-snapshotter fuse-overlayfs`. That replaced `native` in August
2026, so committing a layer would no longer copy the whole snapshot tree.
Kernel overlayfs was not evaluated then.

Production on rc.304, 2026-10-29: a cold Node build (express, the AWS S3 SDK,
TypeScript and similar) spent 300 s in the builder VM, and 240 s with zstd
layers (ADR-20261028084018). About 150 s goes to everything up to the
dependency install. Then 115–147 s goes to a single export step that computes
and compresses every layer. Writing the finished cache layers accounts for
only about 20 s of that.

The FUSE snapshotter costs time in two places:

- **During the install.** Every file operation of the dependency install
  (tens of thousands of stats, opens and writes in `node_modules`) passes
  through a userspace FUSE daemon inside a nested VM.
- **During the export.** BuildKit's fast diff reads only an overlay's upper
  directory, and it applies only to kernel overlay mounts. For a FUSE mount
  BuildKit walks and compares the lower and upper trees.

The builder guest kernel (6.x) supports unprivileged overlay mounts in a user
namespace, and in rootless mode BuildKit's `auto` snapshotter picks
`overlayfs` when the kernel allows it, then `fuse-overlayfs`, then `native`.

## Decision

With `FAAS_BUILD_KERNEL_OVERLAY=true`, builderd sets the build manifest's
`buildkit_snapshotter` to `auto`, and guest-init starts BuildKit with
`--oci-worker-snapshotter auto`. Without it, or with an older guest that
ignores the field, nothing changes.

**Security.** The overlay mount happens inside the builder microVM's own
kernel, in BuildKit's user namespace. ADR-003 makes the VM the build's
sandbox, so an overlayfs bug reachable from a build compromises only that
ephemeral VM, the same blast radius as any other guest-kernel bug a build can
reach today.

**Output.** The snapshotter changes how layers are produced, not what they
contain, so it stays out of the build cache key. Layer digests may differ
from a FUSE-built layer, and the dependency cache imports either.

## Rollout

1. Release the guest and builderd change everywhere. Nothing changes yet.
2. Turn the switch on for one node's builderd with a systemd drop-in.
   Compare a cold build of the same app on both nodes: install time, the
   export step, and the build in the VM. The BuildKit debug log on the VM
   console names the snapshotter it chose.
3. If it holds, make it the default in a follow-up; if overlayfs misbehaves,
   turn the switch off.

## Consequences

- Expected: a shorter install and a much shorter export step for
  dependency-heavy builds, measured in rollout step 2 before any default
  flip.
- Not covered: the dependency cache's `mode=max` export and a package
  pull-through cache, which is a separate change.
