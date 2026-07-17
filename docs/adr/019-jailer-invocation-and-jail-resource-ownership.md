# ADR-019 · Jailer invocation (`--exec-file`) + jail resource ownership (M0)

- **Status:** accepted
- **Date:** 2026-07-17
- **Decision:** Two coupled changes to how `vmmd` builds and launches a jail,
  both surfaced by running the M0 metal gate (`TestMetalHelloBoot`) end-to-end
  for the first time.

  **(1) Jailer invocation deviates from spec Appendix B.** The reference
  invocation in `docs/faas_implementation_spec.md` §Appendix B is:

  ```
  jailer --id {instance} --uid {uid} --gid {gid} \
    --chroot-base-dir /srv/fc/jail --netns /run/netns/fc-{instance} \
    --cgroup-version 2 --parent-cgroup faas-tenant.slice \
    -- firecracker --api-sock api.sock --config-file vmconfig.json
  ```

  We instead emit (`pkg/fcvm/config.go` `JailerCommand`):

  ```
  jailer --id {instance} --uid {uid} --gid {gid} \
    --exec-file {resolved firecracker path} \
    --chroot-base-dir /srv/fc/jail --netns /run/netns/fc-{instance} \
    --cgroup-version 2 --parent-cgroup faas-tenant.slice \
    -- --api-sock api.sock [--config-file vmconfig.json]
  ```

  i.e. `--exec-file` is a **required** jailer option (Appendix B omits it), and
  the tokens after `--` are firecracker's *own* argv with **no leading
  `firecracker`** — jailer execs the exec-file itself, so a positional
  `firecracker` there would be passed to firecracker as an argument. `vmmd`
  resolves the `firecracker` symlink and passes the **real** target as
  `--exec-file`, because jailer derives the chroot directory name from the
  resolved basename (`pkg/fcvm/vmm.go` `resolveFCChrootName`): a
  `firecracker → firecracker-v1.7.0` symlink (shipped by both the ansible role
  and the Lima loop) makes jailer build `.../firecracker-v1.7.0/<id>/root`, so
  the Manager must stage into that same directory.

  **(2) Jail resource ownership.** The jailed firecracker `chroot()`s and then
  drops to the per-instance uid/gid (20000–29999, ADR-inline §3 / spec §11), so
  every file it opens inside the chroot must be reachable by that uid. `vmmd`
  (the only root component) stages accordingly, by file role:
  - **Read-only shared** (kernel, drive0 base rootfs, snapshot mem/vmstate):
    hardlinked in (cheap — Appendix B) and mode-widened to `o+r`. They are
    **never chowned** — the hardlink aliases a shared source inode, and a chown
    would rewrite the owner out from under every other instance holding the same
    link (including the N instances a single snapshot restores as, invariant
    §6.2-5). They are non-secret, read-only, and visible only inside one
    instance's chroot, so `o+r` is safe.
  - **Read-write per-instance** (drive1, the overlay upper — `guest/init`):
    **copied** to a private per-instance file, `chmod 0600`, `chown` to the
    jailer uid. Never hardlinked: two instances must not share a writable ext4
    (invariant §6.2-5), and aliasing the source inode would corrupt it under
    concurrent writers.
  - **`vmconfig.json`** and the **chroot `root/` directory**: chowned to the
    jailer uid, so the jailed firecracker can read `--config-file` and create
    the API socket / write snapshot output inside `root/`.

  The chown step is a no-op when `vmmd` is not running as root (`os.Geteuid()
  != 0`) so the cross-platform unit suite — which never launches a real jail —
  stays green; the metal suite runs as root (`test-metal` / `metal-lima` sudo).
- **Why:**
  - Appendix B's invocation is simply **wrong for jailer v1.7.0** (our pinned
    version, ADR-005): jailer rejects a missing `--exec-file` outright, and
    treats a post-`--` `firecracker` token as a firecracker argument. The spec
    was written before the metal path had ever executed (the M0 asset checksums
    are still `REPLACE_` placeholders), so this is the first time it was
    exercised. Per CLAUDE.md / spec §3, a deviation needs an ADR — this is it.
  - Without (2), M0 could launch jailer→firecracker but the guest never became
    ready: the jailed uid could not read the `0640` root-owned config or drives,
    nor create its API socket in a `0750` root-owned `root/`. This was the
    "jail-resource-ownership gap" called out as out-of-scope in the PR that
    wired the nftables DNAT; it is closed here so **M0 passes end-to-end**.
- **Consequences:**
  - `docs/faas_implementation_spec.md` §Appendix B is superseded by this ADR for
    the jailer invocation. Do not "fix" `JailerCommand` back toward the spec text.
  - `JailerSpec` gains `ExecFile`; `JailerVMM` tracks the resolved chroot
    basename (`fcName`) and uses it in `chrootRoot`/`Kill`.
  - `provision` now stages by drive role (`stageReadOnly` / `stageWritable`) and
    threads the lease uid/gid. drive1 is a per-instance **copy**, so a Scale-plan
    2 GB layer costs a 2 GB copy per live instance — acceptable for M0/M1; a
    reflink/CoW fast path is a later optimization, not a correctness change.
  - The EX44 remains the source of truth for the §14 M0 gate: a green
    `make metal-lima` (arm64 nested KVM) validates the lifecycle and boot path
    but not the pinned x86_64 kernel/snapshots (CLAUDE.md). This ADR's ownership
    model is proven by unit tests (`stageReadOnly`/`stageWritable` inode +
    mode behaviour) and by the M0 boot on metal; the chown-to-uid step itself is
    only exercised under root, i.e. on the box / in the Lima loop.
- **Rejected alternatives:**
  - **Keep Appendix B's invocation.** Does not run on jailer v1.7.0. Rejected.
  - **Chown the hardlinked shared read-only files to the jailer uid.** Corrupts
    the shared source inode's owner for every other instance / restore that
    hardlinks the same base or snapshot. Rejected in favour of `o+r` widening.
  - **Hardlink the writable drive1 and make it world-writable.** Aliases the
    source layer inode; concurrent instances would corrupt each other's disk and
    the source of truth. Rejected in favour of a private per-instance copy.
  - **Make the guest drives world-writable at build time (imaged/rootfs).**
    Pushes a host-jail concern into the image pipeline and still leaves the
    per-instance config/socket ownership unsolved. Rejected.

## Re-evaluation triggers

- **Firecracker/jailer upgrade (ADR-005 fc-version bump):** re-check jailer's
  `--exec-file` semantics and chroot-basename derivation; both are version
  behaviour. The pinned version is the contract.
- **Writable-layer copy cost at M1 concurrency:** if per-instance 2 GB copies
  dominate wake latency or disk, introduce reflink/overlay-backed CoW for
  `stageWritable` — a performance change, not a correctness one.
