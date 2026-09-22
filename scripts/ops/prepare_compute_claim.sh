#!/usr/bin/env bash
# Validate an already-created Linux host and emit the provider-neutral claim
# consumed by enroll_compute_claim.sh. This is the common handoff for bare
# metal and providers that do not have a first-party Gregale provisioner.
set -euo pipefail

node=""
ssh_host=""
ssh_user="root"
ssh_port=22
identity_file=""
host_key_sha256=""
storage_device=""
format_storage=0
claim=""

usage() {
  cat <<'USAGE'
Usage: prepare_compute_claim.sh --node NAME --ssh-host HOST --identity-file PATH
       --host-key-sha256 SHA256:... --storage-device /dev/disk/by-id/...
       [--ssh-user USER] [--ssh-port PORT] [--format-storage] [--claim PATH]

Connects read-only to an already-created Linux x86_64 host, verifies the SSH
host key against an independently supplied fingerprint, and checks systemd,
passwordless root access, hardware virtualization, /dev/kvm, and the stable
storage device. It then atomically writes a host-key-pinned ComputeNodeClaim.

--format-storage does not format anything. It authorizes the later join and
therefore makes this preflight require a completely blank, unmounted device.
USAGE
}

while (($#)); do
  case "$1" in
    --node) node="${2:?}"; shift 2 ;;
    --ssh-host) ssh_host="${2:?}"; shift 2 ;;
    --ssh-user) ssh_user="${2:?}"; shift 2 ;;
    --ssh-port) ssh_port="${2:?}"; shift 2 ;;
    --identity-file) identity_file="${2:?}"; shift 2 ;;
    --host-key-sha256) host_key_sha256="${2:?}"; shift 2 ;;
    --storage-device) storage_device="${2:?}"; shift 2 ;;
    --format-storage) format_storage=1; shift ;;
    --claim) claim="${2:?}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ "$node" =~ ^[a-z0-9][a-z0-9.-]{0,252}$ ]] || { echo "--node is not a valid compute-node name" >&2; exit 2; }
[[ "$ssh_host" =~ ^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$ ]] || { echo "--ssh-host must be an IP address or DNS hostname" >&2; exit 2; }
[[ "$ssh_user" =~ ^[A-Za-z_][A-Za-z0-9_.-]{0,63}$ ]] || { echo "--ssh-user is not a valid account name" >&2; exit 2; }
if [[ ! "$ssh_port" =~ ^[0-9]+$ ]] || ((ssh_port < 1 || ssh_port > 65535)); then
  echo "--ssh-port must be between 1 and 65535" >&2
  exit 2
fi
[[ -f "$identity_file" ]] || { echo "--identity-file must name an existing private key" >&2; exit 2; }
[[ "$host_key_sha256" =~ ^SHA256:[A-Za-z0-9+/]{43}$ ]] || { echo "--host-key-sha256 must use OpenSSH SHA256:<base64> format" >&2; exit 2; }
[[ "$storage_device" =~ ^/dev/disk/by-id/[A-Za-z0-9._:+-]+$ ]] || {
  echo "--storage-device must use a stable /dev/disk/by-id path" >&2
  exit 2
}
claim="${claim:-/tmp/${node}-compute-claim.yaml}"

for command_name in ssh ssh-keyscan ssh-keygen; do
  command -v "$command_name" >/dev/null 2>&1 || { echo "required command is missing: $command_name" >&2; exit 1; }
done

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
scanned_keys="$tmp_dir/scanned_keys"
verified_keys="$tmp_dir/verified_known_hosts"
: >"$verified_keys"

if ! ssh-keyscan -T 10 -p "$ssh_port" -t ed25519,rsa "$ssh_host" >"$scanned_keys" 2>/dev/null; then
  echo "could not scan SSH host keys from $ssh_host:$ssh_port" >&2
  exit 1
