# Local Firecracker metal loop (macOS, Lima nested KVM)

The platform's metal code (firecracker/jailer VM lifecycle) is gated behind
`//go:build metal` and needs `/dev/kvm`, which macOS doesn't provide. On an
**Apple Silicon M3 or later running macOS 15+**, Lima's `vz` backend can grant
**nested virtualization**, so an arm64 Linux guest gets its own `/dev/kvm` and
runs **aarch64 Firecracker**. That gives a local `make test-metal` loop without
a production bare-metal x86_64 host.

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

## Fixtures and checkout selection

The Lima provisioner installs the host tools: Go, Firecracker/jailer v1.7.0,
PostgreSQL, BusyBox, Docker/buildx, and kernel build dependencies. It does not
search guessed paths under the user's home. `run-metal.sh` resolves the checkout
from its own location; `FAAS_REPO_ROOT` explicitly selects another checkout.

Each run builds a static `vmmd` from that checkout for the jailed mount-helper
commands. Metal tests must not use their own Go test executable as that helper.
`FAAS_TEST_VMMD_BINARY` names the static binary for direct test invocations.

`stage-metal.sh` builds guest-init and refreshes the V6 base/layer fixtures when
the binary or fixture recipe changes. There is no need to delete the Lima VM to
pick up guest-init edits. The base includes the mountpoints guest-init needs
before it can pivot off its read-only root. The runner provides the tenant
bridge, scoped outbound NAT/forwarding rules, and ARM64 CPU-cache compatibility
setup. These are local development prerequisites; production uses host policy.

## Focused builder acceptance

```sh
make metal-lima-build
```

This starts an existing stopped VM or provisions one, builds the current
platform builder base using `images/builder-base.Dockerfile`, and stages the
canonical `runner-builder-arm64.ext4`. Docker builds only that platform fixture;
the customer test sources are built by BuildKit/Railpack inside Firecracker.
Buildx caches platform-image stages across runs. The first run also compiles a
checksum-pinned ARM64 kernel with built-in FUSE, overlayfs, user namespaces and
static IP autoconfiguration. The stock Firecracker CI kernel lacks the FUSE
support required by the builder's snapshotter. Subsequent runs verify/reuse the
kernel until the kernel recipe or pinned defaults change. The default suite
uses the production build deadline. `FAAS_METAL_BUILD_TIMEOUT_SECONDS` (1–3600)
can override the test request's deadline without changing production limits.

`TestMetalBuilderAcceptance` covers:

- Dockerfile source executable permissions, OCI export, real imaged conversion,
  and an HTTP response from a cold boot of the resulting app layer.
- Railpack selecting `apps/api` inside an archive whose repository-root build
  deliberately fails. Its shell provider builds a BusyBox static-file server;
  `railpack.json` selects the repository's pinned Debian upstream image for the
  build and runtime bases. The exported image is converted and booted too.
- A deliberate Dockerfile failure, including its guest result and no nonempty image.
- Cancellation after the long-running Dockerfile starts and the Destroy RPC
  owns export; interrupt plus teardown must finish within 15 seconds. An
  interrupted filesystem may fail artifact export; the process, drives, leases,
  and VM resources must still be gone before fallback test cleanup.
- Removed scratch drives, released leases, versioned jail directories and
  tenant/builder cgroups. The final shell check also checks Firecracker processes
  and jail/export mounts, including after a test failure.

This suite uses the real builderd driver and vmmd gRPC server. Store and
notification transport are in memory. It does not exercise apid upload,
PostgreSQL, scanner admission, scheduler snapshotting, or release deployment.
Those remain separate acceptance/CI gates. BuildKit startup has a bounded
one-minute readiness window for cold nested-KVM workers; ready workers proceed
immediately. The pinned BuildKit source also has a tested patch that waits for
its private frontend pipe to become ready before starting HTTP/2’s ten-second
handshake timer. That startup wait is bounded to one minute and honors cancellation.
Railpack’s local build context matches its selected workdir so plan-relative
COPY paths resolve to the selected workspace. Dockerfiles keep their explicit
repository context. App destruction kills the running VM immediately; the builder
completion wait applies only to builder records. Guest-init stops and reaps the
BuildKit daemon before its final sync and poweroff so it cannot keep writing
metadata while the host begins export.

For a narrower iteration inside Lima, use the same runner with a subtest filter:

