# deploy/ — host provisioning and runtime config

Bootstraps a fresh bare-metal x86_64 split-box fleet to Gregale-ready:

```
make manifest-ansible MANIFEST=deploy/manifest/splitbox.yaml
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-control-plane
make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini bootstrap-compute
```

then verify the platform works end-to-end:

```
sudo make test-metal    # `go test -tags metal ./...` — boots a hello-Firecracker VM
sudo make leakcheck     # asserts zero leaked netns/taps/jails/cgroups
make build              # compile every daemon
make test               # cross-platform unit tests
```

The builder pipeline has a focused native Firecracker gate. Run it on the
dedicated x86_64 KVM acceptance host after the signed release assets have been
staged:

```sh
export FAAS_TEST_KERNEL=/srv/fc/base/vmlinux-<release>
export FAAS_TEST_BASE_ROOTFS=/srv/fc/base/base-amd64.ext4
export FAAS_BUILDER_BASE_PATH=/srv/fc/base/runner-builder-amd64.ext4
export FAAS_GUEST_INIT=/path/to/release/faas-guest-init
export FAAS_TEST_FC_VERSION=<firecracker-version>
sudo -E make test-metal-builder
```

This builds the current checkout's static vmmd helper, runs Dockerfile and
Railpack builds inside jailed builder VMs, converts successful exports with
imaged, boots the resulting app, checks failure and cancellation, and finishes
with the host leak check. Set `FAAS_METAL_NODE_ACCEPTANCE=1` and
`METAL_BUILDER_TIMEOUT=60m` to include the cold Node toolchain case.

The automated form lives in `.github/workflows/builder-native.yml`. It runs
only for trusted `main` commits after the matching multi-architecture builder
image has passed the image workflow. The designated host must contain the
root-owned `/etc/faas/builder-acceptance-host` marker. The runner refuses an
active Firecracker workload, serializes with a host `flock`, stages the tested
builder below `/srv/fc/acceptance`, restores the prior service state, removes
the fixture, and performs a final leak check on every exit path.
The cloud identity and host controls are documented in
[`docs/ops/builder-native-ci.md`](../docs/ops/builder-native-ci.md).

- `ansible/` — role-aware split-box bootstrap: the control-plane and
  compute-only plays install only their own daemon set and mask stale
  opposite-role services. See [`ansible/README.md`](ansible/README.md).
- `systemd/` — one unit + slice per daemon; memory.max fences the RAM
  ledger. Wired up in M5 (per-slice `.slice` units land in
  `ansible/roles/systemd_slices/`).
- `nftables/` — tenant + builder egress policy (§7): deny 25/465/587,
  deny RFC1918/link-local/metadata. Dropped as `/etc/nftables.conf`
  via the `nftables` ansible role.
- `scripts/` — ops helpers (`leakcheck.sh` for the shell-side check,
  restore drill planned for M8).
- `controlplane/` — retired legacy bootstrap surface. Production hosts use
  the manifest-generated split-box inventory and role-aware Ansible targets;
  the image builder uses its isolated image-seed inventory.
