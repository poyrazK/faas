#!/bin/sh
set -eu
umask 077
case "${1:-}" in
  '') storage_preflight_only=0 ;;
  --preflight) storage_preflight_only=1 ;;
  *) echo 'Usage: scripts/test-object-storage-native.sh [--preflight]' >&2; exit 1 ;;
esac
if [ "$#" -gt 1 ]; then echo 'Unexpected storage acceptance arguments.' >&2; exit 1; fi
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ "$(id -u)" != 0 ] || [ ! -r /dev/kvm ]; then
  echo 'Storage guest acceptance requires root on x86_64 Linux with KVM.' >&2
  exit 1
fi
if [ ! -f /etc/faas/builder-acceptance-host ]; then
  echo 'This host is not designated for isolated native acceptance.' >&2
  exit 1
fi
: "${DATABASE_URL:?Use a disposable PostgreSQL catalog}"
: "${FAAS_TEST_KERNEL:?Set the qualified guest kernel}"
: "${FAAS_BUILDER_BASE_PATH:?Set the qualified builder base}"
: "${FAAS_GUEST_INIT:?Set the real static guest init built from this checkout}"
: "${GREGALE_STORAGE_NATIVE_HOST_CLASS:?Set native or nested-diagnostic explicitly}"
for storage_tool in firecracker jailer ip nft flock python3 systemctl systemd-detect-virt git make; do
  command -v "$storage_tool" >/dev/null || { echo "Missing native dependency: $storage_tool" >&2; exit 1; }
done
case "$GREGALE_STORAGE_NATIVE_HOST_CLASS" in
  native)
    storage_virt_status=0
    storage_virt=$(systemd-detect-virt --vm) || storage_virt_status=$?
    storage_container_status=0
    storage_container=$(systemd-detect-virt --container) || storage_container_status=$?
    if [ "$storage_virt" != none ] || [ "$storage_virt_status" != 1 ] || [ "$storage_container" != none ] || [ "$storage_container_status" != 1 ]; then
      echo 'Cannot establish a native host; use nested-diagnostic for virtual hosts.' >&2
      exit 1
    fi
    ;;
  nested-diagnostic) echo 'Nested diagnostic: this run cannot qualify native storage.' ;;
  *) echo 'Host class must be native or nested-diagnostic.' >&2; exit 1 ;;
esac
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ] || [ ! -r "$FAAS_TEST_KERNEL" ] || [ ! -r "$FAAS_BUILDER_BASE_PATH" ] || [ ! -x "$FAAS_GUEST_INIT" ]; then
  echo 'Cannot skip PostgreSQL or use missing native fixtures.' >&2
  exit 1
fi
storage_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$storage_root"
storage_revision=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo 'Storage qualification requires a clean source checkout.' >&2
  exit 1
fi
for storage_output in "${GREGALE_STORAGE_NATIVE_REPORT_PATH:-}" "${GREGALE_STORAGE_NATIVE_RESULTS_PATH:-}"; do
  if [ -n "$storage_output" ] && { [ -e "$storage_output" ] || [ -L "$storage_output" ]; }; then
    echo 'Refusing to replace existing storage acceptance evidence.' >&2
    exit 1
  fi
done
if [ -n "${GREGALE_STORAGE_NATIVE_REPORT_PATH:-}" ] && [ "${GREGALE_STORAGE_NATIVE_REPORT_PATH}" = "${GREGALE_STORAGE_NATIVE_RESULTS_PATH:-}" ]; then
  echo 'Storage report and private results need separate paths.' >&2
  exit 1
fi
for storage_service in faas-vmmd faas-builderd faas-imaged faas-gatewayd-internal; do
  if systemctl is-active --quiet "$storage_service"; then
    echo "Refusing to share a host with active $storage_service." >&2
    exit 1
  fi
done
storage_ip=198.18.0.254
if [ -n "$(ip -4 address show to "$storage_ip/32")" ]; then
  echo 'Owned storage gateway address already exists; refusing to take ownership.' >&2
  exit 1
fi
if [ "$storage_preflight_only" = 1 ]; then
  echo 'Storage native prerequisites passed; no daemons or guests started.'
  exit 0
fi
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || { echo 'Native acceptance host is busy.' >&2; exit 1; }
make leakcheck
storage_results=$(mktemp "${TMPDIR:-/tmp}/gregale-storage-native.XXXXXX")
storage_input=${GREGALE_STORAGE_NATIVE_RESULTS_PATH:-$storage_results}
storage_report=${GREGALE_STORAGE_NATIVE_REPORT_PATH:-$storage_results.report.json}
storage_address_owned=0
storage_log_saved=0
storage_cleanup() {
  if [ "$storage_address_owned" = 1 ]; then
    ip address del "$storage_ip/32" dev lo || return 1
    storage_address_owned=0
  fi
  if [ "$storage_log_saved" = 0 ]; then
    if [ -n "${GREGALE_STORAGE_NATIVE_RESULTS_PATH:-}" ]; then
      (set -C; cat "$storage_results" > "$GREGALE_STORAGE_NATIVE_RESULTS_PATH") || return 1
      rm -f "$storage_results"
    else
      echo "Private storage guest log: $storage_results"
    fi
    storage_log_saved=1
  fi
}
storage_on_exit() {
  storage_exit=$?
  trap - EXIT
  storage_cleanup || storage_exit=1
  exit "$storage_exit"
}
trap storage_on_exit EXIT
trap 'exit 1' HUP INT TERM
# Own this benchmark address only for the duration of the isolated journey.
# The app declares its /32 and TCP port through the normal public policy API.
ip address add "$storage_ip/32" dev lo
storage_address_owned=1
export GREGALE_STORAGE_NATIVE_IP="$storage_ip"
export GREGALE_STORAGE_NATIVE_ACCEPTANCE=1
GOFLAGS="${GOFLAGS:-} -buildvcs=true"
export GOFLAGS
storage_status=0
"${GO:-go}" test -p 1 -race -tags metal,object_storage_native -json ./cmd/e2e -count=1 -timeout 60m \
  -run '^TestObjectStorageNativeMetal$' > "$storage_results" || storage_status=$?
storage_cleanup || storage_status=1
make leakcheck || storage_status=1
if [ "$(git rev-parse HEAD)" != "$storage_revision" ] || [ -n "$(git status --porcelain)" ]; then
  echo 'Source checkout changed during storage acceptance.' >&2
  storage_status=1
fi
if [ "$storage_status" = 0 ]; then
  python3 scripts/ci/object-storage-native-verdict.py "$storage_input" --report "$storage_report" \
    --host-class "$GREGALE_STORAGE_NATIVE_HOST_CLASS" --source-clean --leakcheck-before --leakcheck-after || storage_status=1
fi
if [ "$storage_status" != 0 ]; then
  echo 'Storage guest acceptance failed; retain the private JSON log for diagnosis.' >&2
  exit "$storage_status"
fi
echo "Storage guest acceptance passed ($GREGALE_STORAGE_NATIVE_HOST_CLASS)."
echo "Non-secret storage guest report: $storage_report"
