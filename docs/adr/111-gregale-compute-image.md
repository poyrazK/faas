# ADR-111 · Gregale Compute Image — versioned, immutable, per-cloud host image

- **Status:** **Proposed** (PR-1 of the issue #911 post-cutover cluster)
- **Date:** 2026-08-16
- **Decision:** Adopt a versioned, immutable, per-cloud **Gregale Compute
  Image** as the supported production host provisioning path. Every
  production node boots from a Packer-built image named
  `gregale-compute-{role}-{fc_release}-{kernel_version}-{git_sha}`
  where `{role}` is `control-plane` or `compute-only`. The image
  is content-addressed by tag — the same `{role, fc_release,
  kernel_version, git_sha}` always produces the same bytes.
  First-boot user-data (Hetzner cloud-init / EC2 user-data /
  bare-metal EX44 preseed) runs the cutover runbook's 6-step init
  chain idempotently. Image rollout (`make upgrade-node IMAGE_TAG=…`)
  is gated per-daemon by the existing `Lifecycle.Probe` /
  `ProbeTarget` health gate consumed by `gregalectl doctor --deep`
  (PR #921 / ADR-110 PR-4). **`make bootstrap*` stays as the
  installer path for dev boxes, CI, and the image-seed build**;
  image is not a replacement for bootstrap, it is the production
  deploy atop bootstrap. Image content is immutable from the
  operator's perspective; the only mutable surface is first-boot
  user-data, which is idempotent.
- **Why:** Issue #911 PR-cluster (Mega-PR-A, Mega-PR-B, Mega-PR-C /
  PRs #924, #925, #926) closed 10 BLOCKING + MEDIUM deploy-drift
  bugs across the manifest, policy, and deploy-tree sides. What
  remains is the host provisioning flow itself: every fresh box
  runs `apt install firecracker + curl kernel source + make
  bzImage (~5 min) + curl binaries + write systemd units + run
  ansible` from upstream packages. That flow has four concrete
  failure modes an immutable image closes:
  1. **Cross-host drift between spec, Go code, and apt+curl
     outcome** — the exact class of bug Mega-PR-C closed 10 of.
     The `apt install firecracker` step can roll the binary
     forward under us between two fresh boxes; the `curl kernel
     source` step depends on `cdn.kernel.org` being reachable;
     the `make bzImage` step produces a kernel whose SHA-256 only
     matches the pin if the build environment is identical.
  2. **Snapshot-pin violations (ADR-005)** — the spec requires
     `snapshots.fc_version` to pin to the FC version that made
     them; on FC upgrade snapshots go stale and apps lazily
     re-snapshot via cold boot. If `apt install firecracker`
     rolls the binary forward between two boxes in the same
     fleet, snapshots on the older box are still valid but
     snapshots on the newer box are stale — fleet-wide snapshot
     reuse silently degrades. An image bakes ONE FC version
     into the tag; rolling forward means a new image tag +
     lazy re-snapshot, never a host-side FC upgrade.
  3. **Slow wall-clock per-box** — the kernel build alone is
     ~5 minutes on a 4-core box
     (`deploy/ansible/roles/firecracker/tasks/main.yml:175-185`
     `make -j4 bzImage`). Adding the Go daemons, function-runners,
     Grafana dashboards, Prometheus rules, and the postgres
     install, a fresh box is 10+ minutes of wall-clock. Image
     pre-builds all of it once; per-box boot is just the
     first-boot init chain (under 60s on hcloud).
  4. **No per-tag identity contract** — today there is no way to
     say "fsn-2 runs gregale-compute-v17.0.0-fc1.7.0-kernel6.1.134-ga1b2c3d"
     the way you can name an AMI or a container image. The
     doctor (PR #921 / ADR-110 PR-4) asserts the on-disk binaries
     match `Release.FirecrackerDigest` and `Release.KernelDigest`,
     but it can only report a drift, not enforce a tag identity.
     Image fixes this by encoding the contract in the tag itself.
- **Consequences:**
  - **Packer build pipeline (`deploy/packer/`).** New tree with
    per-cloud builder configs:
    - `hcloud.pkr.hcl` — Hetzner Cloud (`hcloud` plugin, snapshot output).
    - `amazon-ebs.pkr.hcl` — AWS (`amazon-ebs` plugin, AMI output).
    - `iso.pkr.hcl` — bare-metal EX44 (Ubuntu 24.04 autoinstall ISO +
      post-install ansible; `iso` builder with `http_directory` for
      the autoinstall).
    - `common.pkr.hcl` — shared build block (source, communicator,
      build labels, post-processor chain, per-tag name synthesis).
    - `ubuntu-2404.pkr.hcl` — base Ubuntu 24.04 LTS source definition.
    - `role-overlay.pkr.hcl` — per-role file drop (`FAAS_BOX_ROLE=`
      drop-in, per-daemon subset per ADR-092).
    - `cloud-init/{control-plane,compute-only}.yaml.tpl` — Jinja2
      templates for first-boot user-data (one per role).
    - `scripts/{build-base,install-go,compile-daemons,compile-runners,
      prebuild-kernel,bake-fc,drain-node,verify-no-secrets}.sh` —
      one shell script per build phase; idempotent; re-runnable.
  - **Image content (baked, immutable from the operator's perspective):**
    - 8 Go daemons (`apid`, `gatewayd-internal`, `gatewayd-public`,
      `schedd`, `vmmd`, `builderd`, `imaged`, `meterd`, `githubd`) +
      `gregale` + `gregalectl` CLIs + 6 function-runners (linux/amd64,
      CGO off, `-trimpath`, `-ldflags='-s -w'`).
    - Firecracker binary + jailer + SHA-256 pin at
      `/usr/local/bin/firecracker-{version}` (symlinked to
      `/usr/local/bin/{firecracker,jailer}`).
    - Pre-built vmlinux + SHA-256 pin at
      `/srv/fc/base/vmlinux-{version}`.
    - nftables Jinja2 template + Go renderer (`cmd/faas-nft-render`)
      at the canonical paths (`/etc/nftables.conf.j2`,
      `/opt/faas/current/bin/faas-nft-render`).
    - Per-daemon users (`faas`, `faas-apid`, `faas-vmmd`,
      `faas-builderd`, `faas-imaged`, etc.), systemd slices
      (`faas-cp.slice`, `faas-tenant.slice`, `faas-build.slice`),
      supply-chain scanners (`grype`, `syft`), node_exporter,
      Prometheus binary + scrape config, Grafana binary +
      dashboards, postgres 16.
    - `/srv/fc/{base,jail,snap,layers,builder,base-staging,sigs,scans}`
      directory skeleton (mode 0750 root:faas).
    - Kernel kargs (`cgroup_no_v1=0`, `unprivileged_userns_clone=0`).
    - Per-role `99-faas-role.conf` drop-in
      (`Environment=FAAS_<DAEMON>_ROLE=<role>` per Mega-PR-C
      Commit 4) at `/etc/systemd/system/faas-<daemon>.service.d/`.
  - **Image content (NOT baked, lands at first-boot):**
    - `/etc/faas/sealed.env` — sealed customer + provider secrets
      (closed by ADR-020; sealed with X25519 host age key).
    - `/etc/faas/host.age` — host age keypair (generated per-box,
      never shared).
    - TLS leaves (`/etc/faas/tls/{schedd,apid,…}.{crt,key}`) —
      per-box PKI subset (ADR-092).
    - `/etc/faas/rclone.conf` — off-host backup creds (issue #250).
    - `cosign.{key,pub}` — image signing keypair (ADR-038).
    - The build script enforces this via `scripts/verify-no-secrets.sh`
      (CI gate: `grep -rIn` scan for forbidden paths before sealing
      the image).
  - **First-boot flow (cloud-init / EC2 user-data / preseed):**
    idempotent; re-runnable; second run is a no-op. Six steps
    from the cutover runbook (`docs/runbooks/manifest-renderer-cutover.md`
    steps 4-8):
    1. `gregalectl pki init --box-role=<role>` (mints per-box PKI
       subset, writes TLS leaves to `/etc/faas/tls/`).
    2. `gregalectl host-age init` (generates/loads the host age
       keypair, writes `/etc/faas/host.age`).
    3. `gregalectl sign-keys init` (per-box cosign keypair for
       ADR-038 layer attestation).
    4. `gregalectl node-key init` (compute-only boxes only;
       per-box wire-layer identity for mTLS).
    5. `gregalectl backup init && gregalectl backup unseal-rclone`
       (off-host Storage Box creds, ADR-056).
    6. `gregalectl release install --git-sha $MANIFEST_GIT_SHA`
       (pins `/opt/faas/current` to the manifest's `git_sha`).
    7. `gregalectl doctor --deep` — gate; failure halts the user-data
       and emits a loud `node-ready: false` log line that the
       hcloud metadata API can surface.
  - **Image-tag version contract** (content-addressed):
    `gregale-compute-{role}-{fc_release}-{kernel_version}-{git_sha}`.
    - `{role}` ∈ `control-plane | compute-only` — per ADR-092,
      compute-only images do NOT ship control-plane daemons and
      vice versa.
    - `{fc_release}` — the FC version baked into the image
      (`v1.7.0` today). Per ADR-005, snapshots pin to this
      version; rolling forward means a new image tag + lazy
      re-snapshot.
    - `{kernel_version}` — the kernel version built into the image
      (`6.1.134` today); the SHA-256 of the built `vmlinux` is
      recorded in the image's `/srv/fc/base/vmlinux-{version}.sha256`.
    - `{git_sha}` — the manifest's `release.git_sha` (per
      ADR-110). Every daemon binary + CLI + function-runner is
      built from this exact commit; the image tag and the
      `Release.{firecracker,kernel}_digest` assertions in
      `gregalectl doctor` line up by construction.
  - **Image rollout (`make upgrade-node IMAGE_TAG=...`):**
    drain-first, health-gate-second, flip-active-last:
    1. `gregalectl compute-nodes drain --node <fqdn>` submits an
       authenticated, audited `node_drain` intent through apid.
    2. Poll until schedd clears live instances and holds the node in the
       non-admitting `maintenance` lifecycle. If instances are still on
       the node, emit a loud `WARNING: N instances still on
       <fqdn>, manual drain required` and exit 1 (the operator
       runs `gregalectl compute-nodes force-drain --yes` to override).
    3. Signal the cloud-specific image-rollout mechanism
       (Hetzner: rebuild server from new snapshot; AWS: terminate
       + launch from new AMI; bare-metal: PXE-boot from new image).
    4. Wait for the new VM to come up; poll `gregalectl doctor
       --deep` on the new node, asserting every `Lifecycle.Probe`
       on every entry in `pkg/daemonunitspec.Registry` reports
       ready (reuses `cmd/deployctl/runtime.go:295-299 waitPath /
       waitTCP` — no new probe code).
    5. Submit `node_activate` only after every probe passes. The new node is
       now eligible for placement.
  - **CI gates (PR #928+):**
    - `packer-validate` job runs `packer init + packer validate
      -syntax-only` on every builder, fails on syntax error.
    - `image-build-canary` job builds the hcloud canary image on
      every PR (`continue-on-error: true` for PR visibility,
      required green on merge to main).
    - `verify-no-secrets` job greps the image for forbidden paths
      (`/etc/faas/sealed.env`, `host.age`, `rclone.conf`, `tls/*.key`,
      `cosign.key`) and fails if any are found.
    - `make image-test-first-boot` + `make image-test-upgrade`
      e2e gates (PR #930, #931).
  - **Out of scope (explicitly deferred):**
    - **Live-migration support** (M9, §14) — image is the
      steady-state; M9 is a separate migration-engine PR.
    - **Multi-region active-active HA** — image is the unit of
      deploy; cross-region replication is its own ADR (the
      active-passive HA in ADR-083 uses the image per box).
    - **In-place OS upgrade** (Ubuntu 24.04 → 26.04) — every new
      LTS ships as a new image tag, not as an in-place upgrade.
    - **Windows / macOS compute nodes** — Linux only.
    - **OCI-image-style hosts** (Kubernetes) — Gregale runs on
      bare metal and dedicated VMs.
  - **Migration shape:** zero schema change. Zero new migration.
    Image ships with `Release.{firecracker,kernel}_digest` already
    asserted by `gregalectl doctor` (per PR #921 / ADR-110 PR-4);
    the image just makes the assertion byte-stable across boxes.
  - **Operator-facing surface change:**
    - `make image-build-{hcloud,amazon-ebs,iso}` — manual gate the
      operator invokes (NOT a CI gate).
    - `make upgrade-node IMAGE_TAG=...` — fleet-wide rolling
      upgrade.
    - `make image-list` — lists tagged images across clouds.
    - `make image-test-first-boot IMAGE_TAG=...` — e2e gate.
    - `make image-test-upgrade OLD_TAG=... NEW_TAG=...` —
      rolling-upgrade e2e gate.
    - `make image-validate` — `packer validate -syntax-only` on
      every builder.
  - **Bootstrap stays.** `make bootstrap*` remains the installer
    path for dev boxes, CI, and the image-seed build. Bootstrap
    and image share the same 23 ansible roles; image build just
    freezes their output into a single artifact. Bootstrap's
    §14 M0 idempotency acceptance (`make bootstrap` idempotent
    on fresh Ubuntu 24.04) is preserved as the per-builder
    "image-build-canary" gate.
- **Rejected alternatives:**
  - **Drop bootstrap entirely, replace with Packer.** Rejected
    because: (a) Lima nested-virt (M3+) and a fresh Hetzner box
    for testing both need to bootstrap WITHOUT an image; removing
    the bootstrap path would break the dev + CI loop; (b) the
    first-time image build needs a bootstrapped box; bootstrap
    is the seed; (c) the spec's "deployable on any bare-metal
    x86_64" claim is load-bearing for the multi-host rollout
    story; removing bootstrap would push the platform to
    "deployable on any cloud with our image," which narrows the
    target.
  - **Use Ansible's `image build` (AWX workflow) instead of
    Packer.** Rejected because: (a) Packer has a per-cloud
    builder model (hcloud / amazon-ebs / iso / googlecompute)
    that covers every target with one config shape; (b) the
    content-addressed tag contract
    (`{role}-{fc_release}-{kernel_version}-{git_sha}`) maps
    cleanly to Packer's per-builder `name` template; (c) the
    per-builder `packer validate` is faster than the equivalent
    AWX workflow check.
  - **Bake `/etc/faas/sealed.env`, host.age, TLS leaves, cosign
    keys into the image.** Rejected because: (a) per-box secrets
    must be unique (the whole point of the per-box PKI subset,
    ADR-092); (b) baking secrets into a shared image is a
    cross-box secret-leak by construction; (c) the existing
    6-step init chain from the cutover runbook
    (`gregalectl {pki,host-age,sign-keys,node-key,backup} init`)
    is already idempotent and runs in under 60s — moving it to
    first-boot user-data is straightforward.
  - **Bake the manifest itself into the image.** Rejected
    because: (a) the manifest is per-fleet (one file describes
    `fsn-1` + `fsn-2` together); a per-box image only sees its
    own role; (b) the operator may rotate manifests mid-fleet
    (issue #911 PR-3); baking makes that brittle; (c) the
    first-boot user-data takes the manifest's `git_sha` from
    the cloud-init metadata and `gregalectl release install`
    fetches it from the registry — same wire path as today.
  - **Use cloud-init's `package_update` + `package_install` for
    every dependency.** Rejected because: (a) re-introduces
    the exact `apt install firecracker` drift the image is
    meant to close; (b) cloud-init's package module runs at
    every boot by default (the `package_update` + frequency
    knobs), which silently rolls dependencies forward; (c)
    the immutable-image contract is what makes the per-tag
    identity work.
  - **Single-image-for-all-roles (no per-role subset).**
    Rejected because: (a) ADR-092 explicitly forbids
    control-plane daemons on a compute image (the role gate
    refuses to start them); (b) shipping daemons that won't
    run adds attack surface; (c) the doctor would flag the
    stopped-but-installed control-plane daemons as
    misconfigured.
  - **In-place upgrade via `apt upgrade`.** Rejected because:
    (a) every in-place upgrade is a new untested permutation;
    (b) snapshot pin (ADR-005) requires the FC version to be
    stable per fleet; (c) the image-tag contract gives operators
    a clear "what's running on fsn-2 right now" answer that
    in-place upgrade cannot.
