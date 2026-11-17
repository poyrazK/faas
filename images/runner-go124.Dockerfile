# runner-go124 — base rootfs (drive0) for Go 1.24 apps and functions (spec §4.6,
# §4.9). Built from Chainguard's minimal Bash image: its maintained Wolfi
# glibc is backward-compatible with the Bookworm toolchain Railpack uses to
# emit the customer's binary, while the image keeps package metadata so the
# production scanner can assess the libraries it actually contains. The Go
# compiler and build utilities never enter the runtime image: the per-app
# layer (drive1) carries the customer's compiled handler at /app/handler
# (function mode) or /app/server (app mode), and the base only needs glibc,
# Bash, /bin/sh, and CA roots.
#
# The two-drive scheme amortizes the shared runtime libraries across every
# go124 app on the box — per-app cost is just the static binary (5-30 MB) —
# so the 130 MB/sandbox accounting is preserved (CLAUDE.md "load-bearing — DO
# NOT fix").
#
# The image is built and published by CI. imaged resolves the immutable
# per-runtime reference and auto-stages the matching ext4 on first use, so a
# newly provisioned compute node does not need a hand-copied runtime image.
# Debug tools for `gregale app exec --debug` (ADR-20261117070620): the pinned
# static BusyBox (root:root 0111) with applet links, at /usr/lib/gregale/debug.
# guest-init mounts it only into a debug session's private mount namespace; it
# is not on any PATH. Every runtime base carries the same BusyBox.
FROM mirror.gcr.io/library/busybox:1.37.0-musl@sha256:f6a95d611e0a8780fabdb2aec43e31275b2720dfefa3e41b16835bfc42454478 AS gregale-debug-tools
RUN set -eu; mkdir -p /out/bin; cp /bin/busybox /out/busybox; \
    for applet in $(/bin/busybox --list); do ln -s ../busybox "/out/bin/$applet"; done; \
    chmod 0111 /out/busybox

FROM cgr.dev/chainguard/bash:latest@sha256:e5528d697cd5fef7f52837bdc573fd4469782064fe19327bd9e83402eb0d7b0d
# Issue #197 B3.6 (extension): mutable tag pinned via images/Dockerfile.lock.

# Guest runtime user (uid 1000, spec §4.8). Copying the shared skeleton also
# avoids carrying account-management tools solely to create two static rows.
COPY images/rootfs-skel/ /
# MicroVM workloads cannot log in and the shared skeleton has no passwords.
# Drop Wolfi's mode-000 placeholder so unprivileged ext4 assembly can copy the
# complete runtime tree without granting read access to a meaningless file.
RUN rm -f /etc/shadow

# The function runner shim (guest/runners/go124) is layered in for
# `type: function` deploys; plain Go apps bring their own entrypoint
# (the customer-emitted static binary at /app/server).
WORKDIR /app

# ADR-20261117070620: debug tools for `gregale app exec --debug`.
COPY --from=gregale-debug-tools /out/ /usr/lib/gregale/debug/
