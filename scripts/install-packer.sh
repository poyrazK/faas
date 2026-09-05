#!/usr/bin/env bash
# scripts/install-packer.sh — install the pinned Packer version (ADR-111).
#
# ADR-111 design: the image build pipeline depends on a Packer binary whose
# version is content-addressed by the tag. apt's packer is too new (1.11+) and
# plugin compat isn't guaranteed; we curl + sha256 + tar from the official
# release page instead (same pattern as deploy/lima/* + scripts/install-*).
#
# Usage: scripts/install-packer.sh [VERSION]
#   Default VERSION is read from deploy/packer/Makefile:PACKER_VERSION.
#
# Installs to /usr/local/bin/packer (sudo if not root).
set -euo pipefail

VERSION="${1:-1.10.0}"
# SHA-256 of packer_1.10.0_linux_amd64.zip from
# https://releases.hashicorp.com/packer/1.10.0/packer_1.10.0_SHA256SUMS.
# Mirror the supply-chain pin pattern used by vacuum / sqlc / protoc /
# promtool in .github/workflows/ci.yml.
SHA="a8442e7041db0a7db48f468e353ee07fa6a7b35276ec62f60813c518ca3296c1"
URL="https://releases.hashicorp.com/packer/${VERSION}/packer_${VERSION}_linux_amd64.zip"

if command -v packer >/dev/null 2>&1; then
    HAVE_VERSION="$(packer --version)"
    if [[ "${HAVE_VERSION}" == "${VERSION}" ]]; then
        echo "packer ${VERSION} already installed — skipping"
        exit 0
    fi
    echo "packer ${HAVE_VERSION} present, want ${VERSION} — replacing"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

echo "Downloading packer ${VERSION}…"
# Hosted runners occasionally reset the TLS connection to releases.hashicorp.com
# before any response bytes arrive. Retry those transport errors as well as the
# usual transient HTTP failures; checksum verification still gates installation.
curl --fail --silent --show-error --location \
    --retry 5 --retry-all-errors --retry-delay 2 --retry-max-time 120 \
    -o "${TMP}/packer.zip" "${URL}"

echo "${SHA}  ${TMP}/packer.zip" | sha256sum --check --strict

echo "Unpacking…"
(cd "${TMP}" && unzip -q packer.zip)

INSTALL_DIR=/usr/local/bin
if [[ ! -w "${INSTALL_DIR}" ]]; then
    SUDO=sudo
fi
${SUDO:-} mv "${TMP}/packer" "${INSTALL_DIR}/packer"
${SUDO:-} chmod 0755 "${INSTALL_DIR}/packer"

packer --version
