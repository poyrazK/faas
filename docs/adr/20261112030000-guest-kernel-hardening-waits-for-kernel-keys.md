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
