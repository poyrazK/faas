#!/usr/bin/env bash
# Provision the provider-owned half of a replacement compute node and emit the
# provider-neutral claim consumed by gregalectl. Dry-run is the default.
set -euo pipefail

project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
instance=""
node=""
zone="${GCP_COMPUTE_ZONE:-europe-west3-b}"
network="${GCP_COMPUTE_NETWORK:-default}"
private_dns_name=""
private_dns_zone=""
claim=""
ssh_user=""
ssh_public_key_file=""
apply=0
resume_existing=0

usage() {
  cat <<'USAGE'
Usage: gcp_provision_compute.sh --instance NAME --node FLEET_NODE [--zone ZONE] [--network NETWORK]
       [--private-dns-name FQDN] [--private-dns-zone ZONE] [--claim PATH]
       [--ssh-user USER --ssh-public-key-file PATH] [--resume-existing] [--apply]

Creates an N2 compute VM with nested virtualization, retained SSD storage,
deletion protection, OS Login/IAP metadata, and the dedicated compute identity.
On apply it uses OS Login only for the provider bootstrap, installs the fleet
operator's public key, converges the node's private runtime DNS A record, waits
for that account to be ready, and writes a host-key-pinned ComputeNodeClaim.
Apply requires both SSH operator arguments. --resume-existing accepts only an
existing VM that matches the managed compute shape, then resumes every
idempotent bootstrap step without creating another machine.
USAGE
}

