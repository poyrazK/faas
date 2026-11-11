#!/usr/bin/env bash
# install-grype.sh — install the exact Grype binary used by image CI.
#
# The caller supplies GRYPE_VERSION and GRYPE_SHA256. Keeping this in one
# script prevents the builder and runtime matrix from silently drifting to
# different scanners or unverified downloads.

set -euo pipefail

destination=${1:?destination path is required}
version=${GRYPE_VERSION:?GRYPE_VERSION is required}
expected_sha=${GRYPE_SHA256:?GRYPE_SHA256 is required}

archive_dir=${RUNNER_TEMP:-$(mktemp -d)}
archive="${archive_dir}/grype_${version}_linux_amd64.tar.gz"
extract_dir="${archive_dir}/grype-extract-${version}"

mkdir -p "$(dirname "${destination}")" "${extract_dir}"
# Pin checked; falls back to the GitHub API (needs GH_TOKEN) when github.com's
# release download fails (2026-11-08: 504s failed every image build).
bash "$(dirname "${BASH_SOURCE[0]}")/fetch-release-asset.sh" anchore/grype "v${version}" \
  "grype_${version}_linux_amd64.tar.gz" "${expected_sha}" "${archive}"

tar -xzf "${archive}" -C "${extract_dir}" grype
install -m 0755 "${extract_dir}/grype" "${destination}"
echo "Installed Grype ${version} at ${destination}"
