# builder-base — rootfs for the ephemeral builder microVM (spec §4.5, ADR-003).
# Contains BuildKit (rootless — inside a VM it may as well be root), Railpack, git,
# and the OCI exporter. Builds run INSIDE this VM so untrusted `npm install` gets
# VM-grade isolation; the 2 GB RAM cap is the VM boundary. Never run builds in a
# host container.
FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates buildkit && \
    rm -rf /var/lib/apt/lists/*
# Railpack + the OCI exporter are fetched pinned in CI; see images/README.md.
WORKDIR /build
