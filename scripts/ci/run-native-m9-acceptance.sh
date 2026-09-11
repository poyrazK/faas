#!/usr/bin/env bash
# Run the M9 heartbeat/failure-safe acceptance against the Ansible-managed
# native x86 compute pair. This replaces the retired Lima two-node target.
# The control-plane checkout runs the Go test; the test stops the designated
# compute node's vmmd over SSH and verifies its owning schedd reconciles the
# failure. The control-plane host is the database/test runner and is not one
# of the two fault-injection peers.

set -Eeuo pipefail

die() {
  echo "native M9 acceptance: $*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die "must run as root"
[[ "$(uname -m)" == "x86_64" ]] || die "the native M9 gate requires x86_64"
[[ -f /etc/faas/m9-acceptance-host ]] ||
  die "/etc/faas/m9-acceptance-host is missing; this node is not designated for the native M9 gate"

: "${DATABASE_URL:?set DATABASE_URL to the production-shaped Postgres DSN}"
: "${FAAS_TWO_NODE_NODE_A:?set FAAS_TWO_NODE_NODE_A (a compute node, for example fsn-2.faas)}"
: "${FAAS_TWO_NODE_NODE_B:?set FAAS_TWO_NODE_NODE_B (a second compute node, for example fsn-3.faas)}"
: "${FAAS_TWO_NODE_SSH_A:?set FAAS_TWO_NODE_SSH_A (SSH target for compute node A)}"
: "${FAAS_TWO_NODE_SSH_B:?set FAAS_TWO_NODE_SSH_B (SSH target for compute node B)}"
: "${FAAS_TWO_NODE_ADDR_A:?set FAAS_TWO_NODE_ADDR_A (compute node A IPv4 address)}"
: "${FAAS_TWO_NODE_ADDR_B:?set FAAS_TWO_NODE_ADDR_B (compute node B IPv4 address)}"
: "${FAAS_M9_CONFIRM:?set FAAS_M9_CONFIRM=native-x86 to enable the fault drill}"
[[ "${FAAS_M9_CONFIRM}" == "native-x86" ]] ||
  die "refusing to run a fault drill without FAAS_M9_CONFIRM=native-x86"

for value_name in FAAS_TWO_NODE_NODE_A FAAS_TWO_NODE_NODE_B FAAS_TWO_NODE_SSH_A \
  FAAS_TWO_NODE_SSH_B FAAS_TWO_NODE_ADDR_A FAAS_TWO_NODE_ADDR_B; do
  value="${!value_name}"
  [[ "${value}" != *[[:space:]]* && "${value}" != -* ]] ||
    die "${value_name} contains unsupported whitespace or a leading dash"
done

for tool in bash go ssh systemctl; do
  command -v "${tool}" >/dev/null || die "required host tool is missing: ${tool}"
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

ssh_opts=(-o BatchMode=yes -o ConnectTimeout=10)
vmmd_was_active=0
remote_systemctl() {
  local target="$1"
  shift
  # Arguments are intentionally expanded locally and passed as argv to SSH;
  # no remote shell interpolation is needed for these fixed systemctl calls.
  # shellcheck disable=SC2029
  ssh "${ssh_opts[@]}" "${target}" sudo systemctl "$@"
}

restore_remote_services() {
  set +e
  remote_systemctl "${FAAS_TWO_NODE_SSH_A}" kill --kill-who=main --signal=SIGCONT faas-schedd >/dev/null 2>&1
  remote_systemctl "${FAAS_TWO_NODE_SSH_B}" kill --kill-who=main --signal=SIGCONT faas-schedd >/dev/null 2>&1
  if [[ "${vmmd_was_active}" -eq 1 ]]; then
    remote_systemctl "${FAAS_TWO_NODE_SSH_B}" start faas-vmmd >/dev/null 2>&1
  fi
}
trap restore_remote_services EXIT HUP INT TERM

echo "native M9 acceptance: checking native x86 compute-pair services"
remote_systemctl "${FAAS_TWO_NODE_SSH_A}" is-active --quiet faas-schedd faas-vmmd ||
  die "node A schedd/vmmd services are not active; native M9 requires per-node ownership"
remote_systemctl "${FAAS_TWO_NODE_SSH_B}" is-active --quiet faas-schedd faas-vmmd ||
  die "node B schedd/vmmd services are not active; native M9 requires per-node ownership"
vmmd_was_active=1

echo "native M9 acceptance: checking for leaked host resources before the drill"
bash deploy/scripts/leakcheck.sh

echo "native M9 acceptance: running heartbeat-gap reconciliation against ${FAAS_TWO_NODE_NODE_B}"
FAAS_TWO_NODE_REMOTE=1 \
FAAS_TWO_NODE_NODE_A="${FAAS_TWO_NODE_NODE_A}" \
FAAS_TWO_NODE_NODE_B="${FAAS_TWO_NODE_NODE_B}" \
FAAS_TWO_NODE_SSH_A="${FAAS_TWO_NODE_SSH_A}" \
FAAS_TWO_NODE_SSH_B="${FAAS_TWO_NODE_SSH_B}" \
FAAS_TWO_NODE_ADDR_A="${FAAS_TWO_NODE_ADDR_A}" \
FAAS_TWO_NODE_ADDR_B="${FAAS_TWO_NODE_ADDR_B}" \
DATABASE_URL="${DATABASE_URL}" \
go test -tags=metal ./cmd/e2e \
  -run '^TestTwoNode_HeartbeatGapFlipsLifecycleUnavailable$' \
  -count=1 -timeout="${FAAS_M9_TEST_TIMEOUT:-5m}"

echo "native M9 acceptance: checking for leaked host resources after the drill"
bash deploy/scripts/leakcheck.sh
echo "native M9 acceptance: PASS; remote vmmd restored"
