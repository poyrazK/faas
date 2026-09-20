#!/usr/bin/env bash
# Provision the provider-owned half of a replacement compute node and emit the
# provider-neutral claim consumed by gregalectl. Dry-run is the default.
set -euo pipefail

project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
instance=""
node=""
zone="${GCP_COMPUTE_ZONE:-europe-west3-b}"
claim=""
ssh_user=""
ssh_public_key_file=""
apply=0

usage() {
  cat <<'USAGE'
Usage: gcp_provision_compute.sh --instance NAME --node FLEET_NODE [--zone ZONE] [--claim PATH]
       [--ssh-user USER --ssh-public-key-file PATH] [--apply]

Creates an N2 compute VM with nested virtualization, retained SSD storage,
deletion protection, OS Login/IAP metadata, and the dedicated compute identity.
On apply it uses OS Login only for the provider bootstrap, installs the fleet
operator's public key, waits for that account to be ready, and writes a
host-key-pinned ComputeNodeClaim. Apply requires both SSH operator arguments.
USAGE
}

while (($#)); do
  case "$1" in
    --instance) instance="${2:?}"; shift 2 ;;
    --node) node="${2:?}"; shift 2 ;;
    --zone) zone="${2:?}"; shift 2 ;;
    --claim) claim="${2:?}"; shift 2 ;;
    --ssh-user) ssh_user="${2:?}"; shift 2 ;;
    --ssh-public-key-file) ssh_public_key_file="${2:?}"; shift 2 ;;
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ "$instance" =~ ^faas-compute-node-[a-z0-9-]+$ ]] || { echo "--instance must use faas-compute-node-*" >&2; exit 2; }
[[ "$node" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || { echo "--node must be a static or dynamic-policy compute-node name" >&2; exit 2; }
if [[ -n "$ssh_user" ]]; then
  [[ "$ssh_user" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || { echo "--ssh-user is not a valid Linux account name" >&2; exit 2; }
fi
claim="${claim:-/tmp/${node}-gcp-claim.yaml}"

if ((apply == 1)); then
  [[ -n "$ssh_user" ]] || { echo "--ssh-user is required with --apply" >&2; exit 2; }
  [[ -n "$ssh_public_key_file" ]] || { echo "--ssh-public-key-file is required with --apply" >&2; exit 2; }
  [[ -f "$ssh_public_key_file" ]] || { echo "SSH public key file does not exist: $ssh_public_key_file" >&2; exit 2; }
  ssh_public_key="$(awk 'NF { count++; key=$0 } END { if (count != 1) exit 1; print key }' "$ssh_public_key_file")" || {
    echo "SSH public key file must contain exactly one non-empty key" >&2
    exit 2
  }
  [[ "$ssh_public_key" =~ ^(ssh-(ed25519|rsa)|ecdsa-sha2-nistp(256|384|521))[[:space:]] ]] || {
    echo "SSH public key file must contain an OpenSSH public key" >&2
    exit 2
  }
  ssh-keygen -l -f "$ssh_public_key_file" >/dev/null 2>&1 || { echo "SSH public key file is invalid: $ssh_public_key_file" >&2; exit 2; }
fi

active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | paste -sd, -)"
[[ "$active" == "$operator" ]] || { echo "active account '$active' is not '$operator'" >&2; exit 1; }
[[ "$(gcloud config get-value project 2>/dev/null)" == "$project" ]] || { echo "active project is not '$project'" >&2; exit 1; }
if gcloud compute instances describe "$instance" --project="$project" --zone="$zone" >/dev/null 2>&1; then
  echo "instance already exists: $instance" >&2
  exit 1
fi

create_args=(gcloud compute instances create "$instance"
  --project="$project" --zone="$zone"
  --machine-type=n2-standard-4
  --image-project=ubuntu-os-cloud --image-family=ubuntu-2404-lts-amd64
  --boot-disk-size=100GB --boot-disk-type=pd-balanced --no-boot-disk-auto-delete
  --create-disk="name=${instance}-storage,device-name=faas-fc-storage,size=100GB,type=pd-ssd,auto-delete=no"
  --enable-nested-virtualization --maintenance-policy=MIGRATE
  --service-account="gregale-compute@${project}.iam.gserviceaccount.com"
  --scopes=cloud-platform --no-address --deletion-protection
  "--metadata=enable-oslogin=TRUE,block-project-ssh-keys=TRUE"
  --tags=gregale-admin
  "--labels=service=gregale,role=compute,recovery=managed")
printf '+'; printf ' %q' "${create_args[@]}"; printf '\n'
if ((apply == 0)); then
  echo "dry run complete; --apply provisions the VM and emits $claim"
  exit 0
fi

started_at="$(date +%s)"
"${create_args[@]}" --quiet
for _ in $(seq 1 60); do
  if gcloud compute ssh "$instance" --project="$project" --zone="$zone" \
      --tunnel-through-iap --quiet --command='true' >/dev/null 2>&1; then
    break
  fi
  sleep 5
done
gcloud compute ssh "$instance" --project="$project" --zone="$zone" \
  --tunnel-through-iap --quiet --command='true' >/dev/null

# OS Login is the provider bootstrap identity, not Gregale's durable fleet
# identity. Install the public half of COMPUTE_SSH_KEY under the explicit
# operator account before emitting a claim; otherwise cd-compute receives a
# valid host fingerprint but cannot authenticate to the new machine.
ssh_public_key_b64="$(printf '%s\n' "$ssh_public_key" | base64 | tr -d '\n')"
bootstrap_command="$(cat <<EOF
set -eu
if ! id '$ssh_user' >/dev/null 2>&1; then
  sudo useradd --create-home --user-group --shell /bin/bash '$ssh_user'
fi
operator_home=\$(getent passwd '$ssh_user' | cut -d: -f6)
test -n \"\$operator_home\"
sudo install -d -o '$ssh_user' -g '$ssh_user' -m 0700 \"\$operator_home/.ssh\"
printf '%s' '$ssh_public_key_b64' | base64 -d | sudo install -o '$ssh_user' -g '$ssh_user' -m 0600 /dev/stdin \"\$operator_home/.ssh/authorized_keys\"
printf '%s ALL=(ALL) NOPASSWD:ALL\n' '$ssh_user' | sudo install -o root -g root -m 0440 /dev/stdin '/etc/sudoers.d/90-gregale-operator'
sudo visudo -cf '/etc/sudoers.d/90-gregale-operator' >/dev/null
sudo passwd --lock '$ssh_user' >/dev/null
sudo -u '$ssh_user' sudo -n true
EOF
)"
gcloud compute ssh "$instance" --project="$project" --zone="$zone" \
  --tunnel-through-iap --quiet --command="$bootstrap_command" >/dev/null

private_ip="$(gcloud compute instances describe "$instance" --project="$project" --zone="$zone" --format=json \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["networkInterfaces"][0]["networkIP"])')"
fingerprint="$(gcloud compute ssh "$instance" --project="$project" --zone="$zone" \
  --tunnel-through-iap --quiet \
  --command="sudo ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256 | awk '{print \$2}'" \
  | tail -1 | tr -d '\r')"
[[ "$fingerprint" == SHA256:* ]] || { echo "could not read target SSH fingerprint" >&2; exit 1; }

umask 077
cat >"$claim" <<EOF
api_version: gregale.dev/v1alpha1
kind: ComputeNodeClaim
metadata:
  name: $node
spec:
  ssh:
    host: $private_ip
    user: $ssh_user
    port: 22
    host_key_sha256: $fingerprint
  storage:
    device: /dev/disk/by-id/google-faas-fc-storage
    format: true
EOF
elapsed="$(( $(date +%s) - started_at ))"
echo "provider_ready_seconds=$elapsed instance=$instance node=$node claim=$claim"
echo "create and sign a FleetEnrollmentBundle from the claim, then dispatch cd-compute or use gregalectl deploy join-node"
