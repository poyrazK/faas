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

`run-metal.sh` sets `FAAS_TEST_KERNEL` / `FAAS_TEST_BASE_ROOTFS` /
`FAAS_TEST_LAYER_ROOTFS` / `FAAS_TEST_FC_VERSION` for the tests
(see `pkg/fcvm/manager_metal_test.go`).

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

**M0 does not yet pass end-to-end.** The remaining blocker is jail-resource
ownership: the manager stages the drive/config files `0640` root-owned
(`pkg/fcvm` `copyFile`), so the jailed firecracker (unprivileged uid) can't open
them, and the writable app layer (drive1, the overlay upper) needs per-instance
ownership under the jailer uid. That is a security-sensitive product change,
tracked separately.

Two **arm64-Lima shims** live in `run-metal.sh` (never needed on the x86_64 EX44):
the `br-tenants` bridge (host-prep the box does via ansible) and a CPU-cache
sysfs shim (jailer reads cache sizes the arm64 nested guest doesn't expose).

The **M1** (50× concurrent) and **M3** (park→wake latency) tests additionally need
real base/layer rootfs images (`runner-node22`, …), an **M2** deliverable — they
`t.Skip` until `FAAS_TEST_BASE_ROOTFS`/`_LAYER_ROOTFS` point at real images.

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
