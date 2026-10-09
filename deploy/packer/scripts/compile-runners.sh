#!/usr/bin/env bash
# scripts/compile-runners.sh — build every function-runner shim for the
# guest into /opt/faas/current/bin/runners/<runtime>/faas-runner.
#
# Per CLAUDE.md: function-runner shims are static Go binaries that the
# guest-init invokes based on FAAS_FUNCTION_RUNNER_<RUNTIME_UPPER> (set by
# imaged at deploy time). Each shim is tiny (<1 MB) and statically linked
# (linux/amd64, CGO off). The image bakes ALL six so any per-app runtime
# can be stitched without rebuilding the image.
set -euo pipefail

SRC_ROOT="${SRC_ROOT:-/tmp/src}"
GO_VERSION="${GO_VERSION:-1.26.9}"

# guest/init's build matrix — see guest/runners/{node22,python312,…}/main.go
RUNNERS=(node22 python312 go124 go124-alpine node24 python313)

if [[ ! -d "${SRC_ROOT}" ]]; then
    echo "compile-runners: SRC_ROOT=${SRC_ROOT} not present" >&2
    exit 1
fi

export PATH="/usr/local/go/bin:${PATH}"

cd "${SRC_ROOT}"

mkdir -p /opt/faas/current/bin/runners

for rt in "${RUNNERS[@]}"; do
    echo "compile-runners: ${rt}"
    mkdir -p "/opt/faas/current/bin/runners/${rt}"
    source_rt="${rt}"
    if [[ "${rt}" == "go124-alpine" ]]; then source_rt="go124"; fi
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        go build -trimpath -ldflags='-s -w' \
        -o "/opt/faas/current/bin/runners/${rt}/faas-runner" \
        "./guest/runners/${source_rt}"
done

echo "compile-runners: $(ls /opt/faas/current/bin/runners/ | wc -l) runners installed"
