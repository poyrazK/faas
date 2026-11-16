#!/usr/bin/env bash
# compute_role_record.sh fetch OUT_FILE
#
# Reads a compute node's role-hash record
# (/var/lib/faas/bootstrap/compute-roles.json, written by node_join.yml) into
# OUT_FILE, for cd-compute's per-role convergence plan
# (docs/adr/20261115120000-full-release-converge-speed.md).
#
# The node's host key must match SSH_HOST_KEY_SHA256, the fingerprint
# gregalectl deploy join verifies too; nothing else is trusted. Exits 1
# without writing OUT_FILE when the node has no record or cannot be read:
# cd-compute then converges every role, so a failure here only costs time.
#
# Environment: SSH_HOST, SSH_USER, SSH_PORT, SSH_HOST_KEY_SHA256,
# ARTIFACT_DIR (compute-ssh-key).
set -euo pipefail

if [[ $# -ne 2 || "$1" != fetch ]]; then
  echo "usage: compute_role_record.sh fetch OUT_FILE" >&2
  exit 2
fi
out=$2
host=${SSH_HOST:-}
user=${SSH_USER:-root}
port=${SSH_PORT:-22}
want=${SSH_HOST_KEY_SHA256:-}
key="${ARTIFACT_DIR:-}/compute-ssh-key"
if [[ -z "$host" || -z "$want" || ! -s "$key" ]]; then
  echo "compute_role_record: no SSH address, host-key fingerprint or key; converging every role" >&2
  exit 1
fi
want=${want#SHA256:}

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
known="$work/known_hosts"
# Keep only the scanned keys whose fingerprint is the pinned one.
ssh-keyscan -T 10 -p "$port" "$host" 2>/dev/null > "$work/scanned" || true
while read -r line; do
  [[ -n "$line" && "$line" != \#* ]] || continue
  fp=$(ssh-keygen -lf - <<<"$line" 2>/dev/null | awk '{print $2}')
  if [[ "${fp#SHA256:}" == "$want" ]]; then
    printf '%s\n' "$line" >> "$known"
  fi
done < "$work/scanned"
if [[ ! -s "$known" ]]; then
  echo "compute_role_record: $host presented no host key matching the pinned fingerprint; converging every role" >&2
  exit 1
fi

read_cmd='cat /var/lib/faas/bootstrap/compute-roles.json'
if [[ "$user" != root ]]; then
  read_cmd="sudo -n $read_cmd"
fi
if ! ssh -i "$key" -p "$port" \
  -o BatchMode=yes -o ConnectTimeout=15 \
  -o UserKnownHostsFile="$known" -o StrictHostKeyChecking=yes \
  "$user@$host" "$read_cmd" > "$work/record.json" 2>/dev/null; then
  echo "compute_role_record: $host has no role record yet; converging every role" >&2
  exit 1
fi
if ! jq -e '.group == "compute_nodes" and (.roles | type == "object")' "$work/record.json" >/dev/null 2>&1; then
  echo "compute_role_record: $host's role record is not a compute_nodes record; converging every role" >&2
  exit 1
fi
install -m 0600 "$work/record.json" "$out"
