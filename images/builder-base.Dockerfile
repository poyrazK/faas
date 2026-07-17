# builder-base — rootfs for the ephemeral builder microVM (spec §4.5, ADR-003).
# Contains BuildKit (rootless — inside a VM it may as well be root), Railpack, git,
# and the OCI exporter. Builds run INSIDE this VM so untrusted `npm install` gets
# VM-grade isolation; the 2 GB RAM cap is the VM boundary. Never run builds in a
# host container.

# Railpack is the canonical Node/Python build engine per spec §4.5. We pin a
# version via ARG so dev-time builds are reproducible; CI overrides it to the
# value locked in tools/release/railpack-version.json (kept out of this file so
# platform-specific bumps don't churn the spec-side layer).
ARG RAILPACK_VERSION=0.5.0

FROM debian:12-slim
ARG RAILPACK_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates buildkit uidmap curl && \
    rm -rf /var/lib/apt/lists/*
# Railpack pinned; AMD64 binary (EX44 target). arm64 Lima builders use the
# multi-arch release tarball — that's left for the platform shim.
ADD https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/railpack-v${RAILPACK_VERSION}-linux-amd64 /usr/local/bin/railpack
RUN chmod +x /usr/local/bin/railpack && \
    /usr/local/bin/railpack --version
# guest-init is built by CI and stamped in at image-build time — the kernel
# cmdline hands PID1 to this binary regardless of app vs build mode (mode is
# decided by which manifest file exists at /etc/faas/).
COPY guest-init /usr/local/bin/faas-guest-init
RUN chmod +x /usr/local/bin/faas-guest-init
WORKDIR /build
