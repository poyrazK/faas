#!/usr/bin/env bash
# Fixed privileged surface for a dedicated financial acceptance peer. The
# controller keeps `hold` alive with heartbeats; losing it restores the host.
set -Eeuo pipefail
die() { echo "financial pair host: $*" >&2; exit 1; }
[[ "${EUID}" -eq 0 ]] || die "requires root"
[[ -f /etc/faas/financial-pair-acceptance-host ]] || die "dedicated pair marker missing"
[[ "$(uname -m)" == x86_64 && -c /dev/kvm ]] || die "requires native x86_64 KVM"
action="${1:?action required}"
bundle="${2:?bundle required}"
[[ "${bundle}" =~ ^/var/tmp/faas-financial-pair-[a-f0-9]{40}-[A-Za-z0-9._-]+$ ]] || die "invalid bundle"
[[ "$(stat -c '%a %U' "${bundle}/peer.env")" == '600 root' ]] || die "peer.env must be root-owned 0600"
# Created by the exact-source controller, never supplied by a test request.
# shellcheck disable=SC1090
source "${bundle}/peer.env"
[[ "${FAAS_FINANCIAL_PAIR_BUNDLE}" == "${bundle}" ]] || die "bundle identity changed"
[[ "${FAAS_FINANCIAL_PAIR_JAIL_ROOT}" =~ ^/srv/f[a-f0-9]{6}$ ]] || die "invalid jail root"
unit_prefix="faas-financial-${FAAS_FINANCIAL_PAIR_RUN_ID}-"
[[ "${FAAS_FINANCIAL_PAIR_RUN_ID}" =~ ^[A-Za-z0-9._-]{1,32}$ ]] || die "invalid run ID"
export FAAS_E2E_JAIL_ROOT="${FAAS_FINANCIAL_PAIR_JAIL_ROOT}"

case "${action}" in
  start|kill)
    instance="${3:?instance required}"
    [[ "${instance}" =~ ^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$ ]] || die "invalid instance"
    [[ -f "${bundle}/ready.json" && ! -f "${bundle}/cleanup.json" ]] || die "pair hold is not active"
    unit="${unit_prefix}${instance}"
    if [[ "${action}" == kill ]]; then
      systemctl kill --kill-who=main --signal=SIGKILL "${unit}"
      exit
    fi
    # Main-only SIGKILL exercises native orphan recovery. The guest has its
    # own jail/cgroup; stopping the complete unit would hide that failure.
    exec systemd-run --quiet --pipe --wait --collect --unit="${unit}" \
      --property=RuntimeMaxSec=9m --property=KillMode=process \
      --working-directory="${bundle}" \
      /usr/bin/env FAAS_FINANCIAL_NATIVE_OWNER_CHILD=1 \
      bash -c 'source "$1/peer.env"; exec "$1/fcvm.test" -test.run="^TestFinancialComputeNativeOwnerProcess$" -test.timeout=9m' _ "${bundle}"
    ;;
  remove)
    owned="${3:?owned path required}"
    [[ "${owned}" != *..* ]] || die "invalid owned path"
    case "${owned}" in
      "${bundle}"/owned/cost-*|"${FAAS_FINANCIAL_PAIR_JAIL_ROOT}"/c????) rm -rf -- "${owned}" ;;
      *) die "cleanup path is outside this fixture" ;;
    esac
    exit
    ;;
  retire)
    # Only clean, completed fixtures may discard their recovery evidence.
    python3 - "${bundle}/cleanup.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as stream:
    record = json.load(stream)
checks = ("runner_exit", "leakcheck_exit", "post_repair_exit", "restore_exit")
if any(type(record.get(name)) is not int or record[name] != 0 for name in checks):
    raise SystemExit("refusing to discard failed native recovery evidence")
PY
    rm -rf -- "${bundle}"
    exit
    ;;
  network)
    [[ -f "${bundle}/ready.json" && ! -f "${bundle}/cleanup.json" ]] || die "pair hold is not active"
    exec "${bundle}/fcvm.test" -test.v -test.timeout=3m \
      -test.run='^TestMetalFinancialEgress(Packet|Offload)Credits$'
    ;;
  hold) ;;
  *) die "unsupported action" ;;
