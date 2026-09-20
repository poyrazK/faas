#!/usr/bin/env bash
# Converge the keyless GitHub-to-GCS identities used by fleet enrollment.
# Dry-run is the default so a new environment can review every IAM mutation.
set -euo pipefail

project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
bucket="${GCP_FLEET_ENROLLMENT_BUCKET:-gregale-fleet-enrollment-5ae37259}"
pool="${GCP_GITHUB_WORKLOAD_IDENTITY_POOL:-github-actions}"
repository="${GITHUB_REPOSITORY:-poyrazK/faas}"
publisher_id=gregale-fleet-publisher
reader_id=gregale-fleet-reader
publisher_provider=gregale-fleet-publisher
reader_provider=gregale-fleet-reader
apply=0

usage() {
  printf '%s\n' \
    'Usage: gcp_fleet_enrollment_identity.sh [--apply]' \
    '' \
    'Creates least-privilege GitHub OIDC publisher/reader identities for the' \
    'private GCS fleet-enrollment bucket. Existing resources are converged.'
}

while (($#)); do
  case "$1" in
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | paste -sd, -)"
[[ "$active" == "$operator" ]] || {
  echo "refusing: active gcloud account is '${active:-none}', expected '$operator'" >&2
  exit 1
}
[[ "$(gcloud config get-value project 2>/dev/null)" == "$project" ]] || {
  echo "refusing: active project is not '$project'" >&2
  exit 1
}

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  ((apply == 0)) || "$@"
}

exists() { "$@" >/dev/null 2>&1; }

enable_api() {
  local api="$1"
  if ! gcloud services list --enabled --project="$project" --filter="name:$api" \
      --format='value(name)' | grep -Fq "/$api"; then
    run gcloud services enable "$api" --project="$project" --quiet
  fi
}

ensure_service_account() {
  local id="$1" display="$2"
  exists gcloud iam service-accounts describe "${id}@${project}.iam.gserviceaccount.com" --project="$project" \
    || run gcloud iam service-accounts create "$id" --project="$project" --display-name="$display" --quiet
}

ensure_provider() {
  local id="$1" display="$2" workflow="$3" condition current
  condition="assertion.repository == '${repository}' && assertion.job_workflow_ref == '${repository}/.github/workflows/${workflow}@refs/heads/main'"
  current="$(gcloud iam workload-identity-pools providers describe "$id" \
    --project="$project" --location=global --workload-identity-pool="$pool" \
    --format='value(attributeCondition)' 2>/dev/null || true)"
  if [[ -z "$current" ]]; then
    run gcloud iam workload-identity-pools providers create-oidc "$id" \
      --project="$project" --location=global --workload-identity-pool="$pool" \
      --display-name="$display" \
      --issuer-uri=https://token.actions.githubusercontent.com \
      --attribute-mapping='google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.job_workflow_ref=assertion.job_workflow_ref' \
      --attribute-condition="$condition" --quiet
  elif [[ "$current" != "$condition" ]]; then
    run gcloud iam workload-identity-pools providers update-oidc "$id" \
      --project="$project" --location=global --workload-identity-pool="$pool" \
      --display-name="$display" \
      --issuer-uri=https://token.actions.githubusercontent.com \
      --attribute-mapping='google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.job_workflow_ref=assertion.job_workflow_ref' \
      --attribute-condition="$condition" --quiet
  fi
}

service_account_has_role() {
  local resource="$1" member="$2" role="$3"
  gcloud iam service-accounts get-iam-policy "$resource" --project="$project" \
    --flatten='bindings[].members' \
    --filter="bindings.role=$role AND bindings.members=$member" \
    --format='value(bindings.role)' 2>/dev/null | grep -Fqx "$role"
}

ensure_service_account_role() {
  local resource="$1" member="$2" role="$3"
  service_account_has_role "$resource" "$member" "$role" \
    || run gcloud iam service-accounts add-iam-policy-binding "$resource" --project="$project" \
      --member="$member" --role="$role" --quiet
}

bucket_has_role() {
  local member="$1" role="$2"
  gcloud storage buckets get-iam-policy "gs://$bucket" --format=json \
    | python3 -c 'import json,sys
member, role = sys.argv[1:]
policy = json.load(sys.stdin)
raise SystemExit(0 if any(row.get("role") == role and member in row.get("members", []) for row in policy.get("bindings", [])) else 1)' \
      "$member" "$role"
}

ensure_bucket_role() {
  local member="$1" role="$2"
  bucket_has_role "$member" "$role" \
    || run gcloud storage buckets add-iam-policy-binding "gs://$bucket" \
      --member="$member" --role="$role" --quiet
}

enable_api iamcredentials.googleapis.com
enable_api sts.googleapis.com
exists gcloud storage buckets describe "gs://$bucket" || {
  echo "fleet enrollment bucket does not exist: gs://$bucket" >&2
  exit 1
}

ensure_service_account "$publisher_id" 'Gregale fleet enrollment publisher'
ensure_service_account "$reader_id" 'Gregale fleet enrollment reader'
ensure_provider "$publisher_provider" 'Gregale fleet bundle publisher' fleet-enrollment.yml
ensure_provider "$reader_provider" 'Gregale fleet bundle reader' cd-compute.yml

project_number="$(gcloud projects describe "$project" --format='value(projectNumber)')"
[[ "$project_number" =~ ^[0-9]+$ ]] || { echo 'could not resolve GCP project number' >&2; exit 1; }
principal_prefix="principalSet://iam.googleapis.com/projects/${project_number}/locations/global/workloadIdentityPools/${pool}/attribute.job_workflow_ref/${repository}/.github/workflows"
publisher_sa="${publisher_id}@${project}.iam.gserviceaccount.com"
reader_sa="${reader_id}@${project}.iam.gserviceaccount.com"

ensure_service_account_role "$publisher_sa" "${principal_prefix}/fleet-enrollment.yml@refs/heads/main" roles/iam.workloadIdentityUser
ensure_service_account_role "$reader_sa" "${principal_prefix}/cd-compute.yml@refs/heads/main" roles/iam.workloadIdentityUser
ensure_bucket_role "serviceAccount:$publisher_sa" roles/storage.objectCreator
ensure_bucket_role "serviceAccount:$publisher_sa" roles/storage.objectViewer
ensure_bucket_role "serviceAccount:$reader_sa" roles/storage.objectViewer

if ((apply)); then
  echo "fleet enrollment identity converged for gs://$bucket"
else
  echo "fleet enrollment identity dry run complete for gs://$bucket"
fi
