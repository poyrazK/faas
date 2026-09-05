#!/usr/bin/env bash
# scripts/compile-daemons.sh — build every Go daemon + support executable + CLI into the image at
# /opt/faas/current/bin/. The image bakes a `current` symlink that the
# per-daemon systemd units consume; `gregalectl release install --git-sha X`
# flips current→X at install time.
#
# Per ADR-111: 8 daemons + gregale CLI + gregalectl CLI (operator-only).
# Build flags match the canonical `-trimpath -ldflags='-s -w'` posture so
# binaries are reproducible + small.
#
# Per ADR-092 + ADR-112: per-role subset is enforced OUTSIDE this script
# (the per-daemon 99-faas-role.conf drop-in decides which daemons start;
# this script bakes ALL 8 daemons regardless of role). ADR-112 collapsed
# role out of the image entirely — there is no role-overlay.pkr.hcl —
# so first-boot `gregalectl release install --role` writes the drop-ins.
# (Trade-off: larger image by ~30 MB; we accept that for the simpler
# build pipeline + the per-role PKI subset still constrains runtime.)
set -euo pipefail

SRC_ROOT="${SRC_ROOT:-/tmp/src}"
GO_VERSION="${GO_VERSION:-1.25.13}"

DAEMONS=(apid gatewayd-public gatewayd-internal schedd vmmd builderd imaged meterd githubd)
CLIS=(gregale gregalectl)
TOOLS=(vmmd-jail-helper vmmd-raw-bridge vmmd-stream-bridge)

if [[ ! -d "${SRC_ROOT}" ]]; then
    echo "compile-daemons: SRC_ROOT=${SRC_ROOT} not present; expected the repo mounted at this path" >&2
    exit 1
fi

# Go toolchain on PATH.
export PATH="/usr/local/go/bin:${PATH}"
go version | grep -q "go${GO_VERSION}" || { echo "compile-daemons: Go version mismatch" >&2; exit 1; }

cd "${SRC_ROOT}"

mkdir -p /opt/faas/current/bin

VERSION="$(git -C "${SRC_ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)"
LDFLAGS="-X github.com/onebox-faas/faas/pkg/wire.Version=${VERSION} -s -w"

for d in "${DAEMONS[@]}"; do
    echo "compile-daemons: building ${d}"
    go build -trimpath -ldflags "${LDFLAGS}" \
        -o "/opt/faas/current/bin/${d}" \
        "./cmd/${d}"
done

for c in "${CLIS[@]}"; do
    echo "compile-daemons: building CLI ${c}"
    go build -trimpath -ldflags "${LDFLAGS}" \
        -o "/opt/faas/current/bin/${c}" \
        "./cmd/${c}"
done

for t in "${TOOLS[@]}"; do
    echo "compile-daemons: building ${t}"
    go build -trimpath -ldflags "${LDFLAGS}" \
        -o "/opt/faas/current/bin/${t}" \
        "./cmd/${t}"
done

echo "compile-daemons: $(ls /opt/faas/current/bin/ | wc -l) binaries installed"
