#!/usr/bin/env bash
# Turn one provider-produced ComputeNodeClaim into a signed, immutable fleet
# authorization and dispatch the production join. Dry-run is the default.
set -euo pipefail

project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
bucket="${GCP_FLEET_ENROLLMENT_BUCKET:-gregale-fleet-enrollment-5ae37259}"
repository="${GITHUB_REPOSITORY:-poyrazK/faas}"
gregalectl="${GREGALECTL_BIN:-gregalectl}"
claim=""
release_tag=""
generation=""
apply=0
wait_for_rollout=1

usage() {
  cat <<'USAGE'
Usage: enroll_compute_claim.sh --claim PATH --release-tag TAG
       [--generation UINT64] [--no-wait] [--apply]

Validates a provider-neutral ComputeNodeClaim against the exact production
manifest in TAG, creates an immutable private GCS enrollment bundle, waits for
the exact keyless signing run, and dispatches the exact cd-compute rollout.
Dry-run is the default. Set GREGALECTL_BIN when gregalectl is not on PATH.
USAGE
}

while (($#)); do
  case "$1" in
    --claim) claim="${2:?}"; shift 2 ;;
    --release-tag) release_tag="${2:?}"; shift 2 ;;
    --generation) generation="${2:?}"; shift 2 ;;
    --no-wait) wait_for_rollout=0; shift ;;
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -f "$claim" ]] || { echo "claim file does not exist: $claim" >&2; exit 2; }
[[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || {
  echo "--release-tag must be a release tag" >&2
  exit 2
}
[[ "$bucket" =~ ^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$ ]] || { echo "invalid GCS bucket name" >&2; exit 2; }
command -v "$gregalectl" >/dev/null || { echo "gregalectl binary not found: $gregalectl" >&2; exit 1; }
for tool in gcloud gh jq python3 sha256sum; do
  command -v "$tool" >/dev/null || { echo "required tool not found: $tool" >&2; exit 1; }
done
generation="${generation:-$(python3 -c 'import time; print(time.time_ns())')}"
[[ "$generation" =~ ^[1-9][0-9]*$ ]] || { echo "--generation must be a positive integer" >&2; exit 2; }

active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | paste -sd, -)"
[[ "$active" == "$operator" ]] || {
  echo "refusing: active gcloud account is '${active:-none}', expected '$operator'" >&2
  exit 1
}
[[ "$(gcloud config get-value project 2>/dev/null)" == "$project" ]] || {
  echo "refusing: active project is not '$project'" >&2
  exit 1
}
gcloud storage buckets describe "gs://$bucket" >/dev/null
gh auth status --hostname github.com >/dev/null

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
manifest="$work_dir/production-manifest.yaml"
bundle="$work_dir/fleet-enrollment.yaml"

gh release download "$release_tag" --repo "$repository" \
  --pattern production-manifest.yaml --dir "$work_dir"
[[ -s "$manifest" ]] || { echo "release is missing production-manifest.yaml: $release_tag" >&2; exit 1; }

if ! claim_json="$("$gregalectl" deploy claim validate \
  --file "$claim" --manifest-file "$manifest" --json)"; then
  echo "claim validation failed for release $release_tag" >&2
  jq -r '.errors[]? | "\(.path): \(.message)"' <<<"$claim_json" >&2
  exit 1
fi
jq -e '.valid == true' <<<"$claim_json" >/dev/null || { echo "claim validation failed for release $release_tag" >&2; exit 1; }
node="$(jq -er '.node' <<<"$claim_json")"
[[ "$node" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || { echo "claim returned an invalid node name" >&2; exit 1; }

"$gregalectl" deploy fleet-bundle create \
  --claim-file "$claim" --manifest-file "$manifest" \
  --name production --generation "$generation" --output "$bundle"
digest="sha256:$(sha256sum "$bundle" | awk '{print $1}')"
object_prefix="production/$generation"
bundle_object="$object_prefix/fleet-enrollment.yaml"
signature_object="$object_prefix/fleet-enrollment.cosign.bundle"
bundle_url="https://storage.googleapis.com/$bucket/$bundle_object"
signature_url="https://storage.googleapis.com/$bucket/$signature_object"

printf 'node=%s release=%s generation=%s digest=%s\n' "$node" "$release_tag" "$generation" "$digest"
printf 'bundle_url=%s\nsignature_url=%s\n' "$bundle_url" "$signature_url"
if ((apply == 0)); then
  echo "dry run complete; --apply uploads once, signs, and dispatches the join"
  exit 0
fi

started_at="$(date -u +%s)"
gcloud storage cp "$bundle" "gs://$bucket/$bundle_object" \
  --if-generation-match=0 --quiet

gh workflow run fleet-enrollment.yml --repo "$repository" --ref main \
  -f "bundle_url=$bundle_url" \
  -f "bundle_sha256=$digest" \
  -f "signature_upload_url=$signature_url"

find_run() {
  local workflow="$1" title="$2" run_id
  for _ in $(seq 1 30); do
    run_id="$(gh run list --repo "$repository" --workflow "$workflow" \
      --event workflow_dispatch --branch main --limit 50 \
      --json databaseId,displayTitle \
      --jq ".[] | select(.displayTitle == \"$title\") | .databaseId" | head -1)"
    if [[ "$run_id" =~ ^[0-9]+$ ]]; then
      printf '%s\n' "$run_id"
      return 0
    fi
    sleep 2
  done
  echo "could not find exact workflow run: $title" >&2
  return 1
}

sign_title="Sign fleet bundle $digest"
sign_run="$(find_run fleet-enrollment.yml "$sign_title")"
gh run watch "$sign_run" --repo "$repository" --exit-status
gcloud storage objects describe "gs://$bucket/$signature_object" >/dev/null
signed_elapsed="$(( $(date -u +%s) - started_at ))"
echo "bundle_signed_seconds=$signed_elapsed node=$node release=$release_tag"

gh workflow run cd-compute.yml --repo "$repository" --ref main \
  -f "release_tag=$release_tag" \
  -f "fleet_bundle_url=$bundle_url" \
  -f "fleet_bundle_signature_url=$signature_url" \
  -f "fleet_bundle_sha256=$digest" \
  -f "node=$node" \
  -f rollout_phase=full

rollout_title="Compute $node $digest"
rollout_run="$(find_run cd-compute.yml "$rollout_title")"
rollout_url="https://github.com/$repository/actions/runs/$rollout_run"
echo "compute_rollout=$rollout_url"
if ((wait_for_rollout)); then
  gh run watch "$rollout_run" --repo "$repository" --exit-status
  elapsed="$(( $(date -u +%s) - started_at ))"
  echo "enrollment_ready_seconds=$elapsed node=$node release=$release_tag rollout=$rollout_url"
else
  elapsed="$(( $(date -u +%s) - started_at ))"
  echo "enrollment_dispatched_seconds=$elapsed node=$node release=$release_tag rollout=$rollout_url"
fi
