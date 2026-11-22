# runner-node24 — base rootfs (drive0) for Node 24 LTS function
# deploys (spec §4.6, §4.9). Pairs with guest/runners/node24/main.go.
# Content-addressed, staged to /srv/fc/base/runner-node24.ext4.
#
# Tier 1 PR 2 (ADR-052): this base is auto-staged by imaged through
# pkg/imaged/base_stage.go::EnsureRuntimeBase. Production nodes receive
# a digest-pinned ref from the deployment pipeline; no manual ext4 copy is
# part of the supported workflow.
#
# The two-drive scheme amortizes this base across every node24 app on
# the box — per-app cost is just the customer's package.json-resolved
# node_modules + handler. The 130 MB/sandbox accounting is preserved
# (CLAUDE.md "load-bearing — DO NOT fix").
# Debug tools for `gregale app exec --debug` (ADR-20261117070620): the pinned
# static BusyBox (root:root 0555) with applet links, at /usr/lib/gregale/debug.
# guest-init mounts it only into a debug session's private mount namespace; it
# is not on any PATH. Every runtime base carries the same BusyBox.
FROM mirror.gcr.io/library/busybox:1.37.0-musl@sha256:f6a95d611e0a8780fabdb2aec43e31275b2720dfefa3e41b16835bfc42454478 AS gregale-debug-tools
RUN set -eu; mkdir -p /out/bin; cp /bin/busybox /out/busybox; \
    for applet in $(/bin/busybox --list); do ln -s ../busybox "/out/bin/$applet"; done; \
    chmod 0555 /out/busybox

FROM mirror.gcr.io/library/node:24-trixie-slim@sha256:60587baa7c895cf438d44f691e4ac8248b36c13b26be77332f874bebb3a64201
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
# The official image already reserves uid 1000 for `node`; reuse that
# identity under the platform's canonical `app` name instead of attempting
# a duplicate uid.
COPY --chmod=0644 guest/profiling/node/package*.json /opt/gregale/profiling/
COPY --chmod=0644 guest/tracing/node/package*.json /opt/gregale/tracing/
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt chmod 0755 /opt/gregale /opt/gregale/profiling /opt/gregale/tracing && \
    apt-get update && apt-get install -y --no-install-recommends python3 make g++ && \
    cd /opt/gregale/profiling && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev && \
    cd /opt/gregale/tracing && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev --ignore-scripts && \
    apt-get purge -y --auto-remove python3 make g++ && rm -rf /root/.npm /var/lib/apt/lists/*
# libatomic1: guest-init skips Railpack's default apt step on shared runtime
# bases because the base ships what it installs. Without it every app whose
# start command runs pnpm (mise's pnpm 11 binary) crashed at startup with
# "libatomic.so.1: cannot open shared object file" (prod hunt r28).
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libatomic1 && \
    rm -rf /var/lib/apt/lists/* /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else useradd -u 1000 -m app; fi
COPY --chmod=0644 guest/profiling/node.cjs /opt/gregale/profiling/node.cjs
COPY --chmod=0644 guest/tracing/node.cjs /opt/gregale/tracing/node.cjs
WORKDIR /app

# ADR-20261117070620: debug tools for `gregale app exec --debug`.
COPY --from=gregale-debug-tools /out/ /usr/lib/gregale/debug/
