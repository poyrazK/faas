#!/usr/bin/env bash
# Run image API/worker and real source/Dockerfile scheduled-Job journeys for both providers.
# Reports are local operator evidence and never enable production enforcement.
set -Eeuo pipefail
umask 077

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v python3 >/dev/null || { echo "GitOps native acceptance: python3 is required" >&2; exit 1; }
report_tool="$repo_root/scripts/gitops_native_report.py"
report_dir="$(python3 "$report_tool" init --repo "$repo_root" \
  --directory "${GREGALE_GITOPS_NATIVE_REPORT_DIR:-}" --host-class "${GREGALE_GITOPS_NATIVE_HOST_CLASS:-}")"
preflight=blocked
leak_before=not_run
journey=not_run
leak_after=not_run
source_check=not_run
reason=preflight_incomplete
finish() {
  local status=$? report_status=0
  trap - EXIT
  # Interrupted/failed commands must still leave a post-run leak observation.
  # Never remove host resources to make this observation pass.
  if [[ "$preflight" == pass && "$leak_after" == not_run ]]; then
    if make -C "$repo_root" leakcheck >"$report_dir/leak-after.log" 2>&1; then leak_after=pass; else leak_after=fail; fi
  fi
  python3 "$report_tool" finish --repo "$repo_root" --directory "$report_dir" \
    --exit-code "$status" --reason "$reason" --preflight "$preflight" \
    --leak-before "$leak_before" --journey "$journey" --leak-after "$leak_after" \
    --source "$source_check" || report_status=$?
  echo "GitOps native acceptance: private report $report_dir/report.json"
  [[ "$status" != 0 || "$report_status" == 0 ]] || status=1
  exit "$status"
}
trap finish EXIT
trap 'reason=interrupted; exit 130' INT
trap 'reason=interrupted; exit 143' TERM

die() { reason="$1"; echo "GitOps native acceptance: $2" >&2; exit 1; }
require_env() { [[ -n "${!1:-}" ]] || die missing_configuration "set $1 explicitly"; }
for variable in GREGALE_GITOPS_NATIVE_HOST_CLASS DATABASE_URL FAAS_TEST_KERNEL FAAS_GUEST_INIT GREGALE_GITOPS_NATIVE_BUILDER_REF GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF; do
  require_env "$variable"
done
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && "$(id -u)" == 0 && -c /dev/kvm ]] ||
  die unsupported_host "root on x86_64 Linux with KVM is required"
[[ -f /etc/faas/builder-acceptance-host ]] || die undesignated_host "host is not designated for isolated acceptance"
for tool in flock make psql systemctl systemd-detect-virt firecracker jailer; do
  command -v "$tool" >/dev/null || die missing_tool "missing host tool: $tool"
done
go_binary="$(command -v "${GO:-go}")" || die missing_tool "the pinned Go binary is unavailable"
pinned_go="$(awk '$1 == "go" {print "go" $2; exit}' "$repo_root/go.mod")"
[[ "$("$go_binary" env GOVERSION)" == "$pinned_go" ]] || die unsupported_go "use the exact Go version pinned by go.mod"
PATH="$(dirname "$go_binary"):$PATH"
export PATH
virtualization_status=0
virtualization="$(systemd-detect-virt --vm)" || virtualization_status=$?
case "$GREGALE_GITOPS_NATIVE_HOST_CLASS" in
  native)
    [[ "$virtualization_status" == 1 && "$virtualization" == none ]] ||
      die virtualized_host "an unverified or virtual host cannot provide dedicated native acceptance"
    ;;
  nested-diagnostic)
    echo "GitOps native acceptance: diagnostic only; production gates remain closed" >&2
    ;;
  *) die unsupported_host_class "host class must be native or nested-diagnostic" ;;
esac
for fixture in "$FAAS_TEST_KERNEL" "$FAAS_GUEST_INIT"; do
  [[ -f "$fixture" && -r "$fixture" ]] || die missing_fixture "a required native fixture is unreadable"
done
[[ -x "$FAAS_GUEST_INIT" ]] || die invalid_fixture "guest-init is not executable"
if ! python3 - "$report_dir/report.json" <<'PY_IMAGES'
import json, sys
raise SystemExit(0 if all(json.load(open(sys.argv[1]))["initial"]["base_images"].values()) else 1)
PY_IMAGES
then die invalid_base_images "all three acceptance base images must be digest-pinned references without credentials"; fi
[[ -z "${FAAS_SKIP_PG_TESTS:-}" ]] || die skipped_postgres "PostgreSQL tests cannot be skipped"
[[ -z "${FAAS_E2E_BIN_DIR:-}" && -z "${FAAS_E2E_HELLO_SERVER_BINARY:-}" ]] ||
  die unverified_prebuilt_binaries "acceptance must build its daemons and fixture server from this checkout"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc 'select 1' >"$report_dir/postgres-probe.log" 2>&1 ||
  die unavailable_postgres "disposable PostgreSQL is unreachable"

exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || die busy_host "acceptance host is busy"
for service in faas-apid faas-imaged faas-builderd faas-schedd faas-vmmd faas-gatewayd-internal faas-gatewayd-public; do
  if systemctl is-active --quiet "$service"; then
    die active_services "refusing to share the host with active $service"
  fi
done
cd "$repo_root"
# Real base conversion, builder drives, source exports and Go compilation
# share this disposable host. Refuse the journey before allocating them.
required_kib=33554432
for volume in "$repo_root" "${TMPDIR:-/tmp}"; do
  free_kib="$(df -Pk "$volume" | awk 'NR == 2 {print $4}')"
  [[ "$free_kib" =~ ^[0-9]+$ && "$free_kib" -ge "$required_kib" ]] ||
    die insufficient_disk "at least 32 GiB free is required on the checkout and temporary volumes"
done
revision="$(git rev-parse HEAD)"
[[ -z "$(git status --porcelain)" ]] || die dirty_source "source checkout must be clean"
preflight=pass
reason=leak_before_failed
if make leakcheck >"$report_dir/leak-before.log" 2>&1; then leak_before=pass; else leak_before=fail; exit 1; fi
reason=journey_failed
if GOMEMLIMIT=6GiB GREGALE_GITOPS_NATIVE_ACCEPTANCE=1 GREGALE_GITOPS_ACCEPTANCE=1 \
  make GO="$go_binary" PKGS=./cmd/e2e RUN_REGEX='^TestEnvironmentGitOps(NativeMetal|SourceJobsNativeMetal)$' \
  RUN_ARGS='-json -timeout 300m' test-metal >"$report_dir/test-events.jsonl" 2>"$report_dir/runner.log"; then
  journey=pass
else
  journey=fail
fi
if make leakcheck >"$report_dir/leak-after.log" 2>&1; then leak_after=pass; else leak_after=fail; fi
if [[ "$(git rev-parse HEAD)" == "$revision" && -z "$(git status --porcelain)" ]]; then source_check=pass; else source_check=fail; fi
[[ "$journey" == pass ]] || exit 1
[[ "$leak_after" == pass ]] || die leak_after_failed "post-run leak gate failed"
[[ "$source_check" == pass ]] || die source_changed "source checkout changed during the run"
reason=""
# finish validates every required structured test result and immutable input.
