#!/usr/bin/env bash
# Dedicated-host CI qualification. Never edits fleet flags, IAM or bucket policy.
set -Eeuo pipefail
umask 077

die() { echo "native CI acceptance: $*" >&2; exit 1; }
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[[ "${GREGALE_CI_NATIVE_ACCEPTANCE:-}" == 1 ]] || die "set GREGALE_CI_NATIVE_ACCEPTANCE=1 explicitly"
[[ "${EUID}" -eq 0 && "$(uname -m)" == x86_64 ]] || die "requires root on native Linux x86_64"
[[ -c /dev/kvm ]] || die "dedicated native KVM is unavailable"
[[ "$(uname -s)" == Linux ]] || die "requires native Linux"
[[ -z "${FAAS_SKIP_PG_TESTS:-}" && -z "${STORAGE_EMULATOR_HOST:-}" ]] || die "PostgreSQL skips and storage emulation cannot qualify"

for tool in go make git python3 timeout flock setsid sha256sum ip pg_isready psql systemctl pgrep \
  debugfs e2fsck firecracker jailer mkfs.ext4 nft iptables tc setpriv; do
  command -v "${tool}" >/dev/null || die "required host tool is missing: ${tool}"
done
: "${GREGALE_CI_ACCEPTANCE_DATABASE_URL:?set the dedicated test database DSN}"
: "${GREGALE_CI_ACCEPTANCE_GCS_BUCKET:?set the dedicated qualified CI bucket}"
: "${GREGALE_CI_ACCEPTANCE_IMAGE:?set the candidate digest-pinned CI image}"
: "${GREGALE_CI_ACCEPTANCE_ORIGIN:?set the guest-reachable trusted HTTPS origin}"
: "${GREGALE_CI_ACCEPTANCE_LISTEN:?set the matching TLS listener address}"
: "${GREGALE_CI_ACCEPTANCE_TLS_CERT:?set the TLS certificate path}"
: "${GREGALE_CI_ACCEPTANCE_TLS_KEY:?set the TLS private key path}"
: "${FAAS_TEST_KERNEL:?set the staged native guest kernel}"
[[ -r "${FAAS_TEST_KERNEL}" ]] || die "staged kernel is unreadable"

python3 - <<'PY'
import os, pathlib, stat
marker = pathlib.Path('/etc/faas/ci-acceptance-host')
try:
    metadata = marker.lstat()
except OSError:
    raise SystemExit('native CI acceptance: dedicated host marker is missing')
if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != 0 or metadata.st_mode & 0o022:
    raise SystemExit('native CI acceptance: host marker must be root-owned and not group/other writable')
for name in ('GREGALE_CI_ACCEPTANCE_TLS_CERT', 'GREGALE_CI_ACCEPTANCE_TLS_KEY'):
    path = pathlib.Path(os.environ[name])
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise SystemExit('native CI acceptance: TLS material must be regular files')
    if name.endswith('_KEY') and (metadata.st_uid != 0 or metadata.st_mode & 0o077):
        raise SystemExit('native CI acceptance: TLS key must be root-owned and private')
PY

exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 30 9 || die "another native acceptance run owns the host"
# Native resources are host-global. Refuse live fleets rather than stopping them.
for service in faas-apid faas-schedd faas-vmmd faas-imaged faas-builderd \
  faas-meterd faas-gatewayd-internal faas-gatewayd-public; do
  if systemctl is-active --quiet "${service}"; then
    die "${service} is active; use the dedicated drained acceptance host"
  fi
done
if pgrep -x 'firecracker(-v[0-9].*)?' >/dev/null; then
  die "native workloads are active; reconcile the previous gate first"
fi

cd "${repo_root}"
[[ -z "$(git status --porcelain --untracked-files=all)" ]] || die "candidate checkout must be clean for source provenance"
[[ "$(go env GOVERSION)" == go1.26.10 ]] || die "requires the pinned Go 1.26.10 toolchain"
export GREGALE_CI_ACCEPTANCE_SOURCE_SHA
GREGALE_CI_ACCEPTANCE_SOURCE_SHA="$(git rev-parse HEAD)"
evidence_root="${GREGALE_CI_ACCEPTANCE_EVIDENCE_ROOT:-/var/tmp/gregale-ci-native}"
[[ "${evidence_root}" =~ ^/var/tmp/gregale-ci-native(-[A-Za-z0-9._-]+)?$ && ! -L "${evidence_root}" ]] || die "evidence root must stay in the native CI staging namespace"
mkdir -p -- "${evidence_root}"
chmod 700 -- "${evidence_root}"
export GREGALE_CI_ACCEPTANCE_EVIDENCE
GREGALE_CI_ACCEPTANCE_EVIDENCE="$(mktemp -d "${evidence_root}/run-XXXXXXXX")"
evidence="${GREGALE_CI_ACCEPTANCE_EVIDENCE}"
echo "Native CI evidence: ${evidence}"