esac

for tool in firecracker jailer ethtool ip tc nft python3 systemctl flock; do
  command -v "${tool}" >/dev/null || die "missing ${tool}"
done
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 60 9 || die "another native gate owns this host"
[[ ! -e "${bundle}/ready.json" && ! -e "${bundle}/cleanup.json" ]] || die "run ID was already used"
bash "${bundle}/deploy/scripts/leakcheck.sh" > "${bundle}/baseline.log" 2>&1 || die "host baseline is dirty"
mkdir -p "${bundle}/owned" "${bundle}/tmp"
# A short hash collision must refuse, never reuse retained ownership evidence.
mkdir -m 0700 "${FAAS_FINANCIAL_PAIR_JAIL_ROOT}" || die "jail namespace already exists; retain it for repair"
active="${bundle}/active-services"
: > "${active}"

cleanup() {
  local rc=$? restore_rc=0 preleak=0 postleak=0 unit jail
  trap - EXIT HUP INT TERM
  set +e
  # A still-active owner is a failed test cleanup, even when emergency
  # retirement below succeeds. Never convert repaired leaks into acceptance.
  while IFS= read -r unit; do
    [[ -n "${unit}" ]] || continue
    rc=1
    systemctl kill --kill-who=all --signal=SIGKILL "${unit}"
  done < <(systemctl list-units --state=running,activating --no-legend --plain "${unit_prefix}*.service" | awk '{print $1}')
  bash "${bundle}/deploy/scripts/leakcheck.sh" > "${bundle}/leakcheck.log" 2>&1
  preleak=$?
  if [[ "${preleak}" -ne 0 ]]; then
    # The pre-repair failure remains in cleanup.json. Both hosts are dedicated
    # and locked; emergency reaping cannot affect customer workloads.
    source "${bundle}/native-e2e-reap.sh"
    reap_test_microvms >> "${bundle}/leakcheck.log" 2>&1
    for jail in "${FAAS_FINANCIAL_PAIR_JAIL_ROOT}"/c????/n?; do
      [[ -d "${jail}" ]] || continue
      FAAS_E2E_JAIL_ROOT="${jail}" reap_test_microvms >> "${bundle}/leakcheck.log" 2>&1
    done
  fi
  bash "${bundle}/deploy/scripts/leakcheck.sh" >> "${bundle}/leakcheck.log" 2>&1
  postleak=$?
  while IFS= read -r service; do
    [[ -n "${service}" ]] || continue
    systemctl start "${service}" || restore_rc=1
    systemctl is-active --quiet "${service}" || restore_rc=1
  done < "${active}"
  python3 "${bundle}/financial-pair-evidence.py" cleanup --bundle "${bundle}" \
    --runner-exit "${rc}" --leakcheck-exit "${preleak}" --post-repair-exit "${postleak}" --restore-exit "${restore_rc}"
  [[ "${preleak}" -eq 0 && "${postleak}" -eq 0 && "${restore_rc}" -eq 0 ]] || rc=1
  # Retain journals/images after a failure for recovery investigation.
  if [[ "${rc}" -eq 0 ]]; then rm -rf -- "${bundle}/owned" "${bundle}/tmp" "${FAAS_FINANCIAL_PAIR_JAIL_ROOT}"; fi
  exit "${rc}"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
for service in faas-apid faas-schedd faas-vmmd faas-builderd faas-imaged \
  faas-gatewayd-internal faas-gatewayd-public faas-meterd faas-realtimed faas-outboundd; do
  if systemctl is-active --quiet "${service}"; then
    printf '%s\n' "${service}" >> "${active}"
    systemctl stop "${service}"
  fi
done
python3 "${bundle}/financial-pair-evidence.py" host --bundle "${bundle}"
while IFS= read -r -t 45 heartbeat; do
  case "${heartbeat}" in
    alive) ;;
    finish) exit 0 ;;
    *) die "invalid controller heartbeat" ;;
  esac
done
die "controller disconnected or heartbeat expired"
