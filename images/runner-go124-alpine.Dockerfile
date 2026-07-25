# runner-go124-alpine — alpine (musl) variant of the runner-go124 base
# rootfs (drive0) for Go 1.24 apps and functions (spec §4.6, §4.9). Built
# FROM the official golang:1.24-alpine image so musl libc matches the
# toolchain Railpack uses to emit the customer's static binary when the
# customer's own Dockerfile does `FROM golang:1.24-alpine AS build`.
#
# Opt-in via runtime=go124-alpine (see docs/runtimes/go124.md "Alpine
# variant" section). The base's larger purpose is disk-budget: ~250 MB
# uncompressed vs the bookworm variant's ~350 MB. ~100 MB savings on
# drive0, amortized across every go124-alpine app on the box via the
# two-drive scheme — per-app cost stays at just the static binary
# (5–30 MB), and the 130 MB/sandbox disk economics are preserved
# (CLAUDE.md "load-bearing — DO NOT fix").
#
# Customers using cgo (e.g. mattn/go-sqlite3 against glibc) MUST rebuild
# their binary against `FROM golang:1.24-alpine AS build` so the binding
# links musl — a glibc-linked binary will fail with `exec format error`
# on first wake against this base. CGO_ENABLED=0 (Railpack's default)
# works on both bases and is the drop-in case.
#
# Operator staging: build the image in CI, publish to
# ghcr.io/onebox-faas/runner-go124-alpine, then stage the produced ext4
# to /srv/fc/base/runner-go124-alpine.ext4 on the EX44 using the same
# `mkfs.ext4 -O '^has_journal' -d <staging>` recipe the bookworm
# runner-go124 / runner-node22 / runner-python312 Dockerfiles use.
# imaged does not auto-stage runtime bases (the established pattern —
# only the builder base is staged on startup).
FROM golang:1.24-alpine@sha256:REPLACE_ME_AT_MERGE_TIME
# Issue #197 B3.6 (extension): mutable tag pinned via images/Dockerfile.lock.

# Guest runtime user (uid 1000, spec §4.8).
RUN id app 2>/dev/null || adduser -u 1000 -D app

# The function runner shim (guest/runners/go124) is layered in for
# `type: function` deploys; plain Go apps bring their own entrypoint
# (the customer-emitted static binary at /app/server). The shim is
# identical to the bookworm variant — libc only differs.
WORKDIR /app
