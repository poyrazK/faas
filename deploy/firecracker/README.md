# Firecracker builds

## Production VMM: host-owned virtio-net header (ADR-20261108170000)

Compute nodes run a Firecracker built by release CI. It is
`fc_source_commit` (the commit `fc_release` tags, both in
`deploy/ansible/roles/firecracker/defaults/main.yml`) plus the ordered patch
series in `patches/<fc_release>/`. Upstream's release binary is no longer what
production runs; its tarball still supplies the jailer and the image-seed/dev
fallback.

`patches/v1.17.0/0001-virtio-net-host-owned-vnet-header.patch`:

- New net devices offer no checksum or segmentation offload in either
  direction.
- Every frame is written to the TAP behind a vnet header the host owns. It is
  all zero unless the device was restored from a snapshot that negotiated
  guest-to-host offloads.
- For such legacy devices, `offload_guard::validate_legacy_offload` accepts
  only a TCP/UDP checksum (and TCP GSO) whose `csum_start` is the transport
  header the host parsed from its own copy. The host then writes those copied
  headers. Everything else is dropped.

This closes CVE-2026-99999 ("PwnEscape") and the rest of the guest
`virtio_net_hdr` class regardless of the host kernel.

New Firecracker metrics, per net device:

| metric | meaning |
| --- | --- |
| `tx_offload_metadata_ignored` | guest asked for an offload the device never offered; the host sent a zero header |
| `tx_offload_legacy_accepted` | legacy-offload frame that passed validation |
| `tx_offload_rejected` | legacy-offload frame that failed validation and was dropped (a correct driver never does this) |

### Build

```sh
sudo apt-get install -y musl-tools linux-libc-dev libseccomp-dev   # plus git and rustup
scripts/build-firecracker.sh --run-unit-tests --output /tmp/firecracker-vmm
```

The script:

1. Fetches `fc_source_commit` and refuses it unless the `fc_release` tag names
   that commit.
2. Applies the series in name order and stops on any patch that does not apply.
3. Installs the toolchain from that revision's `rust-toolchain.toml` (its
   seccomp filters assume it) and builds a static musl binary.
4. Runs the pure `offload_guard` tests before building.

Release CI runs it, ships the result in the signed bundle as `firecracker-vmm`,
and writes its sha256 to `release.firecracker_digest`.

### Test (needs root and a TAP-capable host)

From a pristine checkout of `fc_source_commit` with the series applied:

```sh
cargo test -p vmm --lib --no-run
sudo build/cargo_target/debug/deps/vmm-<hash> --test-threads=1 devices:: device_manager:: persist
cargo clippy -p vmm --all-targets -- -D warnings
```

The device tests create TAP devices, so they run as root.
`test_tx_guest_offload_metadata_ignored` and `test_tx_legacy_offload_validated`
cover both TAP paths, and `offload_guard::tests` covers the validator. Those
include the PwnEscape shapes and an exhaustive check that no accepted offload
places the checksum outside the transport header.

### Rollout

1. Before the first rollout, measure guest throughput on test-metal with the
   patched binary against upstream. Losing TSO costs guest-to-host bulk
   throughput. Record the numbers in the ADR.
2. Release as usual. `node_join` verifies the bundle signature, stages
   `firecracker-vmm`, and the `firecracker` role installs it as
   `/usr/local/bin/firecracker-v1.17.0-x86_64`. The basename stays upstream's,
   because the jailer and native recovery depend on it. The role then asserts
   the manifest digest.
3. Running VMs keep their already-started VMM. New boots and restores use the
   patched binary, because vmmd re-stages its resident copy when the installed
   file changes (ADR-20261105100000).
4. Rollback: deploying a release without `firecracker-vmm` re-extracts
   upstream's binary, which that release's manifest names.

Upgrading `fc_release` means rebasing the series onto the new commit. Bump
`fc_release`, `fc_release_sha256`, `fc_source_commit` and the patch directory
together.

## Historical: Firecracker 1.7 timer restore correction

> **Superseded by [ADR-983](../../docs/adr/983-firecracker-1-17-and-guest-kernel-6-18.md).**
> Firecracker 1.8 and later fix the ordering upstream. The patch applies only to
> v1.7.0 and is kept for the sparse-memory experiment.

`tsc-restore-order.patch` fixes the restore-time ordering described in upstream
[Firecracker #4666](https://github.com/firecracker-microvm/firecracker/pull/4666).
It defers `MSR_IA32_TSC_DEADLINE` until all other saved MSRs have been restored.
Unlike upstream's snapshot-creation change, this also repairs the ordering in
existing 1.7 snapshots. Register values and the snapshot format stay unchanged.

This patch is selected only for the GCP SSD VMMD canary. The Ansible fleet pin
remains unchanged. See [ADR-150](../../docs/adr/150-firecracker-tsc-restore-order-canary.md)
and [the measurement report](../../docs/ops/snapshot-restore-performance.md).

### Build and test

Use a pristine Firecracker checkout at commit
`76542f598b7a7fd59559949070aa6b0c4e68523d` (v1.7.0), its original `Cargo.lock`,
and its pinned **Rust 1.76.0** toolchain with the x86_64 Linux musl target and
required native build dependencies. Do not substitute a newer compiler without
checking syscall compatibility with the existing seccomp filters.

From that checkout, apply this patch and run:

```sh
cargo test --locked --release --target x86_64-unknown-linux-musl -p vmm --lib legacy_restore_msr_tests
cargo build --locked --release --target x86_64-unknown-linux-musl -p firecracker
```

The tests cover legacy same-chunk and cross-chunk ordering, unchanged register
values and inputs, empty snapshots, zero deadlines, and KVM batch size limits.
The artifact manifest records the source archive, patch and binary checksums.
A source audit must show only `src/vmm/src/vstate/vcpu/x86_64.rs` changed.

### Canary selection and rollback

The SSD-only override is
`/etc/systemd/system/faas-vmmd.service.d/zz-ssd-tsc-order-canary.conf`. It prepends
`/opt/faas/canaries/ssd-restore-20260905/tsc-runtime` to VMMD's PATH. The installed
upstream Firecracker and jailer links are retained. The canary manifest is
`tsc-runtime/manifest.json`; the reported snapshot compatibility version is
still 1.7.0.

After ensuring the SSD host has no live guests, rollback removes only that
override, runs `systemctl daemon-reload`, and restarts `faas-vmmd`. Verify the
service and its metrics endpoint after either change. Keep snapshot-only
measurements separate from cold boot and warm instance reuse.
