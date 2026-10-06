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
GREGALE_BIN="${GREGALE_BIN:-}"
if [[ -n "$GREGALE_BIN" && ! -x "$GREGALE_BIN" ]]; then
  echo "GREGALE_BIN must name an executable current Gregale CLI" >&2
  exit 2
fi

for tool in aws curl jq; do
  command -v "$tool" >/dev/null || {
    echo "missing required command: $tool" >&2
    exit 2
  }
done

smoke_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gregale-s3-smoke.XXXXXX")"
bucket_name="smoke-$(date -u +%Y%m%d%H%M%S)-$(printf '%04x%04x' "$RANDOM" "$RANDOM")"
object_key="probe/hello.txt"
bucket_id=""
credential_id=""
binding_id=""
binding_prefix="SMOKE_S3"
object_uploaded=false
credential_revoked=false
binding_deleted=false
bucket_deleted=false
provision_attempted=false

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

  # The CLI may create the bucket before a binding/network failure prevents
  # its final JSON response. Recover only this run's random logical name.
  if [[ "$provision_attempted" == true && -z "$bucket_id" ]]; then
    if api "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets" \
      >"$smoke_tmp/cleanup-catalog.json" 2>/dev/null; then
      bucket_id="$(jq -r --arg name "$bucket_name" \
        '[.items[] | select(.name == $name and .scope == "default")] | if length == 1 then .[0].id else empty end' \
        "$smoke_tmp/cleanup-catalog.json")"
    else
      cleanup_status=1
    fi
  fi

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
  if [[ -n "$binding_id" && "$binding_deleted" != true ]]; then
    api -X DELETE \
      "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings/$binding_id" \
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

provision_attempted=true
if [[ -n "$GREGALE_BIN" ]]; then
  FAAS_API="$GREGALE_API_URL" "$GREGALE_BIN" --json add bucket "$bucket_name" \
    --app "$GREGALE_APP_SLUG" --env default --region "$GREGALE_S3_REGION" \
    --prefix "$binding_prefix" --wait-timeout 60s >"$smoke_tmp/cli-add.json"
  jq '.bucket' "$smoke_tmp/cli-add.json" >"$smoke_tmp/bucket.json"
  jq '.binding' "$smoke_tmp/cli-add.json" >"$smoke_tmp/binding.json"
  binding_id="$(jq -er '.id' "$smoke_tmp/binding.json")"
else
  api -X POST \
    --data "$(jq -cn --arg name "$bucket_name" '{name:$name,scope:"default"}')" \
    "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets" \
    >"$smoke_tmp/bucket.json"
fi
bucket_id="$(jq -er '.id' "$smoke_tmp/bucket.json")"
jq -e '.state == "ready"' "$smoke_tmp/bucket.json" >/dev/null

# Compute bindings deliberately never return secret material. Verify the
# managed secret names, binding identity, and rotation contract instead. The
# direct S3 credential below supplies the data-plane read/write and revoke
# checks; combining both in one run catches cross-surface cleanup leaks.
if [[ -z "$binding_id" ]]; then
  api -X POST \
    --data "$(jq -cn --arg prefix "$binding_prefix" '{label:"compute-smoke",permission:"read_write",prefix:$prefix}')" \
    "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings" \
    >"$smoke_tmp/binding.json"
fi
binding_id="$(jq -er '.id' "$smoke_tmp/binding.json")"
jq -e --arg bucket "$bucket_id" --arg prefix "$binding_prefix" '
  .bucket_id == $bucket and .prefix == $prefix and
  .secret_keys == {
    endpoint: ($prefix + "_ENDPOINT"),
    region: ($prefix + "_REGION"),
    bucket: ($prefix + "_BUCKET"),
    access_key_id: ($prefix + "_ACCESS_KEY_ID"),
    secret_access_key: ($prefix + "_SECRET_ACCESS_KEY"),
    addressing_style: ($prefix + "_ADDRESSING_STYLE")
  } and
  (.credential.status == "active") and
  (.credential | has("secret_access_key") | not)
' "$smoke_tmp/binding.json" >/dev/null
binding_access_key_id="$(jq -er '.credential.access_key_id' "$smoke_tmp/binding.json")"

