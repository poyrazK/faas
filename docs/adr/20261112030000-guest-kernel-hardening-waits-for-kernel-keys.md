# ADR: The guest kernel's no-module build waits for content-addressed kernel keys

Date: 2026-11-12
Status: Accepted (interim)
Amends: [ADR-20261027090013](20261027090013-sandbox-sessions.md) §10 (when "No kernel code" ships, not what it requires)

## Context

#6970 turned off `CONFIG_MODULES` and `CONFIG_KEXEC_FILE` in every guest
kernel build, as ADR-20261027090013 §10 requires before sandbox sessions
give a tenant root. That changed the release kernel's digest.

Kernel artifacts in shared storage are keyed by Firecracker version alone
(`kernel/<firecracker_version>`, `gregalectl artifact publish` and vmmd's
`storagekey.Kernel`). The key holds one immutable kernel. rc.338 carried the
new kernel under the unchanged `kernel/1.17.0`, so `cd-compute` refused to
publish it (`artifact digest mismatch`) and no compute node could take the
release. The control plane was rolled back to rc.337. Every release cut
from main fails the same way until either the kernel or the key changes.

Sandbox sessions are off in production: agentd, schedd and vmmd each gate
them behind a switch that defaults off, and no deployment sets one.

## Decision

- The guest kernel build returns to its rc.337 inputs (the release script,
  the Ansible fallback and the packer prebuild), so a release ships the
  kernel production already runs.
- `scripts/reuse-release-kernel.sh` matches prior releases by the content of
  the kernel build inputs, not by the last commit that touched them. A
  restored input set reuses the signed kernel built from it, byte for byte,
  instead of rebuilding one that may differ.
- `TestGuestKernelBuildsLoadNoKernelCode` is skipped, not deleted. It comes
  back with the hardening.
- The no-module build ships again once kernel artifacts are keyed by content
  (for example `kernel/<firecracker_version>/<sha256>`), with N-1 nodes
  still reading the old key during the rollout skew and with snapshots taken
  on the old kernel going stale.

## Consequences

- Sandbox sessions must not be switched on in production until the
  no-module kernel ships. Their switches already default off.
- The control plane keeps the no-module `.config` it built during rc.338
  (Ansible's line edits do not undo themselves). Its local kernel is not
  digest-checked and runs no tenant VMs.
- Releases from main can roll to compute again.

## Amendment 1 (2026-11-21): kernel keys are content-addressed

Status: proposed. Unblocks the no-module kernel; does not ship it.

- **Key.** `storagekey.KernelForDigest(fcVersion, digest)` is
  `kernel/<fc_version>-<sha256>`. It is one path segment, so the OCI backend
  maps it to the tag `<fc_version>-<sha256>` (71 characters) and back.
  `kernel/<fc_version>` stays as the version-only alias.
- **vmmd** boots the content-addressed key when `kernel_digest` (vmmd.toml) or
  `FAAS_VMMD_KERNEL_DIGEST` is set, and the version-only key otherwise. An
  invalid digest fails startup. A job boot that names
  `kernel/<this node's fc_version>`, which schedd sends today, boots the node's
  own key, so jobs and app VMs share a kernel and schedd does not change.
  Execution runtimes keep their release-owned `FAAS_EXECUTION_*_KERNEL_KEY`.
- **Ansible** (firecracker role) copies the release kernel to
  `/srv/fc/kernel/<fc_version>-<sha256>` for the local backend and writes
  `faas-vmmd.service.d/99-faas-kernel.conf` with the verified digest.
- **`gregalectl artifact publish`** writes the content-addressed key, which is
  immutable as before. It writes the version-only alias only when the alias is
  empty; an alias holding an earlier release's kernel is kept and reported as
  `legacy_key_state: kept` instead of failing the release (the rc.338
  failure). `artifact verify` checks the content-addressed key.
- **Skew.** N-1 vmmds keep reading the alias, which holds the kernel they were
  released with. A snapshot captured on one kernel and offered to a node on the
  other is refused by the backing check (ADR-510) and cold-boots, as before.
- **Order.** This ships first with the rc.337 kernel bytes, so nothing changes
  for running nodes except the key they read. The no-module build returns in
  a later release, and its rollout makes every app snapshot stale once: plan
  it outside launch week, with the cold-boot wave watched.

## Amendment 2 (2026-11-21): the no-module build returns

Status: proposed; ships after amendment 1 is released and after launch week.

- The release script, the Ansible fallback and the packer prebuild turn off
  `CONFIG_MODULES` and `CONFIG_KEXEC_FILE` again, as #6970 did, and
  `TestGuestKernelBuildsLoadNoKernelCode` runs again.
- The new kernel publishes under its own content-addressed key (amendment 1),
  so `cd-compute` no longer collides with `kernel/<fc_version>`.
- Every app snapshot captured on the old kernel is refused by the backing check
  and cold-boots once, then re-snapshots. Roll it out with the cold-boot rate
  and `gateway_wake_latency_seconds` watched, node by node.
- Sandbox sessions may be switched on in production only after this kernel
  runs on every agent node and `TestMetalSandboxRootStaysInsideTheVM` passes
  on it.
