# Guest-disk mount hardening evidence — 2026-10-25 (#5323)

Host: `gregale-internal-test-1` (n2-highmem-2, nested KVM, kernel
`7.0.0-1011-gcp`, Firecracker 1.17.0). Nested KVM is functional evidence only;
native x86_64 acceptance runs when the native CI hosts are back (the CI GCP
project has billing disabled).

#5323 (merged as 860d0e94f6) routes every host mount of a guest-written disk
through `guestDiskMountArgs` (`-t ext4 -o loop,<mode>,nodev,nosuid,noexec,errors=remount-ro`)
and gives the native loop mount the `errors=remount-ro` data string. CLAUDE.md
asks for `test-metal` and `leakcheck` on VM lifecycle changes; this is that run.

## Metal suite (`guest-disk-mount-metal.sh`)

Source `ec77c012` (main `ca4cea2fb2` plus the new test), run under the shared
`/run/lock/faas-builder-acceptance.lock` with a private tmpfs jail.

```text
leakcheck before: rc=0 leakcheck OK — no leaked netns/taps/jails/cgroups/processes/bridges/mounts/native-loops
--- PASS: TestMetalGuestDiskMountOptionsOverrideTheGuestSuperblock (0.19s)
--- PASS: TestMetalNativeLoopMountRecovery (0.75s)
--- PASS: TestMetalRestoreLearnsReusedCaptureWithAppEnv (9.84s)
--- PASS: TestMetalRestoreSkipsUnchangedPreBootFiles (5.76s)
--- PASS: TestMetalRestoreLearnsReusedCaptureAfterRestart (4.16s)
--- PASS: TestMetalSecretsEnvReachesGuest (7.86s)
--- SKIP: TestMetalAdoptionKinds (0.03s)  set FAAS_ADOPTION_EXPERIMENTS=1 on a dedicated acceptance host
leakcheck after: rc=0 leakcheck OK — no leaked netns/taps/jails/cgroups/processes/bridges/mounts/native-loops
```

| Mount path | Covered by |
|---|---|
| Pre-boot staging on restore (`loop_mount.go`, `rw`) | the three `TestMetalRestore*` tests and `TestMetalSecretsEnvReachesGuest` restore and cold-boot real guests through it |
| Native loop mounts (`native_loop_mounts_linux.go`) | `TestMetalNativeLoopMountRecovery` |
| Job manifest drive (`job_vmm.go`, `rw`) and builder export (`vmm.go`, `ro,noload` / `ro`) | `TestMetalGuestDiskMountOptionsOverrideTheGuestSuperblock` mounts with the same argv for each mode; no `pkg/fcvm` metal test boots a job or builder VM |

`TestMetalGuestDiskMountOptionsOverrideTheGuestSuperblock` formats an ext4
image, sets its superblock to panic on error (`tune2fs -e panic`), mounts it
with `guestDiskMountArgs` for `rw`, `ro` and `ro,noload`, and reads
`/proc/self/mountinfo`: the filesystem is ext4, the mount is `nodev,nosuid,noexec`
(and `ro` for the read-only modes), and the superblock options carry
`errors=remount-ro`, which ext4 only lists when it differs from the
superblock's own behaviour. A disk reformatted as vfat is refused. The test
does not corrupt a filesystem: proving that a guest-triggered error no longer
panics the host would risk the shared node if it failed.

`TestMetalAdoptionKinds` was not run: it is opt-in for dedicated acceptance
hosts, and this node is shared.
