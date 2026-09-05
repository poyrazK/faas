#!/usr/bin/env bash
set -euo pipefail

repo_root=${1:-$(git rev-parse --show-toplevel)}
tmp_file=$(mktemp)
trap 'rm -f "$tmp_file"' EXIT

rules_files=(
  "$repo_root/deploy/ansible/roles/prometheus/files/faas.rules.yml"
  "$repo_root/deploy/ansible/roles/prometheus/files/bridge.rules.yml"
  "$repo_root/deploy/ansible/roles/prometheus/files/pg_backup.rules.yml"
  "$repo_root/pkg/promqlrules/data_placement.yaml"
)

status=0
for rules_file in "${rules_files[@]}"; do
  if [[ ! -f "$rules_file" ]]; then
    echo "alert metadata: missing rule file: $rules_file" >&2
    status=1
    continue
  fi

  # Emit alert|runbook-path records after checking the two labels that make
  # an alert actionable in Alertmanager and in the operator cookbook. The
  # parser deliberately stays indentation-aware rather than depending on a
  # third-party YAML runtime in CI.
  awk -v source="$rules_file" '
    function finish(    path) {
      if (alert == "") return
      if (!family) {
        print source ":" alert ": missing labels.family" > "/dev/stderr"
        failed = 1
      }
      if (!runbook) {
        print source ":" alert ": missing annotations.runbook_url" > "/dev/stderr"
        failed = 1
      } else {
        path = runbook
        sub(/^.*\/blob\/main\//, "", path)
        print alert "|" path
      }
    }
    FNR == 1 {
      finish()
      alert = ""
      family = 0
      runbook = ""
    }
    /^[[:space:]]+- alert:/ {
      finish()
      alert = $0
      sub(/^.*- alert: /, "", alert)
      family = 0
      runbook = ""
      next
    }
    alert != "" && /^[[:space:]]+family:/ { family = 1 }
    alert != "" && /runbook_url:/ {
      runbook = $0
      sub(/^.*runbook_url:[[:space:]]*"/, "", runbook)
      sub(/".*$/, "", runbook)
    }
    END {
      finish()
      if (failed) exit 1
    }
  ' "$rules_file" >> "$tmp_file" || status=1
done

if (( status != 0 )); then
  exit "$status"
fi

while IFS='|' read -r alert_name runbook_path; do
  [[ -n "$alert_name" ]] || continue
  runbook_file="$repo_root/$runbook_path"
  if [[ ! -f "$runbook_file" ]]; then
    echo "alert metadata: $alert_name references missing runbook: $runbook_path" >&2
    status=1
  fi
done < "$tmp_file"

if (( status != 0 )); then
  exit "$status"
fi

echo "alert metadata: all alerts have family labels and existing runbooks"
