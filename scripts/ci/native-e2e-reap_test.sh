#!/usr/bin/env bash
set -euo pipefail
task_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$task_root/native-e2e-reap.sh"
task_jails=$(mktemp -d)
trap 'rm -rf "$task_jails"' EXIT
mkdir -p "$task_jails/firecracker-v1.7.0/build-versioned/root" \
  "$task_jails/firecracker/build-current/root" \
  "$task_jails/unrelated/keep/root"
reap_stale_jails "$task_jails"
test ! -e "$task_jails/firecracker-v1.7.0/build-versioned"
test ! -e "$task_jails/firecracker/build-current"
test -d "$task_jails/unrelated/keep/root"
printf '%s\n' 'Native reaper covers versioned and versionless jail layouts.'
