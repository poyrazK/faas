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

FROM --platform=$TARGETPLATFORM debian:12-slim
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

# guest-init is built by `make guest-init` (or by CI before the docker build)
# and stamped in at image-build time. The kernel cmdline hands PID1 to this
# binary regardless of app vs build mode (mode is decided by which manifest
# file exists at /etc/faas/).
COPY guest-init /usr/local/bin/faas-guest-init
RUN chmod +x /usr/local/bin/faas-guest-init

WORKDIR /build
