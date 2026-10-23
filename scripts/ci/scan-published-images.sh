#!/usr/bin/env bash
# Rescan the published platform images for the nightly CVE monitor (ADR-131).
#
# images.yml gates each image only when it is rebuilt, so an advisory
# published after the build stays invisible until the next rebuild. imaged
# stages the `:latest` tag of every image below (pkg/imaged/base.go), so that
# tag is what new deploys run on. Each report lands in OUTPUT_DIR/<image>.json
# for normalize-cve-results.sh. A pull or scanner error fails the run; an
# image that cannot be scanned is unknown exposure, not a clean result.

set -euo pipefail

output_dir=${1:?usage: scan-published-images.sh OUTPUT_DIR}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
grype_bin=${GRYPE_BIN:-grype}
registry=${CVE_MONITOR_IMAGE_REGISTRY:-ghcr.io/poyrazk}
platform=${CVE_MONITOR_IMAGE_PLATFORM:-linux/amd64}

# Keep in sync with the runtime matrix in .github/workflows/images.yml plus
# builder-base; scripts/ci/cve-monitor_test.sh fails when they drift.
images=(
  builder-base
  base-debian-parent
  base-minimal
  runner-node22
  runner-node24
  runner-python312
  runner-python313
  execution-python-data-v1
  runner-go124
  runner-go124-alpine
)

mkdir -p "${output_dir}"
for image in "${images[@]}"; do
  config_args=()
  # The staging-only Debian parent carries version-pinned publication
  # exceptions (ADR-053); every other image is reported unfiltered.
  if [[ ${image} == base-debian-parent ]]; then
    config_args=(-c "${repo_root}/images/base-debian-parent.grype.yaml")
  fi
  echo "scanning ${registry}/${image}:latest (${platform})"
  "${grype_bin}" "registry:${registry}/${image}:latest" \
    --platform "${platform}" \
    ${config_args[@]+"${config_args[@]}"} \
    -o json > "${output_dir}/${image}.json"
done