while (($#)); do
  case "$1" in
    --instance) instance="${2:?}"; shift 2 ;;
    --node) node="${2:?}"; shift 2 ;;
    --zone) zone="${2:?}"; shift 2 ;;
    --network) network="${2:?}"; shift 2 ;;
    --private-dns-name) private_dns_name="${2:?}"; shift 2 ;;
    --private-dns-zone) private_dns_zone="${2:?}"; shift 2 ;;
    --claim) claim="${2:?}"; shift 2 ;;
    --ssh-user) ssh_user="${2:?}"; shift 2 ;;
    --ssh-public-key-file) ssh_public_key_file="${2:?}"; shift 2 ;;
    --resume-existing) resume_existing=1; shift ;;
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[[ "$instance" =~ ^faas-compute-node-[a-z0-9-]+$ ]] || { echo "--instance must use faas-compute-node-*" >&2; exit 2; }
[[ "$node" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || { echo "--node must be a static or dynamic-policy compute-node name" >&2; exit 2; }
[[ "$network" =~ ^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$ ]] || { echo "--network is not a valid GCP network name" >&2; exit 2; }
private_dns_name="${private_dns_name:-${node}.gregale.dev}"
private_dns_name="${private_dns_name%.}"
private_dns_zone="${private_dns_zone:-gregale-${node//./-}-private}"
[[ "$private_dns_name" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]] || { echo "--private-dns-name is not a valid FQDN" >&2; exit 2; }
[[ "$private_dns_name" == *.* ]] || { echo "--private-dns-name must be a fully qualified DNS name" >&2; exit 2; }
[[ "$private_dns_zone" =~ ^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$ ]] || { echo "--private-dns-zone is not a valid GCP managed-zone name" >&2; exit 2; }
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

existing_instance_json=""
instance_exists=0
if existing_instance_json="$(gcloud compute instances describe "$instance" \
    --project="$project" --zone="$zone" --format=json 2>/dev/null)"; then
  instance_exists=1
  if ((resume_existing == 0)); then
    echo "instance already exists: $instance; rerun with --resume-existing to validate and continue" >&2
    exit 1
  fi
  if ! python3 -c '
import json, sys

data = json.load(sys.stdin)
instance, zone, project, network = sys.argv[1:]
base = lambda value: str(value or "").rstrip("/").rsplit("/", 1)[-1]
errors = []

def require(ok, message):
    if not ok:
        errors.append(message)

require(data.get("name") == instance, "instance name mismatch")
require(base(data.get("zone")) == zone, "zone mismatch")
require(base(data.get("machineType")) == "n2-standard-4", "machine type must be n2-standard-4")
require(data.get("status") == "RUNNING", "instance must be RUNNING")
require(data.get("deletionProtection") is True, "deletion protection must be enabled")
require(data.get("advancedMachineFeatures", {}).get("enableNestedVirtualization") is True, "nested virtualization must be enabled")

service_accounts = data.get("serviceAccounts") or []
want_sa = f"gregale-compute@{project}.iam.gserviceaccount.com"
require(any(item.get("email") == want_sa for item in service_accounts), "dedicated compute service account is missing")
require(any("https://www.googleapis.com/auth/cloud-platform" in (item.get("scopes") or []) for item in service_accounts), "cloud-platform scope is missing")

interfaces = data.get("networkInterfaces") or []
require(len(interfaces) == 1, "exactly one network interface is required")
if interfaces:
    require(base(interfaces[0].get("network")) == network, "network mismatch")
    require(bool(interfaces[0].get("networkIP")), "private network IP is missing")
    require(not interfaces[0].get("accessConfigs"), "public network access is not allowed")

metadata = {item.get("key"): str(item.get("value", "")).upper() for item in data.get("metadata", {}).get("items", [])}
require(metadata.get("enable-oslogin") == "TRUE", "enable-oslogin metadata must be TRUE")
require(metadata.get("block-project-ssh-keys") == "TRUE", "block-project-ssh-keys metadata must be TRUE")
require("gregale-admin" in (data.get("tags", {}).get("items") or []), "gregale-admin network tag is missing")
labels = data.get("labels") or {}
require(labels.get("service") == "gregale" and labels.get("role") == "compute" and labels.get("recovery") == "managed", "managed compute labels are missing")

disks = data.get("disks") or []
boot = [disk for disk in disks if disk.get("boot")]
storage = [disk for disk in disks if disk.get("deviceName") == "faas-fc-storage"]
require(len(boot) == 1 and boot[0].get("autoDelete") is False, "retained boot disk is missing")
require(len(storage) == 1 and storage[0].get("autoDelete") is False and int(storage[0].get("diskSizeGb", 0)) >= 100, "retained faas-fc-storage disk is missing or too small")

if errors:
    print("existing instance does not match the Gregale managed-compute contract: " + "; ".join(errors), file=sys.stderr)
    raise SystemExit(1)
' "$instance" "$zone" "$project" "$network" <<<"$existing_instance_json"; then
    exit 1
  fi
fi

create_args=(gcloud compute instances create "$instance"
  --project="$project" --zone="$zone"
  --network="$network"
  --machine-type=n2-standard-4
  --image-project=ubuntu-os-cloud --image-family=ubuntu-2404-lts-amd64
  # The OS disk does not carry Firecracker artifacts. Keep it on standard PD
  # so each compute node consumes regional SSD quota only for its dedicated
  # fast data disk, matching the existing production compute layout.
  --boot-disk-size=100GB --boot-disk-type=pd-standard --no-boot-disk-auto-delete
  --create-disk="name=${instance}-storage,device-name=faas-fc-storage,size=100GB,type=pd-ssd,auto-delete=no"
  --enable-nested-virtualization --maintenance-policy=MIGRATE
  --service-account="gregale-compute@${project}.iam.gserviceaccount.com"
  --scopes=cloud-platform --no-address --deletion-protection
  "--metadata=enable-oslogin=TRUE,block-project-ssh-keys=TRUE"
  --tags=gregale-admin
  "--labels=service=gregale,role=compute,recovery=managed")
if ((instance_exists == 0)); then
  printf '+'; printf ' %q' "${create_args[@]}"; printf '\n'
else
  echo "resume validated existing instance=$instance zone=$zone"
fi
echo "private_dns=${private_dns_name}. zone=$private_dns_zone network=$network"
if ((apply == 0)); then
  echo "dry run complete; --apply converges the VM, private DNS, operator access, and emits $claim"
  exit 0
fi

started_at="$(date +%s)"
if ((instance_exists == 0)); then
  "${create_args[@]}" --quiet
fi

instance_json="$(gcloud compute instances describe "$instance" --project="$project" --zone="$zone" --format=json)"
private_ip="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["networkInterfaces"][0]["networkIP"])' <<<"$instance_json")"
[[ "$private_ip" =~ ^10\.|^172\.(1[6-9]|2[0-9]|3[01])\.|^192\.168\. ]] || {
  echo "instance does not have an RFC1918 private IP: $private_ip" >&2
  exit 1
}

dns_zone_json=""
if dns_zone_json="$(gcloud dns managed-zones describe "$private_dns_zone" --project="$project" --format=json 2>/dev/null)"; then
  python3 -c '
import json, sys
data = json.load(sys.stdin)
dns_name, network = sys.argv[1:]
base = lambda value: str(value or "").rstrip("/").rsplit("/", 1)[-1]
networks = [base(item.get("networkUrl")) for item in data.get("privateVisibilityConfig", {}).get("networks", [])]
if data.get("dnsName") != dns_name + "." or data.get("visibility") != "private" or network not in networks:
    print("existing managed zone does not match the requested private DNS name/network", file=sys.stderr)
    raise SystemExit(1)
' "$private_dns_name" "$network" <<<"$dns_zone_json"
else
  gcloud dns managed-zones create "$private_dns_zone" \
    --project="$project" --dns-name="${private_dns_name}." \
    --visibility=private --networks="$network" \
    --description="Gregale private runtime identity for $node" --quiet
fi

current_rrdatas=""
current_record_json=""
dns_record_exists=0
if current_record_json="$(gcloud dns record-sets describe "${private_dns_name}." \
    --project="$project" --zone="$private_dns_zone" --type=A --format=json 2>/dev/null)"; then
  dns_record_exists=1
  current_rrdatas="$(python3 -c 'import json,sys; data=json.load(sys.stdin); print(" ".join(data.get("rrdatas", [])))' <<<"$current_record_json")"
fi
if ((dns_record_exists == 0)); then
  gcloud dns record-sets create "${private_dns_name}." \
    --project="$project" --zone="$private_dns_zone" --type=A \
    --ttl=300 --rrdatas="$private_ip" --quiet
elif [[ "$current_rrdatas" != "$private_ip" ]]; then
  gcloud dns record-sets update "${private_dns_name}." \
    --project="$project" --zone="$private_dns_zone" --type=A \
    --ttl=300 --rrdatas="$private_ip" --quiet
fi

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
test -n "\$operator_home"
sudo install -d -o '$ssh_user' -g '$ssh_user' -m 0700 "\$operator_home/.ssh"
printf '%s' '$ssh_public_key_b64' | base64 -d | sudo install -o '$ssh_user' -g '$ssh_user' -m 0600 /dev/stdin "\$operator_home/.ssh/authorized_keys"
printf '%s ALL=(ALL) NOPASSWD:ALL\n' '$ssh_user' | sudo install -o root -g root -m 0440 /dev/stdin '/etc/sudoers.d/90-gregale-operator'
sudo visudo -cf '/etc/sudoers.d/90-gregale-operator' >/dev/null
sudo passwd --lock '$ssh_user' >/dev/null
sudo -u '$ssh_user' sudo -n true
EOF
)"
gcloud compute ssh "$instance" --project="$project" --zone="$zone" \
  --tunnel-through-iap --quiet --command="$bootstrap_command" >/dev/null

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
echo "provider_ready_seconds=$elapsed instance=$instance node=$node private_dns=${private_dns_name}. claim=$claim"
echo "create and sign a FleetEnrollmentBundle from the claim, then dispatch cd-compute or use gregalectl deploy join-node"
