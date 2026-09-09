#!/usr/bin/env bash
# Read-only release gate for the customer object-storage surface. This checks
# the deployed API contract and operator configuration before running the
# mutating s3-gateway-smoke.sh qualification.
set -euo pipefail
umask 077

: "${FAAS_TOKEN:?set FAAS_TOKEN to a storage:manage or admin bearer token}"
: "${GREGALE_APP_SLUG:?set GREGALE_APP_SLUG to a disposable test app}"

GREGALE_API_URL="${GREGALE_API_URL:-https://api.gregale.dev}"
probe_bucket_id="00000000-0000-0000-0000-000000000000"

for tool in curl jq; do
  command -v "$tool" >/dev/null || {
    echo "missing required command: $tool" >&2
    exit 2
  }
done

preflight_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gregale-object-storage-preflight.XXXXXX")"
cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM
  rm -rf -- "$preflight_tmp"
  exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

api_get() {
  local path="$1"
  local body="$preflight_tmp/body"
  local headers="$preflight_tmp/headers"
  local status

  status="$(curl --silent --show-error --connect-timeout 10 --max-time 30 \
    -D "$headers" -o "$body" -w '%{http_code}' \
    -H "Authorization: Bearer ${FAAS_TOKEN}" \
    "$GREGALE_API_URL$path")"
  PREFLIGHT_STATUS="$status"
  PREFLIGHT_BODY="$(<"$body")"
  PREFLIGHT_CONTENT_TYPE="$(awk -F': *' 'tolower($1) == "content-type" {print tolower($2); exit}' "$headers" | tr -d '\r')"
}

fail_with_body() {
  echo "object-storage release preflight failed: $1" >&2
  if [[ -n "${PREFLIGHT_BODY:-}" ]]; then
    printf '%s\n' "$PREFLIGHT_BODY" | jq -c . >&2 2>/dev/null || printf '%s\n' "$PREFLIGHT_BODY" >&2
  fi
  exit 1
}

api_get "/v1/apps/$GREGALE_APP_SLUG/buckets"
[[ "$PREFLIGHT_STATUS" == 200 ]] || fail_with_body "bucket capability endpoint returned HTTP $PREFLIGHT_STATUS"

jq -e '
  (.enabled == true) and
  (.regions | length > 0) and
  (.default_region | type == "string" and length > 0) and
  (.max_upload_bytes | type == "number" and . > 0) and
  (.max_buckets_per_app | type == "number" and . > 0)
' <<<"$PREFLIGHT_BODY" >/dev/null || fail_with_body "object storage is disabled or incompletely configured"

regions="$(jq -r '.regions | join(",")' <<<"$PREFLIGHT_BODY")"
default_region="$(jq -r '.default_region' <<<"$PREFLIGHT_BODY")"

# A random/nonexistent bucket must be handled by apid's JSON problem surface.
# A plain web-server 404 means the compute-binding route is absent from the
# deployed binary, which is the common failure mode after merging but before
# the release reaches the Frankfurt control plane.
api_get "/v1/apps/$GREGALE_APP_SLUG/buckets/$probe_bucket_id/compute-bindings"
if [[ "$PREFLIGHT_STATUS" == 404 && "$PREFLIGHT_CONTENT_TYPE" == application/problem+json* ]]; then
  : # Route is present; the fake bucket is expected to be unavailable.
elif [[ "$PREFLIGHT_STATUS" == 404 && "$PREFLIGHT_BODY" == *"404 page not found"* ]]; then
  fail_with_body "compute-binding routes are not present in the deployed release"
elif [[ "$PREFLIGHT_STATUS" -ge 400 ]]; then
  fail_with_body "compute-binding route probe returned HTTP $PREFLIGHT_STATUS"
fi

printf 'object-storage release preflight passed: app=%s default_region=%s regions=%s\n' \
  "$GREGALE_APP_SLUG" "$default_region" "$regions"
