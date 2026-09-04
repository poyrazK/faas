# runner-python313 — base rootfs (drive0) for Python 3.13 function
# deploys (spec §4.6, §4.9). Pairs with guest/runners/python313/main.go.
# Content-addressed, staged to /srv/fc/base/runner-python313.ext4.
#
# Tier 1 PR 2 (ADR-052): this base is auto-staged by imaged through
# pkg/imaged/base_stage.go::EnsureRuntimeBase. Production nodes receive
# a digest-pinned ref from the deployment pipeline; no manual ext4 copy is
# part of the supported workflow.
#
# The two-drive scheme amortizes this base across every python313 app
# on the box — per-app cost is just the customer's site-packages +
# handler. The 130 MB/sandbox accounting is preserved
# (CLAUDE.md "load-bearing — DO NOT fix").
FROM python:3.13-slim-bookworm@sha256:2f2e5a876c71a6757f55ec57f2add0225ddaf01c802a33fcc29073943f94d907
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get upgrade -y --no-install-recommends && \
    rm -rf /var/lib/apt/lists/* && \
    (id app 2>/dev/null || useradd -u 1000 -m app)
WORKDIR /app
