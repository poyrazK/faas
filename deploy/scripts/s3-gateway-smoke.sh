#!/usr/bin/env bash
# End-to-end smoke for the customer-facing Gregale S3 contract. The script
# creates a temporary logical bucket and credential through apid, exercises
# the branded endpoint with the AWS CLI, then revokes the credential and
# deletes the object and bucket on every exit path.
set -euo pipefail
umask 077

: "${FAAS_TOKEN:?set FAAS_TOKEN to a storage:manage or admin bearer token}"
: "${GREGALE_APP_SLUG:?set GREGALE_APP_SLUG to the disposable test app}"

GREGALE_API_URL="${GREGALE_API_URL:-https://api.gregale.dev}"
GREGALE_S3_ENDPOINT="${GREGALE_S3_ENDPOINT:-https://s3.gregale.dev}"
GREGALE_S3_REGION="${GREGALE_S3_REGION:-us-east-1}"

for tool in aws curl jq; do
  command -v "$tool" >/dev/null || {
    echo "missing required command: $tool" >&2
    exit 2
  }
done

smoke_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gregale-s3-smoke.XXXXXX")"
bucket_name="smoke-$(date -u +%Y%m%d%H%M%S)-$(printf '%04x' "$RANDOM")"
object_key="probe/hello.txt"
bucket_id=""
credential_id=""
object_uploaded=false
credential_revoked=false
bucket_deleted=false

api() {
  curl --fail-with-body --silent --show-error \
    -H "Authorization: Bearer ${FAAS_TOKEN}" \
    -H "Content-Type: application/json" \
    "$@"
}

cleanup() {
  original_status=$?
  trap - EXIT INT TERM
  set +e
  cleanup_status=0

  if [[ "$object_uploaded" == true && -n "${AWS_ACCESS_KEY_ID:-}" ]]; then
    aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
      s3api delete-object --bucket "$bucket_name" --key "$object_key" \
      >/dev/null 2>&1 || cleanup_status=1
  fi
  if [[ -n "$credential_id" && "$credential_revoked" != true ]]; then
    api -X DELETE \
      "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/s3-credentials/$credential_id" \
      >/dev/null 2>&1 || cleanup_status=1
  fi
  if [[ -n "$bucket_id" && "$bucket_deleted" != true ]]; then
    api -X DELETE \
      "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id" \
      >/dev/null 2>&1 || cleanup_status=1
  fi
  rm -rf -- "$smoke_tmp"

  if (( cleanup_status != 0 )); then
    echo "cleanup failed; inspect temporary bucket ${bucket_name}" >&2
    if (( original_status == 0 )); then
      original_status=1
    fi
  fi
  exit "$original_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'gregale branded S3 smoke\n' >"$smoke_tmp/payload"
cat >"$smoke_tmp/aws-config" <<'CONFIG'
[default]
region = us-east-1
s3 =
    addressing_style = path
CONFIG
export AWS_CONFIG_FILE="$smoke_tmp/aws-config"
export AWS_EC2_METADATA_DISABLED=true
export AWS_PAGER=""

api -X POST \
  --data "$(jq -cn --arg name "$bucket_name" '{name:$name,scope:"default"}')" \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets" \
  >"$smoke_tmp/bucket.json"
bucket_id="$(jq -er '.id' "$smoke_tmp/bucket.json")"
jq -e '.state == "ready"' "$smoke_tmp/bucket.json" >/dev/null

api -X POST \
  --data '{"label":"deployment-smoke","permission":"read_write"}' \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/s3-credentials" \
  >"$smoke_tmp/credential.json"
credential_id="$(jq -er '.id' "$smoke_tmp/credential.json")"
AWS_ACCESS_KEY_ID="$(jq -er '.access_key_id' "$smoke_tmp/credential.json")"
AWS_SECRET_ACCESS_KEY="$(jq -er '.secret_access_key' "$smoke_tmp/credential.json")"
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
jq -e --arg endpoint "$GREGALE_S3_ENDPOINT" --arg region "$GREGALE_S3_REGION" \
  '.endpoint == $endpoint and .region == $region and .addressing_style == "path"' \
  "$smoke_tmp/credential.json" >/dev/null

aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api head-bucket --bucket "$bucket_name"
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api put-object --bucket "$bucket_name" --key "$object_key" \
  --body "$smoke_tmp/payload" --content-type text/plain >/dev/null
object_uploaded=true
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api head-object --bucket "$bucket_name" --key "$object_key" >/dev/null
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api get-object --bucket "$bucket_name" --key "$object_key" \
  "$smoke_tmp/download" >/dev/null
cmp "$smoke_tmp/payload" "$smoke_tmp/download"
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api list-objects-v2 --bucket "$bucket_name" --prefix probe/ \
  | jq -e --arg key "$object_key" '.Contents | any(.Key == $key)' >/dev/null

aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api delete-object --bucket "$bucket_name" --key "$object_key" >/dev/null
object_uploaded=false
api -X DELETE \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/s3-credentials/$credential_id" \
  >/dev/null
credential_revoked=true

if aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api head-bucket --bucket "$bucket_name" \
  >/dev/null 2>"$smoke_tmp/revoked.stderr"; then
  echo "revoked S3 credential still authorizes new requests" >&2
  exit 1
fi
if ! grep -q 'InvalidAccessKeyId' "$smoke_tmp/revoked.stderr"; then
  echo "revocation check failed for an unexpected reason:" >&2
  sed -n '1,10p' "$smoke_tmp/revoked.stderr" >&2
  exit 1
fi

api -X DELETE \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id" >/dev/null
bucket_deleted=true

echo "Gregale S3 smoke passed; temporary bucket ${bucket_name} was deleted"
