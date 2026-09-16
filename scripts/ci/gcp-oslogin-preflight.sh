#!/usr/bin/env bash
# Fail before the bounded SSH wait when the native acceptance node cannot use
# OS Login. The CI principal intentionally has no setMetadata permission, so
# falling back to an ephemeral project SSH key can never succeed.
set -euo pipefail

project="${GCP_PROJECT:-}"
zone="${GCP_ZONE:-}"
instance="${GCP_INSTANCE:-}"

while (($# > 0)); do
  case "$1" in
    --project) project="${2:-}"; shift 2 ;;
    --zone) zone="${2:-}"; shift 2 ;;
    --instance) instance="${2:-}"; shift 2 ;;
    *) echo "gcp-oslogin-preflight: unknown argument: $1" >&2; exit 2 ;;
  esac
done

for value_name in project zone instance; do
  if [[ -z "${!value_name}" ]]; then
    echo "gcp-oslogin-preflight: --${value_name} is required" >&2
    exit 2
  fi
done

instance_json="$(gcloud compute instances describe "$instance" \
  --project "$project" --zone "$zone" --format='json(metadata.items)')"
project_json="$(gcloud compute project-info describe \
  --project "$project" --format='json(commonInstanceMetadata.items)')"

metadata_value() {
  local body="$1" key="$2" root="$3"
  jq -r --arg key "$key" --arg root "$root" \
    '(.[$root].items // []) | map(select(.key == $key)) | last | .value // empty' \
    <<<"$body"
}

os_login="$(metadata_value "$instance_json" enable-oslogin metadata)"
source="instance"
if [[ -z "$os_login" ]]; then
  os_login="$(metadata_value "$project_json" enable-oslogin commonInstanceMetadata)"
  source="project"
fi

os_login_upper="$(printf '%s' "$os_login" | tr '[:lower:]' '[:upper:]')"
case "$os_login_upper" in
  TRUE)
    echo "gcp-oslogin-preflight: OK instance=$instance enable-oslogin=TRUE source=$source"
    ;;
  FALSE)
    echo "::error::native acceptance node $instance has effective enable-oslogin=FALSE ($source metadata); the narrow CI role cannot write fallback SSH keys" >&2
    exit 1
    ;;
  *)
    echo "::error::native acceptance node $instance has no effective enable-oslogin metadata; set enable-oslogin=TRUE before SSH readiness checks" >&2
    exit 1
    ;;
esac
