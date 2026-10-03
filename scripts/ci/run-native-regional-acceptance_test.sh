#!/usr/bin/env bash
set -Eeuo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${repo_root}/scripts/ci/native-regional-verdict.sh"
stage="$(mktemp -d)"
trap 'rm -rf "${stage}"' EXIT
mkdir "${stage}/bin" "${stage}/tmp"
export REGIONAL_RUNNER_TEST_LOG="${stage}/commands.log"
cat > "${stage}/bin/uname" <<'SH'
#!/usr/bin/env bash
echo 'Linux x86_64'
SH
cat > "${stage}/bin/go" <<'SH'
#!/usr/bin/env bash
echo 'go version go1.25.13 linux/amd64'
SH
cat > "${stage}/bin/ssh" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${REGIONAL_RUNNER_TEST_LOG}"
if [[ "${REGIONAL_RUNNER_TEST_FAIL_PREFLIGHT:-}" == 1 && "$*" == *'test -f'* ]]; then exit 1; fi
SH
cat > "${stage}/bin/make" <<'SH'
#!/usr/bin/env bash
[[ "$*" == 'test-metal PKGS=./cmd/e2e RUN_ARGS=-timeout=90m -v' ]]
[[ "${RUN_REGEX}" == '^TestNative(ComputeRegion|RegionalBuildExport|RegionalJobs)Acceptance$' ]]
if [[ "${REGIONAL_RUNNER_TEST_FAIL_TESTS:-}" == 1 ]]; then exit 1; fi
cat "${REGIONAL_RUNNER_TEST_PASSES}"
SH
chmod +x "${stage}/bin/"*
export PATH="${stage}/bin:${PATH}" TMPDIR="${stage}/tmp"
export DATABASE_URL=postgres://fixture FAAS_REGION_API_URL=https://fixture.invalid FAAS_REGION_API_TOKEN=fixture
export FAAS_REGION_NODE_A=worker-a FAAS_REGION_NODE_PEER=worker-peer FAAS_REGION_NODE_B=worker-b
export FAAS_REGION_SSH_A=worker-a FAAS_REGION_SSH_PEER=worker-peer FAAS_REGION_SSH_B=worker-b
export FAAS_REGION_IMAGE=fixture/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export FAAS_REGION_JOB_IMAGE="${FAAS_REGION_IMAGE}" FAAS_REGION_SOURCE_TARBALL="${stage}/fixture.tar.gz"
export FAAS_REGION_WORKER_REPO=/srv/gregale FAAS_REGION_VMMD_METRICS_URL_B=http://127.0.0.1:9104/metrics
export FAAS_REGION_ACCEPTANCE_CONFIRM=native-x86 REGIONAL_RUNNER_TEST_PASSES="${stage}/passes.log"
for test in "${NATIVE_REGIONAL_REQUIRED_TESTS[@]}"; do printf '%s\n' "--- PASS: ${test} (1.00s)" >> "${REGIONAL_RUNNER_TEST_PASSES}"; done
runner="${repo_root}/scripts/ci/run-native-regional-acceptance.sh"
bash "${runner}" > "${stage}/success.log" 2>&1
[[ "$(grep -c 'systemctl start faas-vmmd faas-builderd faas-imaged$' "${REGIONAL_RUNNER_TEST_LOG}")" == 2 ]]
: > "${REGIONAL_RUNNER_TEST_LOG}"
if REGIONAL_RUNNER_TEST_FAIL_PREFLIGHT=1 bash "${runner}" > "${stage}/preflight.log" 2>&1; then echo 'runner accepted failed preflight' >&2; exit 1; fi
if grep -Eq 'systemctl (start|stop)' "${REGIONAL_RUNNER_TEST_LOG}"; then echo 'runner mutated services after failed preflight' >&2; exit 1; fi
: > "${REGIONAL_RUNNER_TEST_LOG}"
if REGIONAL_RUNNER_TEST_FAIL_TESTS=1 bash "${runner}" > "${stage}/failed.log" 2>&1; then echo 'runner accepted failed tests' >&2; exit 1; fi
[[ "$(grep -c 'systemctl start faas-vmmd faas-builderd faas-imaged$' "${REGIONAL_RUNNER_TEST_LOG}")" == 2 ]]
: > "${REGIONAL_RUNNER_TEST_LOG}"
if FAAS_REGION_SSH_A='worker;false' bash "${runner}" > "${stage}/unsafe.log" 2>&1; then echo 'runner accepted an unsafe SSH target' >&2; exit 1; fi
[[ ! -s "${REGIONAL_RUNNER_TEST_LOG}" ]]
echo 'native regional runner contracts OK'
