#!/bin/sh
set -eu
umask 077
case "${1:-}" in
  '') queue_preflight_only=0 ;;
  --preflight) queue_preflight_only=1 ;;
  *) echo 'Usage: scripts/test-queue-bindings-native.sh [--preflight]' >&2; exit 1 ;;
esac
if [ "$#" -gt 1 ]; then echo 'Unexpected queue acceptance arguments.' >&2; exit 1; fi
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ "$(id -u)" != 0 ] || [ ! -r /dev/kvm ]; then
  echo 'Queue guest acceptance requires root on x86_64 Linux with KVM.' >&2
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
: "${GREGALE_QUEUE_NATIVE_HOST_CLASS:?Set native or nested-diagnostic explicitly}"
for queue_tool in firecracker jailer ip nft flock python3 systemctl systemd-detect-virt git make; do
  command -v "$queue_tool" >/dev/null || { echo "Missing native dependency: $queue_tool" >&2; exit 1; }
done
case "$GREGALE_QUEUE_NATIVE_HOST_CLASS" in
  native)
    queue_virt_status=0
    queue_virt=$(systemd-detect-virt --vm) || queue_virt_status=$?
    queue_container_status=0
    queue_container=$(systemd-detect-virt --container) || queue_container_status=$?
    if [ "$queue_virt" != none ] || [ "$queue_virt_status" != 1 ] || [ "$queue_container" != none ] || [ "$queue_container_status" != 1 ]; then
      echo 'Cannot establish a native host; use nested-diagnostic for virtual hosts.' >&2
      exit 1
    fi
    ;;
  nested-diagnostic) echo 'Nested diagnostic: this run cannot qualify native queue.' ;;
  *) echo 'Host class must be native or nested-diagnostic.' >&2; exit 1 ;;
esac
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ] || [ ! -r "$FAAS_TEST_KERNEL" ] || [ ! -r "$FAAS_BUILDER_BASE_PATH" ] || [ ! -x "$FAAS_GUEST_INIT" ]; then
  echo 'Cannot skip PostgreSQL or use missing native fixtures.' >&2
  exit 1
fi
queue_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$queue_root"
queue_revision=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo 'Queue qualification requires a clean source checkout.' >&2
  exit 1
fi
for queue_output in "${GREGALE_QUEUE_NATIVE_REPORT_PATH:-}" "${GREGALE_QUEUE_NATIVE_RESULTS_PATH:-}"; do
  if [ -n "$queue_output" ] && { [ -e "$queue_output" ] || [ -L "$queue_output" ]; }; then
    echo 'Refusing to replace existing queue acceptance evidence.' >&2
    exit 1
  fi
done
if [ -n "${GREGALE_QUEUE_NATIVE_REPORT_PATH:-}" ] && [ "${GREGALE_QUEUE_NATIVE_REPORT_PATH}" = "${GREGALE_QUEUE_NATIVE_RESULTS_PATH:-}" ]; then
  echo 'Queue report and private results need separate paths.' >&2
  exit 1
fi
for queue_service in faas-vmmd faas-builderd faas-imaged faas-gatewayd-internal; do
  if systemctl is-active --quiet "$queue_service"; then
    echo "Refusing to share a host with active $queue_service." >&2
    exit 1
  fi
done
if [ "$queue_preflight_only" = 1 ]; then
  echo 'Queue native prerequisites passed; no daemons or guests started.'
  exit 0
fi
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || { echo 'Native acceptance host is busy.' >&2; exit 1; }
make leakcheck
queue_results=$(mktemp "${TMPDIR:-/tmp}/gregale-queue-native.XXXXXX")
queue_input=${GREGALE_QUEUE_NATIVE_RESULTS_PATH:-$queue_results}
queue_report=${GREGALE_QUEUE_NATIVE_REPORT_PATH:-$queue_results.report.json}
queue_log_saved=0
queue_cleanup() {
  if [ "$queue_log_saved" = 0 ]; then
    if [ -n "${GREGALE_QUEUE_NATIVE_RESULTS_PATH:-}" ]; then
      (set -C; cat "$queue_results" > "$GREGALE_QUEUE_NATIVE_RESULTS_PATH") || return 1
      rm -f "$queue_results"
    else
      echo "Private queue guest log: $queue_results"
    fi
    queue_log_saved=1
  fi
}
queue_on_exit() {
  queue_exit=$?
  trap - EXIT
  queue_cleanup || queue_exit=1
  exit "$queue_exit"
}
trap queue_on_exit EXIT
trap 'exit 1' HUP INT TERM
export GREGALE_QUEUE_NATIVE_ACCEPTANCE=1
GOFLAGS="${GOFLAGS:-} -buildvcs=true"
export GOFLAGS
queue_status=0
"${GO:-go}" test -p 1 -race -tags metal,queue_binding_native -json ./cmd/e2e -count=1 -timeout 40m \
  -run '^TestQueueBindingNativeMetal$' > "$queue_results" || queue_status=$?
queue_cleanup || queue_status=1
make leakcheck || queue_status=1
if [ "$(git rev-parse HEAD)" != "$queue_revision" ] || [ -n "$(git status --porcelain)" ]; then
  echo 'Source checkout changed during queue acceptance.' >&2
  queue_status=1
fi
if [ "$queue_status" = 0 ]; then
  python3 scripts/ci/queue-bindings-native-verdict.py "$queue_input" --report "$queue_report" \
    --host-class "$GREGALE_QUEUE_NATIVE_HOST_CLASS" --source-clean --leakcheck-before --leakcheck-after || queue_status=1
fi
if [ "$queue_status" != 0 ]; then
  echo 'Queue guest acceptance failed; retain the private JSON log for diagnosis.' >&2
  exit "$queue_status"
fi
echo "Queue guest acceptance passed ($GREGALE_QUEUE_NATIVE_HOST_CLASS)."
echo "Non-secret queue guest report: $queue_report"