fi
while IFS= read -r host_key_line; do
  [[ -n "$host_key_line" ]] || continue
  candidate="$tmp_dir/candidate"
  printf '%s\n' "$host_key_line" >"$candidate"
  candidate_fingerprint="$(ssh-keygen -lf "$candidate" -E sha256 2>/dev/null | awk 'NR == 1 { print $2 }')"
  if [[ "$candidate_fingerprint" == "$host_key_sha256" ]]; then
    printf '%s\n' "$host_key_line" >>"$verified_keys"
  fi
done <"$scanned_keys"
[[ -s "$verified_keys" ]] || {
  echo "SSH host key mismatch for $ssh_host:$ssh_port; refusing an unpinned connection" >&2
  exit 1
}

printf -v quoted_storage '%q' "$storage_device"
remote_command="if [ \"\$(id -u)\" -eq 0 ]; then exec bash -s -- $quoted_storage $format_storage; else exec sudo -n bash -s -- $quoted_storage $format_storage; fi"
ssh -p "$ssh_port" -i "$identity_file" \
  -o BatchMode=yes -o ConnectTimeout=15 -o IdentitiesOnly=yes \
  -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$verified_keys" \
  "$ssh_user@$ssh_host" "$remote_command" <<'REMOTE'
set -euo pipefail
storage_device="${1:?}"
format_storage="${2:?}"

[[ "$(uname -s)" == Linux ]] || { echo "compute host must run Linux" >&2; exit 1; }
[[ "$(uname -m)" == x86_64 ]] || { echo "compute host must use x86_64" >&2; exit 1; }
[[ -d /run/systemd/system ]] || { echo "compute host must boot with systemd" >&2; exit 1; }
[[ -c /dev/kvm && -r /dev/kvm && -w /dev/kvm ]] || {
  echo "/dev/kvm must exist and be readable/writable by root" >&2
  exit 1
}
grep -Eq '(^|[[:space:]])(vmx|svm)([[:space:]]|$)' /proc/cpuinfo || {
  echo "CPU virtualization flags are unavailable; enable virtualization or nested virtualization" >&2
  exit 1
}
[[ -L "$storage_device" ]] || { echo "stable storage path is not a symlink: $storage_device" >&2; exit 1; }
storage_real="$(readlink -e "$storage_device")"
[[ -b "$storage_real" ]] || { echo "storage path does not resolve to a block device: $storage_device" >&2; exit 1; }

root_source="$(findmnt -n -o SOURCE /)"
root_real="$(readlink -f "$root_source" 2>/dev/null || printf '%s' "$root_source")"
root_ancestry="$(lsblk -nrpo NAME -s "$root_real")"
storage_ancestry="$(lsblk -nrpo NAME -s "$storage_real")"
while IFS= read -r root_block; do
  [[ -n "$root_block" ]] || continue
  if grep -Fxq "$root_block" <<<"$storage_ancestry"; then
    echo "storage device must not share the operating-system block-device tree" >&2
    exit 1
  fi
done <<<"$root_ancestry"

if [[ "$format_storage" == 1 ]]; then
  if lsblk -nrpo MOUNTPOINT "$storage_real" | grep -Eq '/'; then
    echo "refusing --format-storage because the storage device or a child is mounted" >&2
    exit 1
  fi
  if [[ -n "$(wipefs -n "$storage_real" 2>/dev/null)" ]]; then
    echo "refusing --format-storage because the storage device is not blank" >&2
    exit 1
  fi
fi

echo "host_preflight=ready architecture=x86_64 kvm=ready storage=$storage_device"
REMOTE

claim_dir="$(dirname "$claim")"
mkdir -p "$claim_dir"
claim_tmp="$(mktemp "$claim_dir/.compute-claim.XXXXXX")"
chmod 0600 "$claim_tmp"
cat >"$claim_tmp" <<EOF
api_version: gregale.dev/v1alpha1
kind: ComputeNodeClaim
metadata:
  name: $node
spec:
  ssh:
    host: $ssh_host
    user: $ssh_user
    port: $ssh_port
    host_key_sha256: $host_key_sha256
  storage:
    device: $storage_device
    format: $([[ "$format_storage" == 1 ]] && echo true || echo false)
EOF
mv -f "$claim_tmp" "$claim"
echo "claim_ready node=$node ssh=$ssh_host:$ssh_port claim=$claim"