# Every image/artifact root and daemon binary belongs to this run. The runner
# image was built beforehand; only platform binaries are compiled on this host.
export FAAS_E2E_BIN_DIR="${evidence}/bin"
export FAAS_STORAGE_BACKEND=local FAAS_STORAGE_ROOT="${evidence}/storage"
export DATABASE_URL="${GREGALE_CI_ACCEPTANCE_DATABASE_URL}"
mkdir -p "${FAAS_E2E_BIN_DIR}" "${FAAS_STORAGE_ROOT}"
export FAAS_PUBLIC_IFACE="${FAAS_PUBLIC_IFACE:-$(ip route show default | awk '{print $5; exit}')}"
[[ -n "${FAAS_PUBLIC_IFACE}" ]] || die "set the tenant egress interface"
ip link show "${FAAS_PUBLIC_IFACE}" >/dev/null

test_group=""
cleanup() {
  local code=$?
  trap - EXIT INT TERM HUP
  set +e
  # Keep owners alive while the helper cancels intent and waits for their stop
  # receipts. It never issues VM commands or deletes uncertain reservations.
  if [[ -f "${evidence}/journal.json" && -x "${evidence}/ciacceptance" ]]; then
    timeout --signal=TERM --kill-after=10s 250s "${evidence}/ciacceptance" \
      -action cleanup -evidence "${evidence}" >"${evidence}/cleanup.log" 2>&1 || code=1
  fi
  if [[ -n "${test_group}" ]]; then
    kill -TERM -- "-${test_group}" 2>/dev/null
    sleep 5
    kill -KILL -- "-${test_group}" 2>/dev/null
    wait "${test_group}" 2>/dev/null
  fi
  # Assert what owners actually released. Do not reap first and conceal leaks.
  make leakcheck >"${evidence}/leakcheck-final.log" 2>&1 || code=1
  if [[ -x "${evidence}/ciacceptance" ]]; then
    "${evidence}/ciacceptance" -action finalize -evidence "${evidence}" \
      >"${evidence}/finalize.log" 2>&1 || code=1
    "${evidence}/ciacceptance" -action verdict -evidence "${evidence}" \
      >"${evidence}/verdict.log" 2>&1 || code=1
  else
    code=1
  fi
  printf '%s\n' "${code}" >"${evidence}/exit-code"
  exit "${code}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

make leakcheck >"${evidence}/leakcheck-initial.log" 2>&1
go build -tags metal -trimpath -o "${evidence}/ciacceptance" ./scripts/ci/ciacceptance
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags '-s -w' -o "${evidence}/gregale-ci-runner" ./cmd/ci-runner
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o "${evidence}/guest-init" ./guest/init
export FAAS_GUEST_INIT="${evidence}/guest-init"
export GREGALE_CI_ACCEPTANCE_RUNNER_SHA256 GREGALE_CI_ACCEPTANCE_GUEST_SHA256
GREGALE_CI_ACCEPTANCE_RUNNER_SHA256="$(sha256sum "${evidence}/gregale-ci-runner" | cut -d' ' -f1)"
GREGALE_CI_ACCEPTANCE_GUEST_SHA256="$(sha256sum "${FAAS_GUEST_INIT}" | cut -d' ' -f1)"

# No skip, an empty filter, or a portable substitute can produce the journal's
# six native receipts. Keep this separate from the ordinary metal matrix.
export RUN_REGEX='^TestCINativeAcceptance$'
setsid timeout --signal=TERM --kill-after=90s 24m make test-metal \
  PKGS=./tests/ci/acceptance RUN_ARGS='-json -timeout=23m' \
  >"${evidence}/native-tests.jsonl" 2>"${evidence}/native-tests.stderr" &
test_group=$!
result=0
wait "${test_group}" || result=$?
# Cleanup still signals the group: descendants can survive a failed test driver.
exit "${result}"
