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
FROM node:24-bookworm-slim@sha256:6642ef280aebc09c4541bee0b15c9f89f0f3f3c247ddee79ae1d37eddfdcbbaa
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
# The official image already reserves uid 1000 for `node`; reuse that
# identity under the platform's canonical `app` name instead of attempting
# a duplicate uid.
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack && \
    rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else useradd -u 1000 -m app; fi
WORKDIR /app
