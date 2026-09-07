#!/usr/bin/env bash
# Keep a designated native-acceptance host out of customer placement while
# its production daemons are stopped. The state file records whether the node
# was active before the test so cleanup can restore the exact operator state.

set -euo pipefail

die() {
  echo "native builder maintenance: $*" >&2
  exit 1
}

action="${1:-}"
state_file="${2:-}"
services_ready="${3:-false}"
node_name="${FAAS_ACCEPTANCE_NODE_NAME:-}"
gregalectl="${FAAS_ACCEPTANCE_GREGALECTL:-/usr/local/bin/gregalectl}"
db_env="${FAAS_ACCEPTANCE_DB_ENV:-/etc/faas/compute-db.env}"

[[ "${action}" == "enter" || "${action}" == "restore" ]] ||
  die "usage: $0 enter|restore STATE_FILE [SERVICES_READY]"
[[ -n "${state_file}" ]] || die "state file path is required"
[[ -n "${node_name}" ]] || die "FAAS_ACCEPTANCE_NODE_NAME is required"
[[ -x "${gregalectl}" ]] || die "gregalectl is not executable: ${gregalectl}"
[[ -r "${db_env}" ]] || die "database environment is not readable: ${db_env}"

# The deployment owns this root-readable EnvironmentFile. Export its values for
# gregalectl without printing the DSN into the acceptance log.
set -a
# shellcheck disable=SC1090
source "${db_env}"
set +a

node_active() {
  local output active
  output="$("${gregalectl}" compute-nodes show --node "${node_name}")" ||
    die "cannot read compute-node state for ${node_name}"
  active="$(sed -n 's/^active=//p' <<<"${output}")"
  case "${active}" in
    true|false) printf '%s\n' "${active}" ;;
    *) die "compute-node state for ${node_name} did not contain active=true|false" ;;
  esac
}

drain_node() {
  "${gregalectl}" compute-nodes drain --node "${node_name}"
}

case "${action}" in
  enter)
    active_before="$(node_active)"
    umask 077
    printf 'active_before=%s\n' "${active_before}" > "${state_file}"
    drain_node

    attempts="${FAAS_ACCEPTANCE_DRAIN_ATTEMPTS:-90}"
    interval="${FAAS_ACCEPTANCE_DRAIN_INTERVAL_SECONDS:-2}"
    [[ "${attempts}" =~ ^[1-9][0-9]*$ ]] || die "FAAS_ACCEPTANCE_DRAIN_ATTEMPTS must be positive"
    [[ "${interval}" =~ ^[0-9]+$ ]] || die "FAAS_ACCEPTANCE_DRAIN_INTERVAL_SECONDS must be non-negative"
    for ((attempt = 1; attempt <= attempts; attempt++)); do
      if "${gregalectl}" compute-nodes drain-status --node "${node_name}"; then
        echo "native builder maintenance: ${node_name} is drained and has no live instances"
        exit 0
      fi
      if (( attempt < attempts )); then
        sleep "${interval}"
      fi
    done
    die "${node_name} still has live instances after $((attempts * interval)) seconds"
    ;;
  restore)
    [[ -r "${state_file}" ]] || die "maintenance state file is unreadable: ${state_file}"
    active_before="$(sed -n 's/^active_before=//p' "${state_file}")"
    case "${active_before}" in
      true)
        if [[ "${services_ready}" == "true" ]]; then
          "${gregalectl}" compute-nodes activate --node "${node_name}"
          echo "native builder maintenance: restored ${node_name} to active"
        else
          drain_node
          echo "native builder maintenance: kept ${node_name} drained because service restoration failed" >&2
          exit 1
        fi
        ;;
      false)
        drain_node
        echo "native builder maintenance: restored ${node_name} to its prior drained state"
        ;;
      *) die "maintenance state file has invalid active_before value" ;;
    esac
    ;;
esac
