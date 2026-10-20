#!/usr/bin/env bash
# Read-only aggregate over independently uploaded, commit-bound job evidence.
native_e2e_combined_verdict() {
  local root="${1:?repository required}" evidence="${2:?evidence required}" sha="${3:?source SHA required}"
  local result="${4:?job result required}" lane="${5:-full}" group directory combined rc
  local service_graph="${6:-false}" coordinated="${7:-false}"
  [[ "$coordinated" == false || "$coordinated" == true && "$service_graph" == true && "$lane" == project-environment-promotion-only ]] || { echo 'native combined verdict: invalid coordinated qualification selection' >&2; return 1; }
  [[ "$service_graph" == false || "$service_graph" == true && "$lane" == project-environment-promotion-only ]] || { echo 'native combined verdict: invalid service-graph qualification selection' >&2; return 1; }
  [[ "$result" == success && "$sha" =~ ^[a-f0-9]{40}$ ]] || { echo 'native combined verdict: jobs failed/cancelled or source SHA invalid' >&2; return 1; }
  local groups=(single)
  if [[ "$lane" == full || "$lane" == qualify ]]; then groups=(source runtime); fi
  if [[ "$lane" == project-environment-promotion-only ]]; then groups=(promotion-core promotion-controls promotion-load promotion-admission promotion-migrations promotion-publication); fi
  for group in "${groups[@]}"; do
    directory="$evidence/native-e2e-$sha-$group"
    [[ -s "$directory/native-e2e.log" ]] || { echo "native combined verdict: $group log missing" >&2; return 1; }
    grep -Fxq "sha=$sha" "$directory/native-group.txt" || return 1
    grep -Fxq "group=$group" "$directory/native-group.txt" || return 1
  done
  if [[ "${groups[0]}" == single ]]; then
    if [[ "$lane" == project-progressive-http-only || "$lane" == project-concurrent-release-only || "$lane" == project-rollback-retention-only || "$lane" == project-release-secrets-only ]]; then
      python3 "$root/scripts/ci/project-http-qualification.py" \
        --scope "$lane" --log "$directory/native-e2e.log" --sha "$sha" \
        --output "$directory/$lane-qualification.json" || return 1
    fi
    return 0
  fi
  combined="$(mktemp)" || return 1
  for group in "${groups[@]}"; do
    cat "$evidence/native-e2e-$sha-$group/native-e2e.log" >> "$combined" || { rm -f "$combined"; return 1; }
  done
  # shellcheck source=scripts/ci/native-e2e-verdict.sh
  source "$root/scripts/ci/native-e2e-verdict.sh"
  rc=0
  if [[ "$lane" == project-environment-promotion-only ]]; then
    python3 "$root/scripts/ci/promotion-qualification.py" --log "$combined" --sha "$sha" --output "$evidence/promotion-qualification.json" || rc=$?
  else
    native_e2e_verdict "$combined" || rc=$?
  fi
  rm -f "$combined"
  if [[ "$lane" == project-environment-promotion-only && "$service_graph" == true ]]; then
    directory="$evidence/native-e2e-$sha-promotion-service-graph"
    [[ -s "$directory/native-e2e.log" ]] || return 1
    grep -Fxq "sha=$sha" "$directory/native-group.txt" || return 1
    grep -Fxq 'group=promotion-service-graph' "$directory/native-group.txt" || return 1
    python3 "$root/scripts/ci/promotion-service-graph-qualification.py" --log "$directory/native-e2e.log" --sha "$sha" --output "$evidence/service-graph-qualification.json" || rc=$?
  fi
  if [[ "$lane" == project-environment-promotion-only && "$coordinated" == true ]]; then
    directory="$evidence/native-e2e-$sha-promotion-coordinated"
    [[ -s "$directory/native-e2e.log" ]] || return 1
    grep -Fxq "sha=$sha" "$directory/native-group.txt" || return 1
    grep -Fxq 'group=promotion-coordinated' "$directory/native-group.txt" || return 1
    python3 "$root/scripts/ci/promotion-coordinated-qualification.py" --log "$directory/native-e2e.log" --sha "$sha" --output "$evidence/coordinated-qualification.json" || rc=$?
  fi
  return "$rc"
}
