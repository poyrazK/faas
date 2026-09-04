#!/usr/bin/env bash
# update-grype-db.sh — retry the vulnerability database fetch on CI egress
# blips without hiding a genuine database failure.

set -euo pipefail

grype_bin=${GRYPE_BIN:-grype}
attempts=${GRYPE_DB_ATTEMPTS:-3}

for attempt in $(seq 1 "${attempts}"); do
  if "${grype_bin}" db update; then
    echo "Grype database updated on attempt ${attempt}"
    exit 0
  fi
  if [[ "${attempt}" == "${attempts}" ]]; then
    echo "Grype database update failed after ${attempts} attempts" >&2
    exit 1
  fi
  sleep $((attempt * 5))
done
