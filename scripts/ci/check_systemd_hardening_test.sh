#!/usr/bin/env bash
# Hermetic tests for check_systemd_hardening.sh: run it against a copy of the
# committed units, then against copies with one regression each.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
check="$repo_root/scripts/ci/check_systemd_hardening.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

fresh_root() {
  rm -rf "$work/root"
  mkdir -p "$work/root/deploy/ansible"
  cp -R "$repo_root/deploy/ansible/roles" "$work/root/deploy/ansible/roles"
  cp -R "$repo_root/deploy/systemd" "$work/root/deploy/systemd"
}

# The committed units pass.
fresh_root
out="$(bash "$check" "$work/root" 2>&1)" || fail "committed units failed: $out"
[[ "$out" == *"systemd-hardening-check: OK"* ]] || fail "no pass message: $out"

# systemd refuses user names longer than 31 characters, so such a unit never
# starts. faas-notification-receiver-metrics (34) failed every start on
# gregale-cp after rc.344.
unit="$work/root/deploy/ansible/roles/notification_receiver_metrics/templates/faas-notification-receiver-metrics.service.j2"
fresh_root
sed -i.bak 's/^User=.*/User=faas-notification-receiver-metrics/' "$unit"
if out="$(bash "$check" "$work/root" 2>&1)"; then
  fail "a 34-character User= passed: $out"
fi
[[ "$out" == *"User=faas-notification-receiver-metrics is 34 characters; systemd allows at most 31"* ]] \
  || fail "no user-name finding: $out"

# Exactly 31 characters is accepted.
fresh_root
sed -i.bak 's/^User=.*/User=faas-notification-recvr-metrics/' "$unit"
[[ "$(grep '^User=' "$unit" | cut -d= -f2 | tr -d '\n' | wc -c | tr -d ' ')" == 31 ]] || fail "fixture is not 31 characters"
out="$(bash "$check" "$work/root" 2>&1)" || fail "a 31-character User= failed: $out"

echo "check_systemd_hardening_test: OK"
