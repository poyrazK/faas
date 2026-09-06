# runner-go124-alpine — alpine (musl) variant of the runner-go124 base
# rootfs (drive0) for Go 1.24 apps and functions (spec §4.6, §4.9). Built
# FROM the official golang:1.24-alpine image so musl libc matches the
# toolchain Railpack uses to emit the customer's static binary when the
# customer's own Dockerfile does `FROM golang:1.24-alpine AS build`.
#
# Opt-in via runtime=go124-alpine (see docs/runtimes/go124.md "Alpine
# variant" section). The base's larger purpose is disk-budget: musl keeps the
# final image smaller than the glibc variant. The Go compiler/toolchain is
# removed from both final runtime images; per-app cost stays at just the
# static binary (5–30 MB), and the 130 MB/sandbox disk economics are
# preserved (CLAUDE.md "load-bearing — DO NOT fix").
#
# Customers using cgo (e.g. mattn/go-sqlite3 against glibc) MUST rebuild
# their binary against `FROM golang:1.24-alpine AS build` so the binding
# links musl — a glibc-linked binary will fail with `exec format error`
# on first wake against this base. CGO_ENABLED=0 (Railpack's default)
# works on both bases and is the drop-in case.
#
# The image is built and published by CI. imaged resolves the immutable
# per-runtime reference and auto-stages the matching ext4 on first use, so a
# newly provisioned compute node does not need a hand-copied runtime image.
FROM golang:1.24-alpine@sha256:757779acac4af1b349a20f357c7296097b4a0b89da4ad0e370b339060077282a
# Issue #197 B3.6 (extension): mutable tag pinned via images/Dockerfile.lock.

# Guest runtime user (uid 1000, spec §4.8).
RUN apk upgrade --no-cache && \
    apk add --no-cache bash && \
    rm -rf /usr/local/go && \
    (id app 2>/dev/null || adduser -u 1000 -D app)

# The function runner shim (guest/runners/go124) is layered in for
# `type: function` deploys; plain Go apps bring their own entrypoint
# (the customer-emitted static binary at /app/server). The shim is
# identical to the bookworm variant — libc only differs.
WORKDIR /app
