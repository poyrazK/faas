# builder-base — rootfs for the ephemeral builder microVM (spec §4.5, ADR-003).
# Contains BuildKit, Railpack, git, and the OCI exporter. Builds run INSIDE this
# VM so untrusted `npm install` gets VM-grade isolation; the 2 GB RAM cap is the
# VM boundary. Never run builds in a host container.
#
# Multi-arch: TARGETARCH is set automatically by `docker buildx build`. The
# EX44 builds --platform=linux/amd64 (production target). The Lima dev loop
# builds --platform=linux/arm64 so the local metal-lima path can exercise a
# real builder VM end-to-end. The arm64 build does NOT replace the §14 M6
# acceptance gates — those still need the EX44 — but it does prove the
# spawn → runBuild → DestroyWithExport path works against a real artifact
# producing engine rather than a busybox stub.
#
# Both railpack and buildkit are pulled as upstream release tarballs (neither is
# packaged in debian:bookworm). Versions are pinned via build-args so CI can
# override them per release without churning this file.

# ---- railpack (Node/Python builder, spec §4.5) ---------------------------
# Upstream switched from flat `-linux-amd64` binaries to Rust-target-triple
# names in v0.10+. The current naming is `-x86_64-unknown-linux-musl` /
# `-arm64-unknown-linux-musl`. v0.5.0 with the old naming is no longer
# published, so bumping to v0.31.1 (current stable as of 2026-07) is mandatory.
ARG RAILPACK_VERSION=0.31.1

# ---- buildkit (Dockerfile builds, spec §4.5 fallback path) ----------------
# Rootless inside the VM — rootless-runc inside a VM is functionally root, and
# the VM boundary is the actual security perimeter (ADR-003).
ARG BUILDKIT_VERSION=0.31.2

# ---- guest-init version (issue #938 / PR-B / ADR-114) -------------------
# Multi-arch builds CANNOT pre-stage guest-init in the build context because
# both arches would overwrite the same host path (review finding #2 on PR
# #940). Instead, build guest-init inside the Dockerfile via cross-compile
# so each arch's binary lands in its own image. The Go builder base is
# digest-pinned via images/Dockerfile.lock just like debian:12-slim below.
# Issue #938: building guest-init inside the image (instead of in the
# workflow) also lets the Lima local-build path stage a multi-arch rootfs
# via buildx without per-arch file juggling.
# Note: the version is intentionally baked into the FROM line (no ARG)
# so images/Dockerfile.lock has a literal "golang:1.25.7" alias to
# match against. Bumping the Go version is a two-step: change this
# line, run `make images-lock-update` to refresh the lock and digest.
# We use 1.25.7 (not 1.23.x) because go.mod in this repo declares
# `go 1.25.7` and a `tool` directive that older Go versions reject
# with `unknown directive: tool` (verified during PR #940 review).

# ---- stage 1: build guest-init for the target arch -----------------------
# Image registry digest pinned via images/Dockerfile.lock; make
# images-lock-update resolves the current digest and rewrites BOTH
# this line and the lock entry. The base manifest-list digest pins
# every per-arch child manifest so buildx's per-arch resolution
# stays race-free under multi-arch build (per-arch digests are not
# stable across re-pulls, but the manifest-list digest is).
FROM --platform=$TARGETPLATFORM golang:1.25.7@sha256:5a79b94c34c299ac0361fbb7c7fca6dc552e166b42341050323fa3ab137d7be9 AS guest-init-build
WORKDIR /src
# guest-init is a pure-Go binary; no submodule vendoring needed. The
# repository is the build context, so COPY . picks up the whole tree.
# .dockerignore (repo root) keeps secrets, .git, and local caches out.
COPY . /src
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -tags linux \
        -o /out/faas-guest-init ./guest/init

# ---- stage 2: assemble the runtime rootfs -------------------------------
FROM --platform=$TARGETPLATFORM debian:12-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241
# Issue #197 B3.5: the `debian:12-slim` tag is mutable. The digest is
# pinned via images/Dockerfile.lock; `make images-lock-update` resolves
# the current registry digest and updates BOTH the lock and the FROM
# line. CI runs `make images-lock-check` to fail any PR that drifts.
ARG RAILPACK_VERSION
ARG BUILDKIT_VERSION
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates curl xz-utils uidmap fuse-overlayfs && \
    rm -rf /var/lib/apt/lists/*

# BuildKit rootless. Two files: buildkitd (daemon) + buildctl (client). The
# upstream tarball unpacks both into ./bin/.
RUN mkdir -p /opt/buildkit && \
    curl -fsSL -o /tmp/buildkit.tgz \
      "https://github.com/moby/buildkit/releases/download/v${BUILDKIT_VERSION}/buildkit-v${BUILDKIT_VERSION}.linux-${TARGETARCH}.tar.gz" && \
    tar -C /opt/buildkit -xzf /tmp/buildkit.tgz && \
    rm /tmp/buildkit.tgz && \
    install -m 0755 /opt/buildkit/bin/buildkitd /usr/local/bin/buildkitd && \
    install -m 0755 /opt/buildkit/bin/buildctl   /usr/local/bin/buildctl && \
    rm -rf /opt/buildkit

# Railpack. The current naming convention is `<ver>-<arch>-unknown-linux-musl.tar.gz`
# where <arch> is `x86_64` or `arm64`. We resolve the right arch from TARGETARCH.
RUN case "${TARGETARCH}" in \
      amd64) RAILPACK_ARCH=x86_64 ;; \
      arm64) RAILPACK_ARCH=arm64 ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/railpack.tgz \
      "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/railpack-v${RAILPACK_VERSION}-${RAILPACK_ARCH}-unknown-linux-musl.tar.gz" && \
    tar -C /usr/local/bin -xzf /tmp/railpack.tgz railpack && \
    chmod +x /usr/local/bin/railpack && \
    rm /tmp/railpack.tgz && \
    /usr/local/bin/railpack --version

# guest-init copied from the build stage. Each arch's manifest receives the
# arch-matching binary because buildx resolves TARGETARCH per image in the
# multi-arch build — no host-side pre-build required, no overwrite bug.
COPY --from=guest-init-build /out/faas-guest-init /usr/local/bin/faas-guest-init
RUN chmod +x /usr/local/bin/faas-guest-init

WORKDIR /build
