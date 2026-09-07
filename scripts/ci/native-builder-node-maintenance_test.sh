#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

fake_ctl="${test_root}/gregalectl"
cat > "${fake_ctl}" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >> "${FAKE_LOG}"
case "$1 $2" in
  "compute-nodes show")
    printf 'name=%s\nactive=%s\nlive_instance_count=0\n' "${FAAS_ACCEPTANCE_NODE_NAME}" "$(cat "${FAKE_ACTIVE}")"
    ;;
  "compute-nodes drain")
    printf 'false\n' > "${FAKE_ACTIVE}"
    ;;
  "compute-nodes drain-status")
    remaining="$(cat "${FAKE_DRAIN_REMAINING}")"
    if (( remaining > 0 )); then
      printf '%s\n' "$((remaining - 1))" > "${FAKE_DRAIN_REMAINING}"
      exit 1
    fi
    ;;
  "compute-nodes activate")
    printf 'true\n' > "${FAKE_ACTIVE}"
    ;;
  *)
    echo "unexpected fake gregalectl command: $*" >&2
    exit 2
    ;;
esac
FAKE
chmod 0755 "${fake_ctl}"

db_env="${test_root}/compute-db.env"
printf 'FAAS_PG_DSN=postgres://test.invalid/faas\n' > "${db_env}"

export FAAS_ACCEPTANCE_NODE_NAME=fsn-3.faas
export FAAS_ACCEPTANCE_GREGALECTL="${fake_ctl}"
export FAAS_ACCEPTANCE_DB_ENV="${db_env}"
export FAAS_ACCEPTANCE_DRAIN_ATTEMPTS=3
export FAAS_ACCEPTANCE_DRAIN_INTERVAL_SECONDS=0
export FAKE_LOG="${test_root}/commands.log"
export FAKE_ACTIVE="${test_root}/active"
export FAKE_DRAIN_REMAINING="${test_root}/drain-remaining"

maintenance="${repo_root}/scripts/ci/native-builder-node-maintenance.sh"
state_file="${test_root}/maintenance.state"

# An active node is drained before the test, waits for its live instances, and
# is reactivated only after every production service has been restored.
printf 'true\n' > "${FAKE_ACTIVE}"
printf '1\n' > "${FAKE_DRAIN_REMAINING}"
: > "${FAKE_LOG}"
bash "${maintenance}" enter "${state_file}"
[[ "$(cat "${FAKE_ACTIVE}")" == "false" ]]
grep -qx 'active_before=true' "${state_file}"
[[ "$(grep -c '^compute-nodes drain-status ' "${FAKE_LOG}")" -eq 2 ]]
bash "${maintenance}" restore "${state_file}" true
[[ "$(cat "${FAKE_ACTIVE}")" == "true" ]]
grep -q '^compute-nodes activate ' "${FAKE_LOG}"

# A node that was already drained stays drained after a successful test.
printf 'false\n' > "${FAKE_ACTIVE}"
printf '0\n' > "${FAKE_DRAIN_REMAINING}"
: > "${FAKE_LOG}"
bash "${maintenance}" enter "${state_file}"
bash "${maintenance}" restore "${state_file}" true
[[ "$(cat "${FAKE_ACTIVE}")" == "false" ]]
if grep -q '^compute-nodes activate ' "${FAKE_LOG}"; then
  echo "restore unexpectedly activated a node that was already drained" >&2
  exit 1
fi

# Failed service restoration must leave a previously-active node drained.
printf 'true\n' > "${FAKE_ACTIVE}"
printf '0\n' > "${FAKE_DRAIN_REMAINING}"
bash "${maintenance}" enter "${state_file}"
if bash "${maintenance}" restore "${state_file}" false; then
  echo "restore unexpectedly activated a node with failed services" >&2
  exit 1
fi
[[ "$(cat "${FAKE_ACTIVE}")" == "false" ]]

# A node that never becomes drain-safe fails closed.
printf 'true\n' > "${FAKE_ACTIVE}"
printf '9\n' > "${FAKE_DRAIN_REMAINING}"
if bash "${maintenance}" enter "${state_file}"; then
  echo "enter unexpectedly accepted a node with live instances" >&2
  exit 1
fi
[[ "$(cat "${FAKE_ACTIVE}")" == "false" ]]

echo "native builder maintenance tests: PASS"
