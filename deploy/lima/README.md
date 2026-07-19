# Local Firecracker metal loop (macOS, Lima nested KVM)

The platform's metal code (firecracker/jailer VM lifecycle) is gated behind
`//go:build metal` and needs `/dev/kvm`, which macOS doesn't provide. On an
**Apple Silicon M3 or later running macOS 15+**, Lima's `vz` backend can grant
**nested virtualization**, so an arm64 Linux guest gets its own `/dev/kvm` and
runs **aarch64 Firecracker**. That gives a local `make test-metal` loop without
the Hetzner EX44.

## Quick start

```sh
make metal-lima          # boots the VM (first run provisions ~few min) + runs the M0 gate
```

or manually:

```sh
limactl start deploy/lima/faas-metal.yaml    # first run provisions Go + firecracker + kernel
limactl shell --workdir "$PWD" faas-metal    # drop into the guest at your repo checkout
sudo ./deploy/lima/run-metal.sh              # M0 gate (TestMetalHelloBoot)
sudo ./deploy/lima/run-metal.sh -run TestMetalBoot50Concurrent  # a specific test
```

Tear down with `limactl delete -f faas-metal`.

## What the provisioner stages (`faas-metal.yaml`)

- Ubuntu 24.04 arm64, `vz` backend, `nestedVirtualization: true`, host `~`
  mounted read-write (so the repo checkout is reachable).
- A `probe` that fails fast with a clear message if `/dev/kvm` never appears.
- Go 1.25.7 (matches `go.mod`), `build-essential`, `e2fsprogs` (`mkfs.ext4`),
  `iproute2`/`iptables`, `busybox-static` (the M0 rootfs fallback), and the
  default user added to the `kvm` group.
- aarch64 Firecracker + jailer **v1.7.0** on `PATH`, and the aarch64 guest
  kernel `vmlinux-6.1.128` in `/srv/fc/base/`.
- **V6 acceptance rootfs** staged at `/srv/fc/base/v6-base.ext4` (PID 1 =
  the real `faas-guest-init` built from the mounted repo checkout, so the
  AF_VSOCK resume listener is wired) and `/srv/fc/base/v6-layer.ext4`
  (writable overlay upper). Built once per `limactl start`; the M0/M1/M3
  tests reuse the same image as their `FAAS_TEST_BASE_ROOTFS` /
  `FAAS_TEST_LAYER_ROOTFS`, and `TestMetalTwoRestoresDistinctUUID`
  (spec §14 V6, ADR-022) consumes it via `FAAS_TEST_V6_BASE` / `_LAYER`.

  To rebuild after a `guest/init` source change:
  `limactl delete -f faas-metal && limactl start deploy/lima/faas-metal.yaml`.

`run-metal.sh` sets `FAAS_TEST_KERNEL` / `FAAS_TEST_BASE_ROOTFS` /
`FAAS_TEST_LAYER_ROOTFS` / `FAAS_TEST_V6_BASE` / `FAAS_TEST_V6_LAYER` /
`FAAS_TEST_FC_VERSION` for the tests (see `pkg/fcvm/manager_metal_test.go`
and `pkg/fcvm/v6_resume_ext4_metal_test.go`).

## What runs here today

Driving `TestMetalHelloBoot` (M0) on nested KVM exercises the whole
jailer → firecracker → tap → netns → nftables-DNAT path. It was the first time the
metal suite had actually run (the M0 asset checksums are still `REPLACE_`
placeholders), which surfaced a chain of latent bugs — most now fixed:

- Firecracker boots a full guest under nested KVM; the netns + per-instance
  nftables DNAT make the guest reachable at its host identity (proven: a root-ns
  probe to `10.100.x.y:8080` returns 200/404 through the DNAT). ✅
- jailer launches firecracker as the unprivileged uid in the correct chroot
  (`--exec-file` + resolved-symlink chroot basename fixes). ✅
- The jailed uid can read its config + drives and create its API socket: `vmmd`
  stages read-only images `o+r` and copies + chowns the writable drive1 to the
  jailer uid (jail resource ownership, [ADR-019](../../docs/adr/019-jailer-invocation-and-jail-resource-ownership.md)). ✅
- **V6 (post-restore resume hook):** `TestMetalTwoRestoresDistinctUUID`
  cold-boots a guest from the V6 rootfs, parks it, then restores the same
  snapshot into two distinct leases and asserts the served UUIDs diverge.
  This is the §11 ship-blocker test that wires
  `pkg/fcvm/vmm.go::TriggerResumeHook` →
  `guest/init/listen_resume_linux.go::handleResumeConn`. ✅

**M0 and V6 boot end-to-end here.** Remember the arch caveat below: this is the
arm64 nested-KVM guest, so a green run validates the VM lifecycle and boot path —
the **EX44 stays the source of truth for the §14 acceptance gates** on the
pinned x86_64 kernel (CLAUDE.md).

Two **arm64-Lima shims** live in `run-metal.sh` (never needed on the x86_64 EX44):
the `br-tenants` bridge (host-prep the box does via ansible) and a CPU-cache
sysfs shim (jailer reads cache sizes the arm64 nested guest doesn't expose).

The **M1** (50× concurrent) and **M3** (park→wake latency) tests also pass
here against the V6 rootfs (it's a full guest-init, just with a busybox
httpd entrypoint instead of a real app).

## Caveats — read before trusting a result

- **Arch:** the guest is **arm64**; the production EX44 is **x86_64**. This
  validates the arch-agnostic lifecycle logic and the Firecracker boot path. It
  does **not** produce production x86_64 snapshots or exercise the pinned
  x86_64 kernel. **The EX44 remains the source of truth for the metal
  acceptance gates (spec §14).**
- **Supply chain:** firecracker + kernel are fetched here **without** the pinned
  SHA-256 discipline the ansible `firecracker` role enforces on the box. Fine
  for a throwaway dev VM; never do this on the EX44.
- **Nested virt requires M3+ / macOS 15+.** Older chips or macOS won't grant
  `/dev/kvm`; the provisioner's probe reports this and you fall back to the EX44
  or a cloud KVM box.
