# runner-go124 — base rootfs (drive0) for Go 1.24 apps and functions (spec §4.6,
# §4.9). Built FROM the official golang:1.24-bookworm image so glibc matches
# the toolchain Railpack uses to emit the customer's static binary. The
# per-app layer (drive1) carries the customer's compiled handler at
# /app/handler (function mode) or /app/server (app mode); the base only
# needs the runtime + standard library.
#
# This base is the largest of the three runtimes (~350 MB uncompressed).
# The two-drive scheme amortizes it across every go124 app on the box —
# per-app cost is just the static binary (5-30 MB) — so the 130 MB/sandbox
# accounting is preserved (CLAUDE.md "load-bearing — DO NOT fix").
#
# Operator staging: build the image in CI, publish to
# ghcr.io/onebox-faas/runner-go124, then stage the produced ext4 to
# /srv/fc/base/runner-go124.ext4 on the EX44 using the same
# `mkfs.ext4 -O '^has_journal' -d <staging>` recipe the existing
# runner-node22 / runner-python312 Dockerfiles use. imaged does not
# auto-stage runtime bases (the established pattern — only the
# builder base is staged on startup).
FROM golang:1.24-bookworm@sha256:REPLACE_ME_AT_MERGE_TIME
# Issue #197 B3.6 (extension): mutable tag pinned via images/Dockerfile.lock.

# Guest runtime user (uid 1000, spec §4.8).
RUN id app 2>/dev/null || useradd -u 1000 -m app

# The function runner shim (guest/runners/go124) is layered in for
# `type: function` deploys; plain Go apps bring their own entrypoint
# (the customer-emitted static binary at /app/server).
WORKDIR /app
