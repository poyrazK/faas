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
# Railpack is pulled as an upstream release tarball (it is not packaged in
# Alpine). BuildKit and runc are compiled from their checksum-pinned sources
# below so the image does not inherit stale Go dependencies from opaque
# upstream binaries. Versions are pinned via build-args so CI can override
# them per release without churning this file.

# ---- railpack (Node/Python builder, spec §4.5) ---------------------------
# Upstream switched from flat `-linux-amd64` binaries to Rust-target-triple
# names in v0.10+. The current naming is `-x86_64-unknown-linux-musl` /
# `-arm64-unknown-linux-musl`. v0.5.0 with the old naming is no longer
# published, so bumping to v0.38.0 (current stable as of 2026-08) is mandatory.
ARG RAILPACK_VERSION=0.38.0
ARG RAILPACK_SHA256_AMD64=7c3f0e70ca8bf80bde87e8c30cb0171414c2b6bbd794d6f60a19cc3b71772950
ARG RAILPACK_SHA256_ARM64=d33716e87f0e39314898746c806e26d9edde890ac65156891b2f06c8d07ba8c4

# Railpack 0.38.0 bootstraps mise 2026.7.6 using its glibc linux-x64
# asset. The builder rootfs is Alpine, so stage the matching musl asset and
# let guest-init seed Railpack's expected cache path before prepare runs.
ARG MISE_VERSION=2026.7.6
ARG MISE_SHA256_AMD64=debdf9d7e776c3c0f9e3dfa7c4067bf02f3592fdc7c5d2bb5027fb2325c9916f
ARG MISE_SHA256_ARM64=926914f938c55e86e48875f1c9253573ddf6d5efb5abb6e8721ea061fe2767f7

# ---- buildkit (Dockerfile builds, spec §4.5 fallback path) ----------------
# Rootless inside the VM — rootless-runc inside a VM is functionally root, and
# the VM boundary is the actual security perimeter (ADR-003).
ARG BUILDKIT_VERSION=0.32.2
ARG BUILDKIT_SOURCE_SHA256=b19deba3f8cf3eb05407aa85c246e22839770c437439a04d880ef3d645aed0aa
ARG GO_ARCHIVE_VERSION=0.3.0

# The latest upstream runc release still embeds golang.org/x/net v0.50.0 and
# Go 1.25.12, which leaves this image exposed to fixed HIGH advisories. Build
# the same release from checksum-pinned source with the image's Go 1.26.6
# toolchain and an explicit dependency floor. The static-pie result runs on
# Alpine without inheriting the host libc.
ARG RUNC_VERSION=1.5.1
ARG RUNC_SOURCE_SHA256=32286f18899a644ec7c1589688a9600ba54cc65264f23f1f5877ba214ca76e75

# ---- guest-init version (issue #938 / PR-B / ADR-114) -------------------
# Multi-arch builds CANNOT pre-stage guest-init in the build context because
# both arches would overwrite the same host path (review finding #2 on PR
# #940). Instead, build guest-init inside the Dockerfile via cross-compile
# so each arch's binary lands in its own image. The Go builder base is
# digest-pinned via images/Dockerfile.lock just like alpine:3.22 below.
# Issue #938: building guest-init inside the image (instead of in the
# workflow) also lets the Lima local-build path stage a multi-arch rootfs
# via buildx without per-arch file juggling.
# Note: the version is intentionally baked into the FROM line (no ARG)
# so images/Dockerfile.lock has a literal "golang:1.26.6" alias to
# match against. Bumping the Go version is a two-step: change this
# line, run `make images-lock-update` to refresh the lock and digest.
# BuildKit v0.32.x requires Go 1.26.3 or newer. Use 1.26.6 so the
# builder itself is not shipped with the Go standard-library advisories
# fixed after 1.25.9; the repo's `tool` directive also rejects older
# toolchains with `unknown directive: tool` (verified during PR #940
# review).

