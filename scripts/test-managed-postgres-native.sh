#!/bin/sh
set -eu
umask 077
: "${DATABASE_URL:?Use a disposable PostgreSQL cluster with TLS and SCRAM enabled}"
: "${GREGALE_POSTGRES_NATIVE_CA_PATH:?Set the disposable SQL certificate/CA with the proxy IP in its SAN}"
: "${FAAS_TEST_KERNEL:?Set the qualified guest kernel}"
: "${FAAS_BUILDER_BASE_PATH:?Set the qualified builder base}"
: "${FAAS_GUEST_INIT:?Set the real guest init compiled from this checkout}"
: "${GREGALE_POSTGRES_NATIVE_HOST_CLASS:?Set native or nested-diagnostic explicitly}"
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ "$(id -u)" != 0 ] || [ ! -r /dev/kvm ]; then
  echo 'PostgreSQL guest acceptance requires root on x86_64 Linux with KVM.' >&2
  exit 1
fi
if [ ! -f /etc/faas/builder-acceptance-host ]; then
  echo 'This host is not designated for isolated native acceptance.' >&2
  exit 1
fi
case "$GREGALE_POSTGRES_NATIVE_HOST_CLASS" in
  native)
    if ! command -v systemd-detect-virt >/dev/null; then echo 'Cannot establish native host class.' >&2; exit 1; fi
    if systemd-detect-virt --vm --quiet; then
      echo 'A virtual machine cannot provide supported native-host qualification; use nested-diagnostic.' >&2
      exit 1
    fi
    ;;
  nested-diagnostic) echo 'Nested virtualization diagnostic: this run cannot qualify production rollout.' ;;
  *) echo 'Host class must be native or nested-diagnostic.' >&2; exit 1 ;;
esac
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ] || [ ! -r "$GREGALE_POSTGRES_NATIVE_CA_PATH" ] || [ ! -r "$FAAS_TEST_KERNEL" ] || [ ! -r "$FAAS_BUILDER_BASE_PATH" ] || [ ! -x "$FAAS_GUEST_INIT" ]; then
  echo 'Cannot skip PostgreSQL or use unreadable native fixtures.' >&2
  exit 1
fi
for probe_tool in firecracker jailer ip nft flock python3 systemd-detect-virt; do
  command -v "$probe_tool" >/dev/null || { echo "Missing native dependency: $probe_tool" >&2; exit 1; }
done
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || { echo 'Native acceptance host is busy.' >&2; exit 1; }
for probe_service in faas-vmmd faas-builderd faas-imaged faas-gatewayd-internal; do
  if systemctl is-active --quiet "$probe_service"; then
    echo "Refusing to share a host with active $probe_service." >&2
    exit 1
  fi
done
probe_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$probe_root"
probe_revision=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain)" ]; then
  echo 'Guest qualification requires a clean source checkout.' >&2
  exit 1
fi
for probe_output in "${GREGALE_POSTGRES_NATIVE_REPORT_PATH:-}" "${GREGALE_POSTGRES_NATIVE_RESULTS_PATH:-}"; do
  if [ -n "$probe_output" ] && { [ -e "$probe_output" ] || [ -L "$probe_output" ]; }; then
    echo 'Refusing to replace existing guest qualification evidence.' >&2
    exit 1
  fi
done
make leakcheck
# RFC 2544 address, owned only during this isolated test. Guest egress policy
# uses the ordinary Pro app port declaration. No host denylist is relaxed.
# This proves TCP/TLS from the guest, not Internet/DNS egress.
probe_ip=198.18.0.254
if [ -n "$(ip -4 address show to "$probe_ip/32")" ]; then
  echo 'Disposable SQL proxy address already exists; refusing to take ownership.' >&2
  exit 1
fi
probe_results=$(mktemp "${TMPDIR:-/tmp}/gregale-postgres-native.XXXXXX")
probe_report=${GREGALE_POSTGRES_NATIVE_REPORT_PATH:-$probe_results.report.json}
probe_input=${GREGALE_POSTGRES_NATIVE_RESULTS_PATH:-$probe_results}
probe_log_saved=0
probe_address_owned=0
probe_cleanup() {
  if [ "$probe_address_owned" = 1 ]; then
    ip address del "$probe_ip/32" dev lo || return 1
    probe_address_owned=0
  fi
  if [ "$probe_log_saved" = 0 ]; then
    if [ -n "${GREGALE_POSTGRES_NATIVE_RESULTS_PATH:-}" ]; then
      # A newly created private file, never an existing file/symlink whose
      # permissions or content belong to an earlier run.
      (set -C; cat "$probe_results" > "$GREGALE_POSTGRES_NATIVE_RESULTS_PATH") || return 1
      rm -f "$probe_results"
    else
      echo "Private guest test log: $probe_results"
    fi
    probe_log_saved=1
  fi
}
probe_on_exit() {
  probe_exit=$?
  trap - EXIT
  probe_cleanup || probe_exit=1
  exit "$probe_exit"
}
trap probe_on_exit EXIT
trap 'exit 1' HUP INT TERM
ip address add "$probe_ip/32" dev lo
probe_address_owned=1
export GREGALE_POSTGRES_NATIVE_SQL_IP="$probe_ip"
export GREGALE_POSTGRES_NATIVE_ACCEPTANCE=1
# Qualification needs executable provenance, including the test executable.
# Preserve bounded compiler flags while overriding diagnostic buildvcs=false.
GOFLAGS="${GOFLAGS:-} -buildvcs=true"
export GOFLAGS
probe_status=0
"${GO:-go}" test -p 1 -race -tags metal,managed_postgres_native -json ./cmd/e2e -count=1 -timeout 45m \
  -run '^TestManagedPostgresNativeMetal$' > "$probe_results" || probe_status=$?
# Check leak cleanup on failure too. Preserve failure even when the verdict or
# teardown also fails; successful teardown never turns a failed phase green.
probe_cleanup || probe_status=1
make leakcheck || probe_status=1
if [ "$(git rev-parse HEAD)" != "$probe_revision" ] || [ -n "$(git status --porcelain)" ]; then
  echo 'Source checkout changed during guest acceptance.' >&2
  probe_status=1
fi
if [ "$probe_status" = 0 ]; then
  python3 scripts/ci/managed-postgres-native-verdict.py "$probe_input" \
    --report "$probe_report" --host-class "$GREGALE_POSTGRES_NATIVE_HOST_CLASS" \
    --source-clean --leakcheck-before --leakcheck-after || probe_status=1
fi
if [ "$probe_status" != 0 ]; then
  echo 'PostgreSQL guest acceptance failed; retain the private JSON log for diagnosis.' >&2
  exit "$probe_status"
fi
echo "PostgreSQL guest acceptance passed ($GREGALE_POSTGRES_NATIVE_HOST_CLASS)."
echo "Non-secret guest qualification report: $probe_report"
