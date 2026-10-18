#!/bin/sh
set -eu
umask 077
case "${1:-}" in
  '') service_preflight_only=0 ;;
  --preflight) service_preflight_only=1 ;;
  *) echo 'Usage: scripts/test-managed-service-native.sh [--preflight]' >&2; exit 1 ;;
esac
if [ "$#" -gt 1 ]; then echo 'Unexpected service acceptance arguments.' >&2; exit 1; fi
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ "$(id -u)" != 0 ] || [ ! -r /dev/kvm ]; then
  echo 'Managed HTTPS guest acceptance requires root on x86_64 Linux with KVM.' >&2
  exit 1
fi
if [ ! -f /etc/faas/builder-acceptance-host ]; then
  echo 'This host is not designated for isolated native acceptance.' >&2
  exit 1
fi
: "${DATABASE_URL:?Use a disposable PostgreSQL catalog}"
: "${FAAS_TEST_KERNEL:?Set the qualified guest kernel}"
: "${FAAS_BUILDER_BASE_PATH:?Set the qualified builder base}"
: "${FAAS_TEST_MINIMAL_BASE_PATH:?Set the approved minimal deployment base}"
: "${FAAS_GUEST_INIT:?Set the real static guest init built from this checkout}"
: "${GREGALE_SERVICE_NATIVE_HOST_CLASS:?Set native or nested-diagnostic explicitly}"
for service_tool in firecracker jailer ip nft flock python3 systemctl systemd-detect-virt git make pgrep; do
  command -v "$service_tool" >/dev/null || { echo "Missing native dependency: $service_tool" >&2; exit 1; }
done
case "$GREGALE_SERVICE_NATIVE_HOST_CLASS" in
  native)
    service_virt_status=0
    service_virt=$(systemd-detect-virt --vm) || service_virt_status=$?
    service_container_status=0
    service_container=$(systemd-detect-virt --container) || service_container_status=$?
    if [ "$service_virt" != none ] || [ "$service_virt_status" != 1 ] || [ "$service_container" != none ] || [ "$service_container_status" != 1 ]; then
      echo 'Cannot establish a native host; use nested-diagnostic for virtual hosts.' >&2
      exit 1
    fi
    ;;
  nested-diagnostic) echo 'Nested diagnostic: this run cannot qualify native service.' ;;
  *) echo 'Host class must be native or nested-diagnostic.' >&2; exit 1 ;;
esac
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ] || [ ! -r "$FAAS_TEST_KERNEL" ] || [ ! -r "$FAAS_BUILDER_BASE_PATH" ] || [ ! -x "$FAAS_GUEST_INIT" ] || [ ! -r "$FAAS_TEST_MINIMAL_BASE_PATH" ] || [ ! -r "$FAAS_TEST_MINIMAL_BASE_PATH.digest" ]; then
  echo 'Cannot skip PostgreSQL or use missing native fixtures.' >&2
  exit 1
fi
service_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$service_root"
service_revision=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo 'Managed HTTPS qualification requires a clean source checkout.' >&2
  exit 1
fi
for service_output in "${GREGALE_SERVICE_NATIVE_REPORT_PATH:-}" "${GREGALE_SERVICE_NATIVE_RESULTS_PATH:-}"; do
  if [ -n "$service_output" ] && { [ -e "$service_output" ] || [ -L "$service_output" ]; }; then
    echo 'Refusing to replace existing service acceptance evidence.' >&2
    exit 1
  fi
done
if [ -n "${GREGALE_SERVICE_NATIVE_REPORT_PATH:-}" ] && [ "${GREGALE_SERVICE_NATIVE_REPORT_PATH}" = "${GREGALE_SERVICE_NATIVE_RESULTS_PATH:-}" ]; then
  echo 'Managed HTTPS report and private results need separate paths.' >&2
  exit 1
fi
for service_service in faas-apid faas-schedd faas-vmmd faas-builderd faas-imaged faas-meterd faas-gatewayd-internal faas-gatewayd-public; do
  if systemctl is-active --quiet "$service_service"; then
    echo "Refusing to share a host with active $service_service." >&2
    exit 1
  fi
done
service_process_status=0
pgrep -x 'firecracker(-v[0-9].*)?' >/dev/null || service_process_status=$?
if [ "$service_process_status" != 1 ]; then
  echo 'Firecracker workloads are active or process inventory is unavailable.' >&2
  exit 1
fi
if [ "$service_preflight_only" = 1 ]; then
  echo 'Managed HTTPS native prerequisites passed; no daemons or guests started.'
  exit 0
fi
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || { echo 'Native acceptance host is busy.' >&2; exit 1; }
make leakcheck
service_results=$(mktemp "${TMPDIR:-/tmp}/gregale-service-native.XXXXXX")
service_input=${GREGALE_SERVICE_NATIVE_RESULTS_PATH:-$service_results}
service_report=${GREGALE_SERVICE_NATIVE_REPORT_PATH:-$service_results.report.json}
service_log_saved=0
service_cleanup() {
  if [ "$service_log_saved" = 0 ]; then
    if [ -n "${GREGALE_SERVICE_NATIVE_RESULTS_PATH:-}" ]; then
      (set -C; cat "$service_results" > "$GREGALE_SERVICE_NATIVE_RESULTS_PATH") || return 1
      rm -f "$service_results"
    else
      echo "Private service guest log: $service_results"
    fi
    service_log_saved=1
  fi
}
service_on_exit() {
  service_exit=$?
  trap - EXIT
  service_cleanup || service_exit=1
  exit "$service_exit"
}
trap service_on_exit EXIT
trap 'exit 1' HUP INT TERM
export GREGALE_SERVICE_NATIVE_ACCEPTANCE=1
GOFLAGS="${GOFLAGS:-} -buildvcs=true"
export GOFLAGS
service_status=0
"${GO:-go}" test -p 1 -race -tags metal,managed_service_native -json ./cmd/e2e -count=1 -timeout 90m \
  -run '^TestManagedServiceBindingNativeMetal$' > "$service_results" || service_status=$?
service_cleanup || service_status=1
make leakcheck || service_status=1
if [ "$(git rev-parse HEAD)" != "$service_revision" ] || [ -n "$(git status --porcelain)" ]; then
  echo 'Source checkout changed during service acceptance.' >&2
  service_status=1
fi
if [ "$service_status" = 0 ]; then
  python3 scripts/ci/managed-service-native-verdict.py "$service_input" --report "$service_report" \
    --host-class "$GREGALE_SERVICE_NATIVE_HOST_CLASS" --source-clean --leakcheck-before --leakcheck-after || service_status=1
fi
if [ "$service_status" != 0 ]; then
  echo 'Managed HTTPS guest acceptance failed; retain the private JSON log for diagnosis.' >&2
  exit "$service_status"
fi
echo "Managed HTTPS guest acceptance passed ($GREGALE_SERVICE_NATIVE_HOST_CLASS)."
echo "Non-secret service guest report: $service_report"
