# Firecracker 1.7 timer restore correction

`tsc-restore-order.patch` fixes the restore-time ordering described in upstream
[Firecracker #4666](https://github.com/firecracker-microvm/firecracker/pull/4666).
It defers `MSR_IA32_TSC_DEADLINE` until all other saved MSRs have been restored.
Unlike upstream's snapshot-creation change, this also repairs the ordering in
existing 1.7 snapshots. Register values and the snapshot format stay unchanged.

This patch is selected only for the GCP SSD VMMD canary. The Ansible fleet pin
remains unchanged. See [ADR-150](../../docs/adr/150-firecracker-tsc-restore-order-canary.md)
and [the measurement report](../../docs/ops/snapshot-restore-performance.md).

## Build and test

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

## Canary selection and rollback

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
