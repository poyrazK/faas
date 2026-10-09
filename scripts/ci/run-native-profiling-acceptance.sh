#!/usr/bin/env bash
# Run only on the dedicated live profiling acceptance stack.
set -Eeuo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
evidence="${GREGALE_PROFILE_EVIDENCE:-${repo_root}/profile-native-evidence}"
mkdir -p "${evidence}"
chmod 700 "${evidence}"
# Keep cleanup on a separate deadline even if qualification fails or is interrupted.
cleanup() {
  local code=$?
  trap - EXIT INT TERM
  if [[ -x "${evidence}/profilefixtures" ]]; then
    if ! timeout --signal=TERM --kill-after=10s 4m "${evidence}/profilefixtures" \
      -action cleanup -root "${repo_root}" -out "${evidence}"; then
      code=1
    else
      rm -f "${evidence}/profilefixtures"
    fi
  fi
  rm -f "${evidence}"/*.tar.gz
  exit "${code}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
[[ -c /dev/kvm ]] || { echo 'profiling acceptance requires native KVM' >&2; exit 1; }
[[ -f /etc/faas/profiling-acceptance-host ]] || { echo 'dedicated profiling acceptance host marker is missing' >&2; exit 1; }
: "${GREGALE_ACCEPTANCE_API_URL:?set dedicated API URL}"
: "${GREGALE_ACCEPTANCE_TOKEN:?set dedicated account token}"
: "${GREGALE_NATIVE_PROFILE_TOKEN:?set fixture-only token}"
for tool in go python3 timeout flock git; do command -v "${tool}" >/dev/null; done
# Shared with native builder/e2e host-global operations. CI concurrency alone
# cannot serialize local manual invocations or other workflows on this host.
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 30 9
cd "${repo_root}"
# The dedicated stack must already run this candidate's daemon and guest images.
# Host release tooling writes this root-owned stamp after installing the stack.
source_sha="${GITHUB_SHA:-$(git rev-parse HEAD)}"
export source_sha
python3 - "${evidence}" <<'PYPROVENANCE'
import json, os, pathlib, stat, sys
stamp = pathlib.Path('/etc/faas/profiling-acceptance-source-sha')
metadata = stamp.lstat()
if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != 0 or metadata.st_mode & 0o022:
    raise SystemExit('profiling platform revision stamp must be root-owned and not writable by group/others')
installed = stamp.read_text().strip()
expected = os.environ['source_sha']
if installed != expected or len(expected) != 40:
    raise SystemExit('dedicated profiling stack revision does not match the CI checkout')
(pathlib.Path(sys.argv[1]) / 'provenance.json').write_text(json.dumps({
    'source_sha': expected, 'installed_platform_sha': installed,
    'platform_evidence_source': 'root-owned release stamp'}, indent=2))
PYPROVENANCE
go build -o "${evidence}/profilefixtures" ./scripts/ci/profilefixtures
timeout --signal=TERM --kill-after=10s 27m "${evidence}/profilefixtures" \
  -action provision -root "${repo_root}" -out "${evidence}"
timeout --signal=TERM --kill-after=10s 16m python3 tests/profiling/deployment/run.py \
  --config "${evidence}/config.json" --report "${evidence}/acceptance.json"
