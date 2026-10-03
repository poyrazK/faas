#!/usr/bin/env bash
# Dedicated three-worker regional qualification. This stops vmmd on A/PEER.
set -Eeuo pipefail
die() { echo "regional acceptance: $*" >&2; exit 1; }
[[ "$(uname -sm)" == 'Linux x86_64' ]] || die 'controller must run Linux x86_64'
[[ "${FAAS_REGION_ACCEPTANCE_CONFIRM:-}" == native-x86 ]] || die 'set FAAS_REGION_ACCEPTANCE_CONFIRM=native-x86 for the dedicated fleet'
required_vars=(DATABASE_URL FAAS_REGION_API_URL FAAS_REGION_API_TOKEN
  FAAS_REGION_NODE_A FAAS_REGION_NODE_PEER FAAS_REGION_NODE_B
  FAAS_REGION_SSH_A FAAS_REGION_SSH_PEER FAAS_REGION_SSH_B
  FAAS_REGION_IMAGE FAAS_REGION_JOB_IMAGE FAAS_REGION_SOURCE_TARBALL
  FAAS_REGION_WORKER_REPO FAAS_REGION_VMMD_METRICS_URL_B)
for key in "${required_vars[@]}"; do [[ -n "${!key:-}" ]] || die "${key} is required"; done
ssh_target_pattern='^[][a-zA-Z0-9_.@:-]+$'
for key in FAAS_REGION_SSH_A FAAS_REGION_SSH_PEER FAAS_REGION_SSH_B; do
  [[ "${!key}" != -* && "${!key}" =~ ${ssh_target_pattern} ]] || die "invalid ${key}"
done
for key in FAAS_REGION_IMAGE FAAS_REGION_JOB_IMAGE; do
  [[ "${!key}" =~ @sha256:[a-f0-9]{64}$ ]] || die "${key} must be digest-pinned"
done
for tool in go make ssh tee git; do command -v "${tool}" >/dev/null || die "missing ${tool}"; done
[[ "$(go version)" == 'go version go1.25.13 linux/amd64' ]] || die 'requires Go 1.25.13 linux/amd64'
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
source scripts/ci/native-regional-verdict.sh
ssh_opts=(-o BatchMode=yes -o ConnectTimeout=10)
restore_required=0
finish() {
  local rc=$?
  trap - EXIT
  if [[ "${restore_required}" == 1 ]]; then
    for target in "${FAAS_REGION_SSH_A}" "${FAAS_REGION_SSH_PEER}"; do
      if ! ssh "${ssh_opts[@]}" "${target}" sudo -n systemctl start faas-vmmd faas-builderd faas-imaged; then
        echo 'regional acceptance: could not restore designated compute services' >&2
        rc=1
      fi
    done
  fi
  exit "${rc}"
}
trap finish EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
for target in "${FAAS_REGION_SSH_A}" "${FAAS_REGION_SSH_PEER}" "${FAAS_REGION_SSH_B}"; do
  ssh "${ssh_opts[@]}" "${target}" sudo -n test -f /etc/faas/regional-acceptance-host || die 'all workers must be designated for acceptance'
  ssh "${ssh_opts[@]}" "${target}" sudo -n systemctl is-active --quiet faas-vmmd faas-schedd || die 'all workers need active schedd and vmmd'
done
for target in "${FAAS_REGION_SSH_A}" "${FAAS_REGION_SSH_PEER}"; do
  ssh "${ssh_opts[@]}" "${target}" sudo -n systemctl is-active --quiet faas-builderd faas-imaged || die 'regional peers need active builderd and imaged'
done
evidence="$(mktemp -d "${TMPDIR:-/tmp}/gregale-native-region.XXXXXX")"
chmod 700 "${evidence}"
git rev-parse HEAD > "${evidence}/candidate-commit"
echo "regional acceptance: evidence directory ${evidence}"
restore_required=1
RUN_REGEX='^TestNative(ComputeRegion|RegionalBuildExport|RegionalJobs)Acceptance$' \
  make test-metal PKGS=./cmd/e2e RUN_ARGS='-timeout=90m -v' 2>&1 | tee "${evidence}/test-metal.log"
native_regional_verdict "${evidence}/test-metal.log"
echo 'regional acceptance: required app, build handoff and Jobs drills passed'
