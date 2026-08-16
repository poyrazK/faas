# ADR-114 — Multi-arch builder-base image pipeline

- **Status:** Proposed
- **Date:** 2026-08-16
- **Decision:** The builder-base rootfs that backs every ephemeral
  builder microVM is sourced from `images/builder-base.Dockerfile`
  and published to `ghcr.io/onebox-faas/builder-base` for
  `linux/amd64 + linux/arm64` by `.github/workflows/images.yml` on
  every push to `main`. PR builds run with `--push=false` and a
  post-build crane-based verification step asserts
  `/usr/local/bin/faas-guest-init` exists in the published image's
  rootfs. Operators consume the digest via `FAAS_BUILDER_BASE_REF` in
  `/etc/faas/sealed.env`; the example file ships with a TODO
  instructing them to update after the first `main` push.

- **Why:** The GCP split-box fleet deploy surfaced three blockers
  that prevented the build pipeline from working at all (issue
  #938). Two were vmmd self-registration bugs (see PR-A); the third
  was that **nothing published `builder-base`**. `images/builder-
  base.Dockerfile` existed and was complete (debian:12-slim +
  BuildKit 0.31.2 + Railpack 0.31.1 + git + the guest-init binary),
  but `grep -l 'docker buildx|ghcr.io/onebox-faas' .github/workflows/`
  returned zero matches. The only artifact flowing into imaged was
  the operator-set `FAAS_BUILDER_BASE_REF`, which defaulted to
  `mirror.gcr.io/library/alpine` in `deploy/controlplane/sealed.env.
  example:25` — a public-registry placeholder left over from when
  `ghcr.io/onebox-faas/builder-base` was private. The result: imaged
  stages alpine as `builder-base.ext4`; the resulting VM boots
  busybox with no `faas-guest-init`, no railpack, no buildkit; every
  build times out at vmmd's `waitReady` (30s); every build fails.

  Spec §4.5 mandates the builder rootfs contain BuildKit, Railpack,
  git, and the OCI exporter. ADR-003 mandates builds run inside
  ephemeral builder microVMs. Neither was actually enforced end-to-
  end — the contract was implicit in the Dockerfile but the
  pipeline that produced a published artifact was missing. This
  ADR closes that gap.

  Concretely:

  1. **`images/builder-base.Dockerfile` is the source of truth for
     the rootfs shape.** debian:12-slim (digest-pinned via
     `images/Dockerfile.lock`), BuildKit, Railpack, git, OCI
     exporter, and the `guest-init` binary copied from
     `guest/init/faas-guest-init` (the build artifact produced by
     `go build -o guest/init/faas-guest-init ./guest/init`). The
     `COPY guest/init/faas-guest-init` path is unambiguous so the
     same contract works for both the CI workflow (which builds the
     binary first) and the Lima local-build path
     (`deploy/lima/faas-metal.yaml:353-509`).

  2. **`.github/workflows/images.yml` is the publisher.** Trigger:
     push to `main` paths-filtered on the Dockerfile, the digest
     lock, the guest-init source, or this workflow file. PRs and
     `workflow_dispatch` build with `--output type=image,push=false`.
     Multi-arch (`linux/amd64,linux/arm64`) for parity with the
     Lima arm64 dev loop; operators pin the amd64-specific digest
     in `sealed.env` because imaged rejects manifest lists at
     `cmd/imaged/main.go:392-401`. Concurrency group is
     `images-${{ github.ref }}` with `cancel-in-progress: true` so
     re-pushes don't pile up.

  3. **The verify step is the load-bearing contract.** After every
     build (push or PR), `crane export` the linux/amd64 manifest
     to a tar, untar it, and assert
     `/usr/local/bin/faas-guest-init` is executable. The same
     assertion runs in the Lima path
     (`deploy/lima/faas-metal.yaml:482-490`); this is the CI
     equivalent. The CI verify step is what prevents a future
     regression where the Dockerfile `COPY` path silently stops
     matching the build artifact path.

  4. **The `gregalectl doctor` `builder-base-ext4` check is the
     runtime equivalent.** Always-on, severity-graded:
     - ext4 missing → SeverityWarn (imaged stages on first cold
       boot; the finding self-resolves after imaged has run).
     - ext4 present, debugfs absent → SeverityWarn (macOS dev
       boxes / minimal containers; install e2fsprogs for full
       coverage — the ansible `firecracker` role ensures this on
       production control-plane nodes).
     - ext4 present, debugfs runs, file absent → SeverityError.
       This is the load-bearing case: the alpine placeholder from
       `sealed.env.example` produces exactly this finding, so the
       operator sees the broken state before running
       `gregale deploy`.
     Path is configurable via `FAAS_BUILDER_BASE_PATH` (mirroring
     `cmd/imaged/main.go:403`); empty keeps the default
     `/srv/fc/base/builder-base.ext4`.

  5. **`FAAS_BUILDER_BASE_REF` ships with a TODO.** The
     `sealed.env.example` file preserves the alpine ref but
     replaces the comment block with explicit instructions to
     resolve the new digest via `crane digest ghcr.io/onebox-faas/
     builder-base:latest --platform linux/amd64`. The TODO exists
     because the first `main` push publishes the digest; until
     then, the example file can't pin it.

  6. **`e2fsprogs` is installed by the ansible `firecracker` role.**
     This guarantees `debugfs` is on the production control-plane
     PATH so the doctor check runs in its error-severity mode (not
     the degraded warn mode).

- **Consequences:**

  - The PRs in the cluster are atomic and reviewable in <10 min:
    (1) the new workflow file, (2) the Dockerfile `COPY` fix,
    (3) the `sealed.env.example` TODO, (4) the doctor check +
    ansible `e2fsprogs` (one commit because they're load-bearing
    together), (5) this ADR.

  - The Lima local-build path Just Works without further edits
    because the Dockerfile `COPY` now points at the path Lima
    already produces (`guest/init/faas-guest-init`). The
    post-build sanity check at
    `deploy/lima/faas-metal.yaml:482-490` stops catching the
    wrong-COPY-path bug because the bug no longer exists.

  - imaged's startup check at `cmd/imaged/main.go:392-401` is
    unchanged: it rejects bare tags and manifest lists. Operators
    must pin the amd64-specific digest. The TODO comment in
    `sealed.env.example` documents this.

  - Operators on heterogeneous clusters pin via TOML/env per-host
    (PR-A's `FAAS_VCPU_BUDGET` precedent, not repeated here). The
    image identity is per-arch-digest, not per-tag.

  - The first `main` push produces a real artifact. Until that
    push happens, a deploy that runs `gregale deploy` will see
    empty builder VMs (the alpine placeholder path). The TODO
    comment + the doctor check make this state loud, not silent.

- **Related:** spec §4.5 (builder rootfs shape); ADR-003 (builds
  in VMs, never on host); ADR-005 (cold boot always works;
  snapshots are cache, not truth); ADR-040 (OCI layer symlink
  policy — distinct decision, not superseded); ADR-110 (split-box
  fleet plumbing); ADR-111 (Gregale Compute Image); ADR-112
  (per-role image collapse); issue #938.

- **Supersedes:** none. The historical "why alpine" comment in
  `sealed.env.example:16-17` was never an ADR; it was a
  placeholder. ADR-040 (the symlink policy) is unrelated to this
  decision.