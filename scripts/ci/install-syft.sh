#!/usr/bin/env bash
# install-syft.sh — install the exact Syft binary that writes the SBOMs CI
# attests (issue #295).
#
# The caller supplies SYFT_VERSION and SYFT_SHA256 (the same pin release.yml's
# daemon bundle SBOM uses), so image and CLI SBOMs never drift to a different
# or unverified generator.

set -euo pipefail

destination=${1:?destination path is required}
version=${SYFT_VERSION:?SYFT_VERSION is required}
expected_sha=${SYFT_SHA256:?SYFT_SHA256 is required}

archive_dir=${RUNNER_TEMP:-$(mktemp -d)}
archive="${archive_dir}/syft_${version}_linux_amd64.tar.gz"
extract_dir="${archive_dir}/syft-extract-${version}"

mkdir -p "$(dirname "${destination}")" "${extract_dir}"
curl --fail --silent --show-error --location \
  --retry 3 --retry-all-errors --retry-delay 2 \
  -o "${archive}" \
  "https://github.com/anchore/syft/releases/download/v${version}/syft_${version}_linux_amd64.tar.gz"

if command -v sha256sum >/dev/null 2>&1; then
  printf '%s  %s\n' "${expected_sha}" "${archive}" | sha256sum --check --strict
else
  printf '%s  %s\n' "${expected_sha}" "${archive}" | shasum -a 256 -c -
fi

tar -xzf "${archive}" -C "${extract_dir}" syft
install -m 0755 "${extract_dir}/syft" "${destination}"
echo "Installed Syft ${version} at ${destination}"
