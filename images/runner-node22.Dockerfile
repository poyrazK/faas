# runner-node22 — base rootfs (drive0) for Node 22 apps and functions (spec §4.6,
# §4.9). The base is content-addressed and staged once as drive0; app images
# still contribute only their dependency/code delta as drive1.
# Content-addressed, staged to /srv/fc/base/runner-node22.ext4.
# Debug tools for `gregale app exec --debug` (ADR-20261117070620): the pinned
# static BusyBox (root:root 0111) with applet links, at /usr/lib/gregale/debug.
# guest-init mounts it only into a debug session's private mount namespace; it
# is not on any PATH. Every runtime base carries the same BusyBox.
FROM mirror.gcr.io/library/busybox:1.37.0-musl@sha256:f6a95d611e0a8780fabdb2aec43e31275b2720dfefa3e41b16835bfc42454478 AS gregale-debug-tools
RUN set -eu; mkdir -p /out/bin; cp /bin/busybox /out/busybox; \
    for applet in $(/bin/busybox --list); do ln -s ../busybox "/out/bin/$applet"; done; \
    chmod 0111 /out/busybox

FROM mirror.gcr.io/library/node:22-alpine@sha256:12f54cafa58d0e96554ae05871e29663ff7241836377dc9a7916449b8adb062c
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
# `make images-lock-update` is the only way to bump the digest.
# This runtime is intentionally self-contained rather than composed over the
# shared Debian parent: musl and glibc are not interchangeable, and applying
# Debian parent layers would produce an image that scans cleanly but cannot
# boot reliably.
# Guest runtime user (uid 1000, spec §4.8). The official Alpine image
# already reserves uid 1000 for `node`; reuse that identity under the
# platform's canonical `app` name instead of attempting a duplicate uid.
COPY --chmod=0644 guest/profiling/node/package*.json /opt/gregale/profiling/
COPY --chmod=0644 guest/tracing/node/package*.json /opt/gregale/tracing/
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt chmod 0755 /opt/gregale /opt/gregale/profiling /opt/gregale/tracing && \
    apk add --no-cache libstdc++ && apk add --no-cache --virtual .profiling-build python3 make g++ && \
    cd /opt/gregale/profiling && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev && \
    cd /opt/gregale/tracing && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev --ignore-scripts && apk del .profiling-build && \
    rm -rf /root/.npm
# zlib floor: CVE-2026-4738 (CRITICAL, fixed in Alpine zlib 1.4-r0). The
# Grype DB of 2026-11-07 flagged the published image's zlib 1.3.2-r1, and the
# base scan gate then refused every cold boot on the rc.327 node22 base
# (scan_critical). `apk upgrade` alone keeps whatever the layer cache holds;
# the explicit floor fails the build instead of shipping the vulnerable one.
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt apk upgrade --no-cache && \
    apk add --no-cache bash 'zlib>=1.4-r0' && \
    rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else adduser -D -u 1000 app; fi
# The function runner shim (guest/runners/node22) is layered in for `type:
# function` deploys at M7; plain Node apps bring their own entrypoint.
COPY --chmod=0644 guest/profiling/node.cjs /opt/gregale/profiling/node.cjs
COPY --chmod=0644 guest/tracing/node.cjs /opt/gregale/tracing/node.cjs
WORKDIR /app

# ADR-20261117070620: debug tools for `gregale app exec --debug`.
COPY --from=gregale-debug-tools /out/ /usr/lib/gregale/debug/
