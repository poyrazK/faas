#!/usr/bin/env bash
# scripts/bake-fc.sh — install the Firecracker binary + jailer into the
# image at /usr/local/bin/, with SHA-256 pins.
#
# Per ADR-005 + ADR-111: image bakes ONE FC version, pinned. Rolling
# forward = new image tag + lazy re-snapshot, NEVER in-place FC upgrade.
#
# Pinned FC release + sha must mirror deploy/ansible/roles/firecracker/
# defaults/main.yml:fc_release + fc_release_sha256. Today: v1.17.0 +
# 06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558.
set -euo pipefail

FC_RELEASE="${FC_RELEASE:-v1.17.0}"
FC_RELEASE_SHA256="${FC_RELEASE_SHA256:-06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558}"
FC_ARCH="${FC_ARCH:-x86_64}"
FC_URL="https://github.com/firecracker-microvm/firecracker/releases/download/${FC_RELEASE}/firecracker-${FC_RELEASE}-${FC_ARCH}.tgz"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

echo "bake-fc: downloading ${FC_RELEASE}…"
curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "${TMP}/fc.tgz" "${FC_URL}"
echo "${FC_RELEASE_SHA256}  ${TMP}/fc.tgz" | sha256sum --check --strict
(cd "${TMP}" && tar -xzf fc.tgz)

# Install FC + jailer. The versioned name preserves the pinned binary;
# /usr/local/bin/firecracker and /usr/local/bin/jailer are symlinks that
# the systemd unit consumes.
install -m 0755 "${TMP}/release-${FC_RELEASE}-${FC_ARCH}/firecracker-${FC_RELEASE}-${FC_ARCH}" \
    "/usr/local/bin/firecracker-${FC_RELEASE}"
install -m 0755 "${TMP}/release-${FC_RELEASE}-${FC_ARCH}/jailer-${FC_RELEASE}-${FC_ARCH}" \
    "/usr/local/bin/jailer-${FC_RELEASE}"

ln -sf "firecracker-${FC_RELEASE}" /usr/local/bin/firecracker
ln -sf "jailer-${FC_RELEASE}"      /usr/local/bin/jailer

# SHA-256 pin (used by `gregalectl doctor` to assert runtime = bake).
sha256sum "/usr/local/bin/firecracker-${FC_RELEASE}" \
    | awk '{print $1}' > "/usr/local/bin/firecracker-${FC_RELEASE}.sha256"

# /srv/fc/{base,jail,snap,layers,builder,base-staging,sigs,scans}
# skeleton — created here so the per-daemon systemd units can write into
# them at first-boot without permission errors.
mkdir -p /srv/fc/{base,jail,snap,layers,builder,base-staging,sigs,scans}
chown -R root:faas /srv/fc
chmod -R 0750        /srv/fc

echo "bake-fc: installed $(firecracker --version)"
