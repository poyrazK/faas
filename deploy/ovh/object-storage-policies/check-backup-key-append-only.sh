#!/usr/bin/env bash
# Prove the backup key is append-only on OVH: it can write a recovery point
# but can neither delete one nor change the bucket's lifecycle. The bucket
# owner is the backup user, so this checks that OVH enforces the policy
# (gregale-pg-backups.json) and not owner rights.
#
# usage: check-backup-key-append-only.sh [BUCKET]
#
# Asks for the key; the secret is read without echo and never printed. The
# probe object goes under faas-cp-state/, so the bucket lifecycle expires it.
# The lifecycle write sends deploy/backup-lifecycle/s3.json, the bucket's
# current policy, so nothing changes even if it were allowed.
set -euo pipefail

bucket=${1:-gregale-prod-pg-backups}
endpoint=${S3_ENDPOINT:-https://s3.bhs.io.cloud.ovh.net}
here=$(cd "$(dirname "$0")" && pwd)
lifecycle="$here/../../backup-lifecycle/s3.json"
command -v aws >/dev/null || { echo "needs the aws CLI" >&2; exit 2; }
[[ -r "$lifecycle" ]] || { echo "missing $lifecycle" >&2; exit 2; }

read -r -p "backup key access key id: " AWS_ACCESS_KEY_ID
read -r -s -p "backup key secret (not shown): " AWS_SECRET_ACCESS_KEY
echo
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION=${S3_REGION:-bhs}
export AWS_PAGER=""

s3() { aws s3api --endpoint-url "$endpoint" "$@"; }

body=$(mktemp)
trap 'rm -f "$body"' EXIT
printf 'append-only probe\n' >"$body"
key="faas-cp-state/append-only-probe-$(date -u +%Y%m%dT%H%M%SZ)"

failed=0
refused() {
  local label=$1 out
  shift
  if out=$("$@" 2>&1); then
    echo "FAIL  $label was allowed"
    failed=1
  elif grep -q AccessDenied <<<"$out"; then
    echo "ok    $label refused (AccessDenied)"
  else
    echo "FAIL  $label failed, but not with AccessDenied: $(head -c 300 <<<"$out")"
    failed=1
  fi
}

if s3 put-object --bucket "$bucket" --key "$key" --body "$body" >/dev/null; then
  echo "ok    put $key"
else
  echo "FAIL  the key cannot write; check the key and the policy before the delete checks" >&2
  exit 1
fi
refused "delete-object $key" s3 delete-object --bucket "$bucket" --key "$key"
refused "put-bucket-lifecycle-configuration" \
  s3 put-bucket-lifecycle-configuration --bucket "$bucket" --lifecycle-configuration "file://$lifecycle"

if ((failed)); then
  echo "the backup key is NOT append-only on $bucket" >&2
  exit 1
fi
echo "the backup key is append-only on $bucket"