# ---- stage 1: build guest-init for the target arch -----------------------
# Image registry digest pinned via images/Dockerfile.lock; make
# images-lock-update resolves the current digest and rewrites BOTH
# this line and the lock entry. The base manifest-list digest pins
# every per-arch child manifest so buildx's per-arch resolution
# stays race-free under multi-arch build (per-arch digests are not
# stable across re-pulls, but the manifest-list digest is). Use the native
# build platform for the toolchain and cross-compile the target artifact; this
# avoids emulating the Go compiler for arm64 multi-arch builds.
FROM --platform=$BUILDPLATFORM golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 AS guest-init-build
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

# ---- runc (builder OCI runtime) -----------------------------------------
# The official runc asset is intentionally not copied into the final image:
# its embedded module metadata contains fixed HIGH vulnerabilities. Build the
# pinned release from source instead. We build on TARGETPLATFORM because runc
# enables cgo for seccomp; buildx's QEMU path is bounded to this small binary
# and keeps the cross-compiled BuildKit stages native and fast.
FROM --platform=$TARGETPLATFORM golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 AS runc-build
WORKDIR /src/runc
ARG RUNC_VERSION
ARG RUNC_SOURCE_SHA256
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl libseccomp-dev && \
      rm -rf /var/lib/apt/lists/* && \
      curl -fsSL -o /tmp/runc-source.tgz \
        "https://github.com/opencontainers/runc/archive/refs/tags/v${RUNC_VERSION}.tar.gz" && \
      echo "${RUNC_SOURCE_SHA256}  /tmp/runc-source.tgz" | sha256sum -c - && \
      tar -xzf /tmp/runc-source.tgz --strip-components=1 -C /src/runc && \
      rm /tmp/runc-source.tgz && \
      go mod edit -require=golang.org/x/net@v0.57.0 && \
      go mod download && \
      CGO_ENABLED=1 GOOS=linux GOARCH=${TARGETARCH} \
        go build -mod=mod -trimpath -buildmode=pie \
          -tags "seccomp urfave_cli_no_docs netgo osusergo" \
          -ldflags "-linkmode external -extldflags -static-pie -X main.gitCommit=v${RUNC_VERSION}" \
          -o /out/runc . && \
      go version -m /out/runc | tee /tmp/runc-build-info && \
      grep -q 'golang.org/x/net.*v0.57.0' /tmp/runc-build-info && \
      ! grep -Eq 'v0.50.0|go1.25.12' /tmp/runc-build-info

# BuildKit's server has a deliberately strict session liveness check. The
# stock buildctl release has no flag for its per-session timeout header, while
# slow bare-metal builders can spend several minutes importing a remote layer.
# Build both upstream binaries from the same source tree so the repository
# patch and the dependency floors apply consistently to buildctl and buildkitd.
FROM --platform=$BUILDPLATFORM golang:1.26.6@sha256:0d1d3a794be25f809dd2cb3160d8c73276c4056a9f8242a138e908ddeee7b6b6 AS buildkit-client-build
WORKDIR /src/buildkit
ARG BUILDKIT_VERSION
ARG TARGETOS
ARG TARGETARCH
ARG BUILDKIT_SOURCE_SHA256
ARG GO_ARCHIVE_VERSION
COPY images/buildkit-session-health.patch /tmp/buildkit-session-health.patch
# BuildKit 0.32.2 still selects the vulnerable go-archive v0.2.0 and gRPC
# v1.82.1. Keep the source release's vendored dependency graph for a fast,
# reproducible build, but replace the source's vulnerable modules with fixed
# releases, including the Go crypto fix, before compiling. x/net is already
# fixed in this source release; pin it explicitly so a future source change
# cannot lower the builder's dependency floor
# unnoticed.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git && \
      rm -rf /var/lib/apt/lists/* && \
      curl -fsSL -o /tmp/buildkit-source.tgz \
        "https://github.com/moby/buildkit/archive/refs/tags/v${BUILDKIT_VERSION}.tar.gz" && \
      echo "${BUILDKIT_SOURCE_SHA256}  /tmp/buildkit-source.tgz" | sha256sum -c - && \
      tar -xzf /tmp/buildkit-source.tgz --strip-components=1 -C /src/buildkit && \
      rm /tmp/buildkit-source.tgz && \
      git apply /tmp/buildkit-session-health.patch && \
      go mod edit -require=github.com/moby/go-archive@v${GO_ARCHIVE_VERSION} && \
      go mod edit -require=golang.org/x/net@v0.57.0 && \
      go mod edit -require=golang.org/x/crypto@v0.56.0 && \
      go mod edit -require=google.golang.org/grpc@v1.83.1 && \
      go mod download github.com/moby/go-archive@v${GO_ARCHIVE_VERSION} google.golang.org/grpc@v1.83.1 golang.org/x/crypto@v0.56.0 && \
      archive_module="$(go env GOMODCACHE)/github.com/moby/go-archive@v${GO_ARCHIVE_VERSION}" && \
      grpc_module="$(go env GOMODCACHE)/google.golang.org/grpc@v1.83.1" && \
      crypto_module="$(go env GOMODCACHE)/golang.org/x/crypto@v0.56.0" && \
      rm -rf vendor/github.com/moby/go-archive && \
      rm -rf vendor/google.golang.org/grpc && \
      rm -rf vendor/golang.org/x/crypto && \
      cp -a "${archive_module}" vendor/github.com/moby/go-archive && \
      cp -a "${grpc_module}" vendor/google.golang.org/grpc && \
      cp -a "${crypto_module}" vendor/golang.org/x/crypto && \
      sed -i \
        -e "s#github.com/moby/go-archive v0.2.0#github.com/moby/go-archive v${GO_ARCHIVE_VERSION}#" \
        -e "s#golang.org/x/crypto v0.54.0#golang.org/x/crypto v0.56.0#" \
        -e "s#google.golang.org/grpc v1.82.1#google.golang.org/grpc v1.83.1#" \
        vendor/modules.txt && \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
        go build -mod=vendor -trimpath -o /out/buildkitd ./cmd/buildkitd && \
      CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
        go build -mod=vendor -trimpath -o /out/buildctl ./cmd/buildctl

# ---- stage 2: assemble the runtime rootfs -------------------------------
# See the stage 1 FROM above re: $TARGETPLATFORM handling.
# Docker's build-time /etc/resolv.conf is a read-only injected mount, so keep
# the guest resolver as a real file in a scratch stage and COPY it into the
# final filesystem. The guest-init fallback remains for operator overrides.
FROM scratch AS builder-resolver
COPY images/builder-resolv.conf /etc/resolv.conf

# Alpine supplies the small, currently supported userland for the builder
# VM. The image reference is digest-pinned via images/Dockerfile.lock.
FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
# Issue #197 B3.5: the `alpine:3.22` tag is mutable. The digest is
# pinned via images/Dockerfile.lock; `make images-lock-update` resolves
# the current registry digest and updates BOTH the lock and the FROM
# line. CI runs `make images-lock-check` to fail any PR that drifts.
ARG RAILPACK_VERSION
ARG BUILDKIT_VERSION
ARG MISE_VERSION
ARG MISE_SHA256_AMD64
ARG MISE_SHA256_ARM64
ARG TARGETARCH
ARG RAILPACK_SHA256_AMD64
ARG RAILPACK_SHA256_ARM64
ARG RUNC_VERSION

# Railpack's mise/python-build path executes Bash scripts. Alpine's BusyBox
# /bin/sh is not a substitute when invoked as "bash"; keep the real Bash
# package in the runtime builder rootfs.
RUN apk add --no-cache \
      bash git ca-certificates curl xz shadow-subids fuse-overlayfs util-linux util-linux-misc && \
    apk upgrade --no-cache

# Install the source-built static-pie runtime. The build stage applies the
# same target-architecture selection that the final image uses.
COPY --from=runc-build /out/runc /usr/bin/runc
RUN chmod 0755 /usr/bin/runc

# guest-init and BuildKit use the stable platform path for the OCI runtime;
# the source-built static-pie binary above is installed under /usr/bin.
RUN ln -s /usr/bin/runc /usr/local/bin/runc

# guest-init uses util-linux unshare's automatic subordinate-ID mapping. The
# BusyBox applet does not provide --map-auto, so assert the actual runtime
# contract while assembling the image instead of discovering a
# stale/incomplete builder rootfs only after a VM has booted.
RUN test -x /usr/local/bin/runc && \
    test -x /usr/bin/unshare && \
    /usr/bin/unshare --help 2>&1 | grep -q -- '--map-auto'

# Rootless BuildKit runs inside the builder microVM's user namespace. Give
# the mapped root a bounded subordinate range so runc can materialise image
# ownership (for example root:shadow) without falling back to host access.
RUN printf 'root:100000:65536\n' > /etc/subuid && \
    printf 'root:100000:65536\n' > /etc/subgid

# BuildKit rootless. Both binaries come from the source build above; using the
# upstream release tarball here would reintroduce its older Go modules and
# standard library into the final image.
COPY --from=buildkit-client-build /out/buildkitd /usr/local/bin/buildkitd
COPY --from=buildkit-client-build /out/buildctl /usr/local/bin/buildctl
RUN chmod 0755 /usr/local/bin/buildkitd /usr/local/bin/buildctl

# Railpack. The current naming convention is `<ver>-<arch>-unknown-linux-musl.tar.gz`
# where <arch> is `x86_64` or `arm64`. We resolve the right arch from TARGETARCH.
RUN case "${TARGETARCH}" in \
      amd64) RAILPACK_ARCH=x86_64 ;; \
      arm64) RAILPACK_ARCH=arm64 ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/railpack.tgz \
      "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/railpack-v${RAILPACK_VERSION}-${RAILPACK_ARCH}-unknown-linux-musl.tar.gz" && \
    case "${TARGETARCH}" in \
      amd64) RAILPACK_SHA256="${RAILPACK_SHA256_AMD64}" ;; \
      arm64) RAILPACK_SHA256="${RAILPACK_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    echo "${RAILPACK_SHA256}  /tmp/railpack.tgz" | sha256sum -c - && \
    tar -C /usr/local/bin -xzf /tmp/railpack.tgz railpack && \
      chmod +x /usr/local/bin/railpack && \
      rm /tmp/railpack.tgz && \
      /usr/local/bin/railpack --version

# Railpack currently downloads a glibc mise asset at build time. Keep a
# musl-compatible copy in the builder image; guest-init stages it into the
# tmpfs-backed Railpack cache immediately before the build starts.
RUN case "${TARGETARCH}" in \
      amd64) MISE_ARCH=x64 ;; \
      arm64) MISE_ARCH=arm64 ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    mkdir -p /usr/local/lib/faas/mise /opt/mise && \
    curl -fsSL -o /tmp/mise.tgz \
      "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-${MISE_ARCH}-musl.tar.gz" && \
    case "${TARGETARCH}" in \
      amd64) MISE_SHA256="${MISE_SHA256_AMD64}" ;; \
      arm64) MISE_SHA256="${MISE_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    echo "${MISE_SHA256}  /tmp/mise.tgz" | sha256sum -c - && \
    tar -C /opt/mise -xzf /tmp/mise.tgz && \
    install -m 0755 /opt/mise/mise/bin/mise \
      "/usr/local/lib/faas/mise/mise-${MISE_VERSION}" && \
    rm -rf /opt/mise /tmp/mise.tgz

# curl is only an image-build transport; the guest uses BuildKit, Railpack,
# git, and fuse-overlayfs at runtime. Removing curl (and its orphaned
# libcurl dependency) keeps the builder admission scan free of transport
# vulnerabilities that cannot be fixed by the current Alpine repository.
RUN apk del curl

# guest-init copied from the build stage. Each arch's manifest receives the
# arch-matching binary because buildx resolves TARGETARCH per image in the
# multi-arch build — no host-side pre-build required, no overwrite bug.
COPY --from=builder-resolver /etc/resolv.conf /etc/resolv.conf
COPY --from=guest-init-build /out/faas-guest-init /usr/local/bin/faas-guest-init
RUN chmod +x /usr/local/bin/faas-guest-init

WORKDIR /build

# BuildKit is deliberately launched rootless inside the builder VM's user
# namespace. The workspace is disposable VM-local state, so it must be
# writable by the mapped worker uid rather than relying on the image's root
# ownership surviving OCI-to-ext4 materialisation.
RUN chmod 0777 /build
