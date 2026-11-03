# images/ — Dockerfiles for content-addressed base images (spec §4.6, §15)

`ci-runner.Dockerfile` is an internal Jobs CI protocol image recipe, outside the
publication matrix. It packages the guest-only `gregale-ci-runner` binary with a
shell and CA trust. `make ci-runner-build` builds the binary without publishing an
image or enabling CI. Run repository steps only in Jobs microVMs; a container
image build or portable fake-executor test is not native execution acceptance.
See [internal CI](../docs/ops/internal-ci.md#guest-runner-and-scoped-transfers).

`agent-base`, `base-debian-parent`, `base-minimal`, `runner-node22`, `runner-node24`, `runner-python312`, `runner-python313`, `runner-go124`, `runner-go124-alpine`, `builder-base`. Built in CI,
staged to /srv/fc/base/ (inside the 60 GB reserve, counted once). drive0 is one of
these shared read-only base rootfs; per-app layers stack over it via overlayfs.
Never flatten into one rootfs per app (breaks the 130 MB fleet target).

`runner-python313` is a standalone Wolfi/glibc chain rather than a child of
`base-debian-parent`; imaged stages its complete shared base once and still
uses the same two-drive layout for every Python 3.13 app.

`execution-python-data-v1` is a separate platform-owned base for disposable
Python data Runs. Its closed, hash-pinned wheel set comes from
`pkg/executionprofiles/python-data-v1.json`; the standard-library installer
adds no pip or wheel scripts. Package changes require a new profile ID.
The image includes the `python-data-v1` execution marker and is shared read-only
across fresh VMs. The 91.38 MiB extracted package payload is counted once in
base residency, while every run retains disposable scratch. Build output
records wheel/installed bytes in `/usr/share/faas/execution-profile-build.json`.
CI verifies versions and imports as uid 1000 without network or rootfs writes,
and applies the all-CRITICAL scan gate used by `runner-python313`. Total ext4
residency, native KVM startup/memory measurements, and leak acceptance remain
production rollout gates; wheel extraction alone does not measure these.

`agent-base` is the shared, read-only drive0 for Gregale Agents runs
(ADR-20261027090010 §2), on the same Node 22 Alpine digest as `runner-node22`. It holds
the three preset harnesses: Codex CLI, OpenCode and Claude Code, the last as
Anthropic's unmodified published binary. A preset is a binary here plus its
command in `pkg/agentpresets/presets.json`, not an image of its own. The
versions are pinned in `images/agent-base/package.json`, its integrity-pinned
lockfile and the catalog, and `pkg/agentpresets` tests that the three agree.
`finish-install.sh` refuses a harness binary that did not come from the
lockfile (OpenCode's installer can fall back to the registry), drops the
OpenCode builds this CPU and libc never run, and checks every version. The
contract check runs each preset as uid 1000 with no network and a read-only
rootfs. An agent-pool imaged stages it when `FAAS_AGENT_BASE_REF` pins a
digest: the shared base at `base/runner-agent-amd64.ext4` with guest-init and
the Grype scan sidecar vmmd's boot gate reads, plus the platform agent layer
(drive1 holding only `/etc/faas/agent.json`) at
`base/agent-layer-v1-amd64.ext4`. Unset, it stages neither.

The runtime/base images are published under `ghcr.io/poyrazk/<image>` by the
`runtime-bases` matrix in `.github/workflows/images.yml`. `builder-base` keeps
its separate multi-arch publication job. Runtime Dockerfiles are pinned to
linux/amd64 child digests because `imaged` rejects manifest-list references at
staging time.

Every matrix job validates the concrete OCI artifact it built, runs the
runtime contract smoke, and runs the shared Grype gate before promoting the
`latest` or `sha-*` tags. The hardened Python 3.13 lane rejects every CRITICAL
finding to match vmmd's fail-closed boot gate, including vendor-unfixed
findings. Legacy runtime lanes retain the fixable-CRITICAL gate until their
base migrations land separately. The builder job uses a fixed-finding HIGH
gate because it ships the BuildKit daemon and client, and performs those checks
for both linux/amd64 and linux/arm64. BuildKit, Railpack, mise, crane, and the
BuildKit source archive are checksum-verified before they enter the build or image
verification path.

For local images, use `make scan-images IMAGE_REFS="name:tag ..."`. Scanning
`dir:images/` only scans source files and is not an OCI image vulnerability
check.
