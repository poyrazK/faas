#!/usr/bin/env bash
# Safely remove a stopped legacy compute VM and its retained disks. Dry-run is
# the default; apply requires a separately verified release-current SSD node.
set -euo pipefail

project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
instance=""
zone=""
apply=0

usage() {
  cat <<'USAGE'
Usage: gcp_retire_compute.sh --instance NAME [--zone ZONE] [--apply]

Refuses a running VM, the control plane, or the last running compute host.
Prints the deletion-protection, instance, and retained-disk operations by
default. Apply additionally requires GCP_COMPUTE_RETIRE_VERIFIED=1 after the
replacement node has passed release, snapshot-restore, and traffic checks.
USAGE
}

while (($#)); do
  case "$1" in
    --instance) instance="${2:?--instance requires a value}"; shift 2 ;;
    --zone) zone="${2:?--zone requires a value}"; shift 2 ;;
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ "$instance" =~ ^faas-compute-node-[a-z0-9-]+$ ]] || {
  echo "--instance must use faas-compute-node-*" >&2
  exit 2
}
active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | paste -sd, -)"
[[ "$active" == "$operator" ]] || { echo "active account '$active' is not '$operator'" >&2; exit 1; }
[[ "$(gcloud config get-value project 2>/dev/null)" == "$project" ]] || {
  echo "active project is not '$project'" >&2
  exit 1
}
if [[ -z "$zone" ]]; then
  zone="$(gcloud compute instances list --project="$project" --filter="name=($instance)" \
    --format='value(zone.basename())' | head -1)"
fi
[[ -n "$zone" ]] || { echo "instance is missing: $instance" >&2; exit 1; }

description="$(gcloud compute instances describe "$instance" --project="$project" --zone="$zone" --format=json)"
status="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("status", ""))' <<<"$description")"
[[ "$status" == TERMINATED ]] || {
  echo "refusing to retire $instance: status is ${status:-unknown}, expected TERMINATED" >&2
  exit 1
}

running_others="$(gcloud compute instances list --project="$project" \
  --filter="name~^faas-compute-node- AND status=RUNNING AND NOT name=$instance" \
  --format='value(name)' | wc -l | tr -d ' ')"
((running_others >= 1)) || { echo "refusing to remove the last recovery-capable compute host" >&2; exit 1; }

mapfile_cmd='import json,sys
for disk in json.load(sys.stdin).get("disks", []):
    print(disk.get("source", "").rstrip("/").split("/")[-1])'
disks=()
while IFS= read -r disk; do
  [[ -n "$disk" ]] && disks+=("$disk")
done < <(python3 -c "$mapfile_cmd" <<<"$description")
(( ${#disks[@]} > 0 )) || { echo "refusing: $instance has no discoverable disks" >&2; exit 1; }

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  ((apply == 0)) || "$@"
}

if ((apply)) && [[ "${GCP_COMPUTE_RETIRE_VERIFIED:-}" != 1 ]]; then
  echo "refusing apply: set GCP_COMPUTE_RETIRE_VERIFIED=1 after the replacement-node checks" >&2
  exit 1
fi

run gcloud compute instances update "$instance" --project="$project" --zone="$zone" \
  --no-deletion-protection --quiet
run gcloud compute instances delete "$instance" --project="$project" --zone="$zone" --quiet
for disk in "${disks[@]}"; do
  run gcloud compute disks delete "$disk" --project="$project" --zone="$zone" --quiet
done

if ((apply)); then
  if gcloud compute instances describe "$instance" --project="$project" --zone="$zone" >/dev/null 2>&1; then
    echo "retirement verification failed: instance still exists" >&2
    exit 1
  fi
  for disk in "${disks[@]}"; do
    if gcloud compute disks describe "$disk" --project="$project" --zone="$zone" >/dev/null 2>&1; then
      echo "retirement verification failed: disk still exists: $disk" >&2
      exit 1
    fi
  done
  echo "retired_instance=$instance retired_disks=${disks[*]}"
else
  echo "dry run complete; no VM or disk was changed"
fi
