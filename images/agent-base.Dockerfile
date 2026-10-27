# agent-base — the shared, read-only drive0 for agent runs (ADR-20261027090010 §2).
# Content-addressed, staged once per agent node. A run adds only its input
# drive and writable scratch; a seller harness adds its own drive1 layer.
#
# All three presets live here: Codex CLI, OpenCode and Claude Code. A preset
# is a binary in this image plus its command in pkg/agentpresets/presets.json,
# not an image of its own. The binaries are large (Codex about 430 MB,
# Claude Code about 280 MB, OpenCode about 190 MB unpacked), so they are
# counted once in base residency rather than once per run.
#
# The versions are pinned three times, and pkg/agentpresets tests that they
# agree: images/agent-base/package.json, its integrity-pinned lockfile, and
# the catalog. Bump all three together, then regenerate the lockfile with
# `npm install --package-lock-only --ignore-scripts` in images/agent-base.
#
# Claude Code is Anthropic's published npm package and binary, unmodified.
# No managed settings pin providers or disable sign-in methods (ADR-20261027090010 §2
# and §7 set out the hosting terms).
#
# Node 22 on Alpine (musl), the same digest as runner-node22: every harness
# ships a musl build, and the memory experiment measured this base.
FROM mirror.gcr.io/library/node:22-alpine@sha256:76789712cd1ae89a1225eac9077010d68987a423588042dac30446f502f1858c
# Issue #197 B3.6: mutable tag pinned via images/Dockerfile.lock.

COPY --chmod=0644 images/agent-base/package.json images/agent-base/package-lock.json /opt/gregale/agents/
COPY --chmod=0644 pkg/agentpresets/presets.json /usr/share/faas/agent-presets.json
COPY --chmod=0755 images/agent-base/finish-install.sh /tmp/finish-install.sh

# git: checkouts and the harnesses' git tools. python3: common agent tooling.
# ripgrep: Claude Code's search on musl (USE_BUILTIN_RIPGREP=0).
# libstdc++, libgcc: the musl harness builds. bash: harness shell tools.
# npm stays, unlike in the runner bases: agents install and test projects.
# Both install scripts that run here (opencode-ai, claude-code) only link
# their locked platform binary into place; finish-install.sh checks that.
RUN --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt \
    apk upgrade --no-cache && \
    apk add --no-cache bash git python3 ripgrep libstdc++ libgcc && \
    chmod 0755 /opt/gregale /opt/gregale/agents && \
    cd /opt/gregale/agents && \
    NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt npm ci --omit=dev --no-audit --no-fund && \
    /tmp/finish-install.sh && \
    npm cache clean --force && rm -rf /root/.npm /tmp/* && \
    if id app >/dev/null 2>&1; then :; \
    elif id node >/dev/null 2>&1; then sed -i 's/^node:/app:/' /etc/passwd; \
    else adduser -D -u 1000 app; fi

# The run environment comes from the preset catalog's env (vmmd sets it per
# run); these copies only serve containers started from the OCI image, such
# as the image contract check.
ENV DISABLE_AUTOUPDATER=1 \
    USE_BUILTIN_RIPGREP=0

# One-shot run layout (ADR-20261027090010 §3): guest-init unpacks the read-only input
# drive to /input; the harness works in /work and writes results to
# /work/output.
RUN mkdir -p /input /work/output && chown -R 1000:1000 /work
WORKDIR /work
