# runner-python313 — base rootfs (drive0) for Python 3.13 function
# deploys (spec §4.6, §4.9). Pairs with guest/runners/python313/main.go.
# Content-addressed, staged to /srv/fc/base/runner-python313.ext4.
#
# Tier 1 PR 2 (ADR-052): this base is auto-staged by imaged through
# pkg/imaged/base_stage.go::EnsureRuntimeBase. Production nodes receive
# a digest-pinned ref from the deployment pipeline; no manual ext4 copy is
# part of the supported workflow.
#
# Wolfi keeps Python 3.13 as a versioned package while its minimal glibc base
# avoids the unfixed CRITICAL findings in the Debian 12 and 13 Python images.
# The package name pins the interpreter minor; patch releases remain eligible
# for security updates. The image stays standalone in DefaultRuntimeBaseRefs
# because its OCI layer chain is not derived from base-debian-parent.
#
# The two-drive scheme still amortizes this base across every python313 app on
# the box — per-app cost is just the customer's site-packages + handler.
FROM cgr.dev/chainguard/wolfi-base:latest@sha256:918a593b8268c222afd4e2c4f06860ac984e60719b4697e4c71d796bc8fcd042
# Issue #197 B3.6 (extension): mutable tag pinned via images/Dockerfile.lock.
RUN apk add --no-cache bash ca-certificates python-3.13 && \
    mkdir -p /usr/local/bin && \
    ln -sf /usr/bin/python3.13 /usr/local/bin/python3 && \
    ln -sf /usr/bin/python3.13 /usr/local/bin/python

# Guest runtime user (uid 1000, spec §4.8). Keep the static identity in the
# shared skeleton instead of retaining an account-management package.
COPY images/rootfs-skel/ /
# Wolfi's mode-000 placeholder has no login use inside the microVM and blocks
# unprivileged ext4 assembly from copying the complete runtime tree.
RUN rm -f /etc/shadow
WORKDIR /app
