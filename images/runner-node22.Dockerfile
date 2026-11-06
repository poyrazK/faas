# runner-node22 — base rootfs (drive0) for Node 22 apps and functions (spec §4.6,
# §4.9). The base is content-addressed and staged once as drive0; app images
# still contribute only their dependency/code delta as drive1.
# Content-addressed, staged to /srv/fc/base/runner-node22.ext4.
#
# Debian trixie (glibc), like runner-node24 (ADR-20261102133056). It used to
# be node:22-alpine (musl), which kept Node 22 source apps off the shared
# runtime: a Railpack build compiles native addons against glibc, and those
# cannot load on a musl base (ADR-20261029103512).
FROM mirror.gcr.io/library/node:22-trixie-slim@sha256:b0ea3309e53992217c31c10aea3ef3ce47c3fffba7416a1e4a5a60b60373b1ed
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
# `make images-lock-update` is the only way to bump the digest.
# The official image already reserves uid 1000 for `node`; reuse that
# identity under the platform's canonical `app` name (spec §4.8) instead of
# attempting a duplicate uid.
COPY --chmod=0644 guest/profiling/node/package*.json /opt/gregale/profiling/
COPY --chmod=0644 guest/tracing/node/package*.json /opt/gregale/tracing/
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt chmod 0755 /opt/gregale /opt/gregale/profiling /opt/gregale/tracing && \
    apt-get update && apt-get install -y --no-install-recommends python3 make g++ && \
    cd /opt/gregale/profiling && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev && \
    cd /opt/gregale/tracing && NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev --ignore-scripts && \
    apt-get purge -y --auto-remove python3 make g++ && rm -rf /root/.npm /var/lib/apt/lists/*
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else useradd -u 1000 -m app; fi
# The function runner shim (guest/runners/node22) is layered in for `type:
# function` deploys; plain Node apps bring their own entrypoint.
COPY --chmod=0644 guest/profiling/node.cjs /opt/gregale/profiling/node.cjs
COPY --chmod=0644 guest/tracing/node.cjs /opt/gregale/tracing/node.cjs
WORKDIR /app