```sh
sudo env FAAS_METAL_BUILD_ACCEPTANCE=1 RUN_GREGALE_RELEASE_INSTALL=0 \
  ./deploy/lima/run-metal.sh -run '^TestMetalBuilderAcceptance$/dockerfile-executable$'
```

The full Node workspace case is opt-in with `FAAS_METAL_NODE_ACCEPTANCE=1`.
It uses the repository's pinned public Node 24 runtime fixture, including its
shared libraries. **This is an outstanding slow-path gate:** on this ARM64
nested-KVM host, the cold Node build exceeded both 15- and 30-minute budgets
while importing/copying the upstream toolchain. The lightweight gate does not
validate that Node path or establish a production latency SLO. Run the Node
case on the dedicated x86_64 acceptance host before treating it as accepted:

```sh
sudo env FAAS_METAL_BUILD_ACCEPTANCE=1 FAAS_METAL_NODE_ACCEPTANCE=1 \
  RUN_GREGALE_RELEASE_INSTALL=0 ./deploy/lima/run-metal.sh \
  -run '^TestMetalBuilderAcceptance$/railpack-node-workspace$'
```

For a dedicated Linux x86_64 acceptance host, stage the matching platform
builder base at `/srv/fc/base/runner-builder-amd64.ext4`, use the production
kernel and host network/cgroup setup, and run from the checkout:

```sh
CGO_ENABLED=0 go build -trimpath -o /tmp/faas-metal-vmmd ./cmd/vmmd
# Set FAAS_TEST_KERNEL, FAAS_TEST_BASE_ROOTFS, FAAS_TEST_FC_VERSION,
# FAAS_GUEST_INIT and FAAS_BUILDER_BASE_PATH to the staged host assets.
sudo -E env FAAS_TEST_VMMD_BINARY=/tmp/faas-metal-vmmd \
  FAAS_METAL_BUILD_ACCEPTANCE=1 FAAS_METAL_NODE_ACCEPTANCE=1 \
  go test -tags metal -count=1 -v -timeout 60m ./pkg/fcvm -run '^TestMetalBuilderAcceptance$'
sudo make leakcheck
```

Run this only on an otherwise idle acceptance host: the leak check expects zero
running tenant/builder VMs. A green ARM64 Lima run validates the lifecycle and
build path; the pinned x86_64 kernel and hardware remain the production §14 gate.

## M5 §14 acceptance: `TestDeployWakeMetal`

The M5 deploy→wake test (`cmd/e2e/deploy_wake_metal_test.go`) is the
`spec §14` acceptance for "faas deploy → parked → first request wakes".
It goes through the full wire (apid → imaged → schedd → vmmd → firecracker,
then a real HTTP request through gatewayd-public) and asserts the served body
matches the OCI-fixture bytes byte-for-byte.

It boots the V6 rootfs as both `basePath("")` (no runtime) and
`basePath("node22")`, so `stage-metal.sh` retains the BusyBox base/runtime fixture aliases (including
the architecture-qualified storage keys) and builds the function-runner shims
from the selected checkout. These aliases are test fixtures, not full language
runtime images.

The test registers a fake OCI registry on loopback and configures
`imaged` to pull from it via `FAAS_OCI_INSECURE=1` +
`FAAS_TEST_BUILDER_BASE_REF` /
`FAAS_TEST_DEPLOY_BASE_REF`. The harness forwards those env vars
(see `pkg/e2etest/harness.go`).

The test's per-daemon log buffers are dumped via `t.Logf` on failure
(`e2etest.Harness.DumpLogs`), so a flake shows the daemon's last words
in the test output.

## Caveats — read before trusting a result

- **Arch:** the guest is **arm64**; production control-plane nodes are **x86_64**. This
  validates the arch-agnostic lifecycle logic and the Firecracker boot path. It
  does **not** produce production x86_64 snapshots or exercise the pinned
  x86_64 kernel. **A bare-metal x86_64 control-plane node remains the source of truth for
  the metal acceptance gates (spec §14).**
- **Supply chain:** the provisioner’s Firecracker and stock M0 kernel downloads
  still lack production’s checksum enforcement. The builder kernel source and
  config are checksum-pinned separately by `stage-kernel.sh`. Keep this setup
  in a disposable development VM.
- **Nested virt requires M3+ / macOS 15+.** Older chips or macOS won't grant
  `/dev/kvm`; the provisioner's probe reports this and you fall back to another
  bare-metal x86_64 box or a cloud KVM host.
