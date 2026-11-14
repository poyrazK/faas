#!/usr/bin/env bash
# Qualify local receipt and recovery with the deployment-pinned binaries.
set -euo pipefail
repo_root=${1:-$(git rev-parse --show-toplevel)}
role="$repo_root/deploy/ansible/roles/alertmanager"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export PROMTOOL="${PROMTOOL:-$(command -v promtool)}"
test -x "$PROMTOOL"
alertmanager=${ALERTMANAGER:-}
if [[ -z "$alertmanager" ]]; then
  version=$(awk -F'"' '/^am_version:/ {print $2}' "$role/defaults/main.yml")
  sha=$(awk -F'"' '/^am_release_sha256:/ {print $2}' "$role/defaults/main.yml")
  tarball="alertmanager-${version}.linux-amd64.tar.gz"
  curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "$work/$tarball" "https://github.com/prometheus/alertmanager/releases/download/v${version}/${tarball}"
  printf '%s  %s\n' "$sha" "$work/$tarball" | sha256sum --check --strict >/dev/null
  tar -xzf "$work/$tarball" -C "$work" "alertmanager-${version}.linux-amd64/alertmanager"
  alertmanager="$work/alertmanager-${version}.linux-amd64/alertmanager"
fi
python3 "$repo_root/scripts/ops/notification_receiver_metrics_test.py"
python3 "$repo_root/scripts/ops/notification_drill_test.py"
python3 "$repo_root/scripts/ops/notification_receiver_config_test.py"
python3 "$repo_root/scripts/ops/notification_receiver_rules_test.py"
"$PROMTOOL" test rules "$repo_root/pkg/promqlrules/testdata/notification_receivers.test.yml"
python3 "$repo_root/scripts/ops/notification_drill.py" \
  --alertmanager "$alertmanager" \
  --prometheus "${PROMETHEUS:-$(command -v prometheus)}" \
  --output "${NOTIFICATION_DRILL_REPORT:-$work/notification-drill.json}"
echo "notification delivery: local fire, recovery, receiver failure and missed-heartbeat checks passed"
