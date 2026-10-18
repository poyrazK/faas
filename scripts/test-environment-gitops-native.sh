#!/usr/bin/env bash
# Run the real API + push-worker GitOps resilience graph on an isolated KVM host. A nested
# run is useful for diagnosis but cannot qualify production enforcement.
set -Eeuo pipefail
umask 077

die() { echo "GitOps native acceptance: $*" >&2; exit 1; }

: "${GREGALE_GITOPS_NATIVE_HOST_CLASS:?set native or nested-diagnostic explicitly}"
: "${DATABASE_URL:?set a disposable PostgreSQL URL}"
: "${FAAS_TEST_KERNEL:?set the qualified guest kernel}"
: "${FAAS_TEST_MINIMAL_BASE_PATH:?set a validated generic base ext4}"
: "${FAAS_GUEST_INIT:?set guest-init built from this checkout}"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && "$(id -u)" == 0 && -c /dev/kvm ]] ||
  die "root on x86_64 Linux with KVM is required"
[[ -f /etc/faas/builder-acceptance-host ]] || die "host is not designated for isolated acceptance"
for tool in flock make psql systemctl systemd-detect-virt; do
  command -v "$tool" >/dev/null || die "missing host tool: $tool"
done
go_binary="$(command -v "${GO:-go}")" || die "the pinned Go binary is unavailable"
PATH="$(dirname "$go_binary"):$PATH"
export PATH
case "$GREGALE_GITOPS_NATIVE_HOST_CLASS" in
  native)
    if systemd-detect-virt --vm --quiet; then
      die "a virtual machine cannot provide dedicated native acceptance"
    fi
    ;;
  nested-diagnostic)
    echo "GitOps native acceptance: nested diagnostic only; this does not open production gates" >&2
    ;;
  *) die "host class must be native or nested-diagnostic" ;;
esac
for fixture in "$FAAS_TEST_KERNEL" "$FAAS_TEST_MINIMAL_BASE_PATH" "$FAAS_TEST_MINIMAL_BASE_PATH.digest" "$FAAS_GUEST_INIT"; do
  [[ -r "$fixture" ]] || die "native fixture is unreadable: $fixture"
done
[[ -x "$FAAS_GUEST_INIT" ]] || die "guest-init is not executable"
[[ -z "${FAAS_SKIP_PG_TESTS:-}" ]] || die "PostgreSQL tests cannot be skipped"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc 'select 1' >/dev/null || die "disposable PostgreSQL is unreachable"

exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || die "acceptance host is busy"
for service in faas-apid faas-imaged faas-schedd faas-vmmd faas-gatewayd-internal faas-gatewayd-public; do
  if systemctl is-active --quiet "$service"; then
    die "refusing to share the host with active $service"
  fi
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
for volume in "$repo_root" "${TMPDIR:-/tmp}"; do
  free_kib="$(df -Pk "$volume" | awk 'NR == 2 {print $4}')"
  [[ "$free_kib" =~ ^[0-9]+$ && "$free_kib" -ge 8388608 ]] ||
    die "at least 8 GiB free is required on the checkout and temporary volumes ($volume)"
done
revision="$(git rev-parse HEAD)"
[[ -z "$(git status --porcelain)" ]] || die "source checkout must be clean"
log="$(mktemp "${TMPDIR:-/tmp}/gregale-gitops-native.XXXXXX")"
echo "GitOps native acceptance: private test log $log"

status=0
make leakcheck || status=1
if [[ "$status" == 0 ]]; then
  GOMEMLIMIT=6GiB GREGALE_GITOPS_NATIVE_ACCEPTANCE=1 GREGALE_GITOPS_ACCEPTANCE=1 \
    make GO="$go_binary" PKGS=./cmd/e2e RUN_REGEX='^TestEnvironmentGitOpsNativeMetal$' \
    RUN_ARGS='-v -timeout 90m' test-metal >"$log" 2>&1 || status=1
fi
if ! grep -qE '^--- PASS: TestEnvironmentGitOpsNativeMetal( |$)' "$log"; then
  echo "GitOps native acceptance: required end-to-end graph test did not pass" >&2
  status=1
fi
if grep -qE '^--- (SKIP|FAIL): TestEnvironmentGitOpsNativeMetal( |$)' "$log"; then
  echo "GitOps native acceptance: required graph test skipped or failed" >&2
  status=1
fi
make leakcheck || status=1
if [[ "$(git rev-parse HEAD)" != "$revision" || -n "$(git status --porcelain)" ]]; then
  echo "GitOps native acceptance: source checkout changed during the run" >&2
  status=1
fi
if [[ "$status" != 0 ]]; then
  echo "GitOps native acceptance failed; inspect the private log at $log" >&2
  exit "$status"
fi
if [[ "$GREGALE_GITOPS_NATIVE_HOST_CLASS" == native ]]; then
  echo "GitOps native acceptance passed at $revision"
else
  echo "GitOps nested diagnostic passed at $revision; production gate remains closed"
fi
echo "Private test log: $log"