api -X GET \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings" \
  >"$smoke_tmp/bindings-before-rotate.json"
jq -e --arg id "$binding_id" '.items | length == 1 and .[0].id == $id' \
  "$smoke_tmp/bindings-before-rotate.json" >/dev/null

api -X POST \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings/$binding_id/rotate" \
  >"$smoke_tmp/binding-rotated.json"
jq -e --arg id "$binding_id" --arg bucket "$bucket_id" --arg prefix "$binding_prefix" --arg old "$binding_access_key_id" '
  .id == $id and .bucket_id == $bucket and .prefix == $prefix and
  .credential.status == "active" and
  .credential.access_key_id != $old and
  .secret_keys == {
    endpoint: ($prefix + "_ENDPOINT"),
    region: ($prefix + "_REGION"),
    bucket: ($prefix + "_BUCKET"),
    access_key_id: ($prefix + "_ACCESS_KEY_ID"),
    secret_access_key: ($prefix + "_SECRET_ACCESS_KEY"),
    addressing_style: ($prefix + "_ADDRESSING_STYLE")
  }
' "$smoke_tmp/binding-rotated.json" >/dev/null

printf 'compute_binding_lifecycle=pass\n'

# A ready provider bucket may still be waiting for its first complete
# inventory. Never manufacture a report to make this readiness check pass.
usage_ready=false
for ((attempt=0; attempt<90; attempt++)); do
  api "$GREGALE_API_URL/v1/account/object-storage-usage" >"$smoke_tmp/usage-before.json"
  if jq -e '.usage.fresh == true' "$smoke_tmp/usage-before.json" >/dev/null; then
    usage_ready=true
    break
  fi
  sleep 2
done
[[ "$usage_ready" == true ]] || { echo "object storage usage never became fresh" >&2; exit 1; }

if [[ -n "$GREGALE_BIN" ]]; then
  for family in notifications lifecycle; do
    FAAS_API="$GREGALE_API_URL" "$GREGALE_BIN" --json bucket "$family" get \
      "$GREGALE_APP_SLUG" "$bucket_id" >"$smoke_tmp/cli-$family.json"
  done
  FAAS_API="$GREGALE_API_URL" "$GREGALE_BIN" --json bucket writes list \
    "$GREGALE_APP_SLUG" "$bucket_id" >"$smoke_tmp/cli-writes.json"
fi

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
api "$GREGALE_API_URL/v1/account/object-storage-usage" >"$smoke_tmp/usage-after.json"
if jq -e '.policy.accounting_mode == "gateway_safety_v1"' "$smoke_tmp/usage-after.json" >/dev/null; then
  jq -e --slurpfile before "$smoke_tmp/usage-before.json" '
    .billing_mode == "off" and (.charges == null) and .usage.fresh == true and
    .usage.request_count > $before[0].usage.request_count and
    .usage.egress_bytes > $before[0].usage.egress_bytes and
    (.usage.unavailable_meters | index("cost_millicents") != null) and
    (.usage.unavailable_meters | index("stored_byte_hours") != null)
  ' "$smoke_tmp/usage-after.json" >/dev/null
  printf 'gateway_usage_and_billing_off=pass\n'
fi
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api list-objects-v2 --bucket "$bucket_name" --prefix probe/ \
  | jq -e --arg key "$object_key" '.Contents | any(.Key == $key)' >/dev/null

delete_request="$(jq -cn --arg key "$object_key" '{Objects:[{Key:$key}],Quiet:false}')"
aws --endpoint-url "$GREGALE_S3_ENDPOINT" --region "$GREGALE_S3_REGION" \
  s3api delete-objects --bucket "$bucket_name" --delete "$delete_request" \
  | jq -e --arg key "$object_key" '.Deleted | any(.Key == $key)' >/dev/null
object_uploaded=false
api -X DELETE \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/s3-credentials/$credential_id" \
  >/dev/null
credential_revoked=true

api -X DELETE \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings/$binding_id" \
  >/dev/null
binding_deleted=true
api -X GET \
  "$GREGALE_API_URL/v1/apps/$GREGALE_APP_SLUG/buckets/$bucket_id/compute-bindings" \
  | jq -e '.items | length == 0' >/dev/null

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
