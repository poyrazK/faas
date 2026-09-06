#!/usr/bin/env bash
# scan-oci-image.sh — scan one concrete OCI image artifact.
#
# The source prefix is deliberately explicit: `docker` scans a locally loaded
# image and `registry` scans an image served by a registry. A tag is accepted
# only because the CI callers use immutable PR/SHA tags; production release
# manifests still carry the resolved digest. The first pass prints the full
# report, while the second pass enforces the caller's publication policy.
# Actionable fixed matches remain the default. Hardened runtime callers can set
# GRYPE_ONLY_FIXED=false to match vmmd's fail-closed CRITICAL admission policy.

set -euo pipefail

source_kind=${1:?source kind is required (docker or registry)}
image_ref=${2:?image reference is required}
platform=${3:-linux/amd64}
fail_on=${GRYPE_FAIL_ON:-critical}
grype_bin=${GRYPE_BIN:-grype}
only_fixed=${GRYPE_ONLY_FIXED:-true}

case "${only_fixed}" in
  true) gate_fix_args=(--only-fixed) ;;
  false) gate_fix_args=(--only-fixed=false) ;;
  *)
    echo "GRYPE_ONLY_FIXED must be true or false, got ${only_fixed}" >&2
    exit 2
    ;;
esac

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
  echo "Full report contains findings; enforcing the configured publication gate." >&2
fi

"${grype_bin}" "${source_kind}:${image_ref}" \
  --platform "${platform}" \
  --fail-on "${fail_on}" \
  "${gate_fix_args[@]}" \
  -o table
