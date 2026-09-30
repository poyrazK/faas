#!/usr/bin/env bash
# Exercise deployed native split-box fixtures through their public TLS edge.
# This gate owns sessions only; it never stops tenant daemons or changes flags.
set -Eeuo pipefail

die() { echo "native Dev Bridge acceptance: $*" >&2; exit 1; }
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || die "requires native x86_64 Linux"
[[ -f /etc/faas/dev-bridge-acceptance-host ]] || die "host is not designated for Dev Bridge acceptance"
[[ -r /dev/kvm && -w /dev/kvm ]] || die "requires accessible /dev/kvm on the native acceptance runner"
: "${FAAS_API:?set the dedicated deployment public HTTPS API URL}"
: "${FAAS_TOKEN:?set the acceptance account token through the secure environment}"
: "${FAAS_BRIDGE_PROJECT:?set the isolated fixture project slug}"
: "${FAAS_BRIDGE_PAYMENTS:?set the payments fixture app slug}"
: "${FAAS_BRIDGE_FRONTEND:?set the frontend fixture app slug}"
: "${FAAS_BRIDGE_INVENTORY:?set the inventory fixture app slug}"
command -v go >/dev/null || die "Go is missing"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
export FAAS_DEV_BRIDGE_ACCEPTANCE=native-x86
export FAAS_BRIDGE_EVIDENCE="${FAAS_BRIDGE_EVIDENCE:-dev-bridge-native-evidence.json}"
FAAS_BRIDGE_SOURCE_COMMIT="$(git rev-parse HEAD)"
export FAAS_BRIDGE_SOURCE_COMMIT
go test ./pkg/devbridgeacceptance -run '^TestNativeDevBridgeAcceptance$' -count=1 -timeout=65m -v
