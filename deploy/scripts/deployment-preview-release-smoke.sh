#!/usr/bin/env bash
set -euo pipefail

: "${GREGALE_API_KEY:?set GREGALE_API_KEY to a release-smoke account key}"
: "${GREGALE_DEPLOYMENT_ID:?set GREGALE_DEPLOYMENT_ID to a live deployment}"
: "${GREGALE_PREVIEW_EXPECT:?set GREGALE_PREVIEW_EXPECT to content unique to that deployment}"

GREGALE_API_URL="${GREGALE_API_URL:-https://api.gregale.dev}"
GREGALE_PREVIEW_PATH="${GREGALE_PREVIEW_PATH:-/}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-preview-smoke.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

curl --fail --silent --show-error \
  --header "Authorization: Bearer ${GREGALE_API_KEY}" \
  "${GREGALE_API_URL}/v1/deployments/${GREGALE_DEPLOYMENT_ID}/url" \
  >"${tmp_dir}/preview.json"

preview_url="$(jq -er 'select(.alive == true) | .url | select(startswith("https://"))' "${tmp_dir}/preview.json")"
preview_host="$(jq -er '.host' "${tmp_dir}/preview.json")"
[[ "$preview_url" == "https://${preview_host}" ]] || {
  echo "preview smoke: API host/url mismatch" >&2
  exit 1
}
[[ "$preview_host" =~ ^deploy-[1-9][0-9]*-[a-z0-9][a-z0-9-]*[a-z0-9]\.gregale\.dev$ ]] || {
  echo "preview smoke: API emitted a host outside the one-label wildcard contract: ${preview_host}" >&2
  exit 1
}

curl --fail --silent --show-error --location \
  --proto '=https' --tlsv1.2 --max-time 30 \
  "${preview_url}${GREGALE_PREVIEW_PATH}" >"${tmp_dir}/body"
grep -Fq -- "$GREGALE_PREVIEW_EXPECT" "${tmp_dir}/body" || {
  echo "preview smoke: response did not come from the expected deployment" >&2
  exit 1
}

printf 'deployment preview smoke passed: %s\n' "$preview_url"
