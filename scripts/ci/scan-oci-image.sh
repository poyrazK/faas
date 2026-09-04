#!/usr/bin/env bash
# scan-oci-image.sh — scan one concrete OCI image artifact.
#
# The source prefix is deliberately explicit: `docker` scans a locally loaded
# image and `registry` scans an image served by a registry. A tag is accepted
# only because the CI callers use immutable PR/SHA tags; production release
# manifests still carry the resolved digest. The first pass prints the full
# report, while the second pass makes only actionable fixed matches blocking.
# This keeps vendor-unfixed and unknown findings visible without making every
# Debian stable base permanently unpublishable.

set -euo pipefail

source_kind=${1:?source kind is required (docker or registry)}
image_ref=${2:?image reference is required}
platform=${3:-linux/amd64}
fail_on=${GRYPE_FAIL_ON:-critical}
grype_bin=${GRYPE_BIN:-grype}

case "${source_kind}" in
  docker|registry) ;;
  *)
    echo "source kind must be docker or registry, got ${source_kind}" >&2
    exit 2
    ;;
esac

set +e
"${grype_bin}" "${source_kind}:${image_ref}" \
  --platform "${platform}" \
  --fail-on "${fail_on}" \
  --only-fixed=false \
  -o table
full_status=$?
set -e
if [[ "${full_status}" -ne 0 && "${full_status}" -ne 2 ]]; then
  echo "Grype full report failed with status ${full_status}" >&2
  exit "${full_status}"
fi

if [[ "${full_status}" -eq 2 ]]; then
  echo "Full report contains findings; enforcing the fixed-finding gate." >&2
fi

"${grype_bin}" "${source_kind}:${image_ref}" \
  --platform "${platform}" \
  --fail-on "${fail_on}" \
  --only-fixed \
  -o table
