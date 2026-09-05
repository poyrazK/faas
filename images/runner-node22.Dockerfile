# runner-node22 — base rootfs (drive0) for Node 22 apps and functions (spec §4.6,
# §4.9). The base is content-addressed and staged once as drive0; app images
# still contribute only their dependency/code delta as drive1.
# Content-addressed, staged to /srv/fc/base/runner-node22.ext4.
FROM node:22-alpine@sha256:76789712cd1ae89a1225eac9077010d68987a423588042dac30446f502f1858c
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
# `make images-lock-update` is the only way to bump the digest.
# This runtime is intentionally self-contained rather than composed over the
# shared Debian parent: musl and glibc are not interchangeable, and applying
# Debian parent layers would produce an image that scans cleanly but cannot
# boot reliably.
# Guest runtime user (uid 1000, spec §4.8). The official Alpine image
# already reserves uid 1000 for `node`; reuse that identity under the
# platform's canonical `app` name instead of attempting a duplicate uid.
RUN apk upgrade --no-cache && \
    apk add --no-cache bash && \
    rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else adduser -D -u 1000 app; fi
# The function runner shim (guest/runners/node22) is layered in for `type:
# function` deploys at M7; plain Node apps bring their own entrypoint.
WORKDIR /app
