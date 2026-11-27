#!/usr/bin/env bash
# Functional real-KVM Run matrix. Does not qualify a production runtime release.
# Invoke the exact source archive on a designated, idle acceptance host as root.
set -Eeuo pipefail
umask 077
die() { echo "Run lifecycle acceptance: $*" >&2; exit 1; }
cohort=${FAAS_RUN_LIFECYCLE_COHORT:-baseline}
case "$cohort" in
  controls|tasks|baseline|warm-oom|template-clones|native-recovery|files-contracts|extended-environments|system-environments|report-agent-json|report-agent-files|report-agent-hosted|report-agent-setup|report-agent-recovery|report-agent-background) ;;
  *) die 'unknown qualification cohort' ;;
esac
[[ $EUID == 0 && $(uname -sm) == 'Linux x86_64' ]] || die 'root on Linux x86_64 is required'
[[ -c /dev/kvm && -f /sys/fs/cgroup/cgroup.controllers ]] || die 'KVM and cgroups v2 are required'
[[ -f /etc/faas/builder-acceptance-host ]] || die 'host is not designated for disruptive acceptance'
: "${FAAS_RUN_LIFECYCLE_SOURCE_SHA:?select the exact source snapshot commit}"
: "${FAAS_RUN_LIFECYCLE_GO:?select the pinned Go executable}"
: "${FAAS_RUN_LIFECYCLE_OUTPUT:?select a fresh evidence directory}"
[[ $FAAS_RUN_LIFECYCLE_SOURCE_SHA =~ ^[0-9a-f]{40}$ ]] || die 'invalid source SHA'
[[ $FAAS_RUN_LIFECYCLE_OUTPUT =~ ^/mnt/e2e-scratch/run-lifecycle-[A-Za-z0-9._-]+/evidence$ ]] || die 'output must use the private acceptance staging namespace'
[[ ! -e $FAAS_RUN_LIFECYCLE_OUTPUT ]] || die 'evidence directory already exists'
for tool in flock firecracker jailer debugfs e2fsck cp gcc ip iptables nft tc mkfs.ext4 truncate python3 systemctl psql tar; do
  command -v "$tool" >/dev/null || die "missing host tool: $tool"
done
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"
[[ $(cat .faas-run-lifecycle-source-sha) == "$FAAS_RUN_LIFECYCLE_SOURCE_SHA" ]] || die 'archive marker does not match selected source'
expected_go=$(awk '$1 == "go" {language=$2} $1 == "toolchain" {compiler=$2; sub(/^go/, "", compiler)} END {print compiler ? compiler : language}' go.mod)
[[ $($FAAS_RUN_LIFECYCLE_GO version) == "go version go${expected_go} linux/amd64" ]] || die 'Go version does not match source go.mod'
if [[ ( $cohort == native-recovery || $cohort == system-environments || $cohort == report-agent-* ) && ${FAAS_RUN_NATIVE_PRIVATE_MOUNT_NS:-} != 1 ]]; then
  command -v unshare >/dev/null || die 'unshare is required for private native recovery acceptance'
  exec unshare --mount --propagation private env FAAS_RUN_NATIVE_PRIVATE_MOUNT_NS=1 bash "$0"
fi
if [[ $cohort == native-recovery || $cohort == system-environments || $cohort == report-agent-json || $cohort == report-agent-files || $cohort == report-agent-background ]]; then
  export FAAS_RUN_LIFECYCLE_NATIVE_RECOVERY=1
fi
exec 9>/var/lock/faas-builder-acceptance.lock
lock_wait=${FAAS_RUN_LIFECYCLE_LOCK_WAIT_SECONDS:-30}
[[ $lock_wait =~ ^[1-9][0-9]{0,3}$ ]] || die 'lock wait must be 1..7200 seconds'
((10#$lock_wait <= 7200)) || die 'lock wait must be 1..7200 seconds'
flock -w "$lock_wait" 9 || die 'another acceptance run holds the host lock'
for unit in faas-apid faas-schedd faas-vmmd faas-imaged faas-builderd faas-gatewayd-internal faas-gatewayd-public faas-realtimed faas-outboundd; do
  if systemctl is-active --quiet "$unit"; then die "drain the acceptance host first: $unit is active"; fi
done
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  [[ ${target##*/} != firecracker* ]] || die 'a Firecracker workload is already running'
done
env_file=/etc/faas/e2e-acceptance.env
[[ $(stat -c '%a %U' "$env_file") == '600 root' ]] || die 'dedicated PostgreSQL configuration must be root-owned 0600'
# shellcheck disable=SC1090
source "$env_file"
: "${FAAS_E2E_DATABASE_URL:?dedicated acceptance PostgreSQL is required}"
export DATABASE_URL=$FAAS_E2E_DATABASE_URL
[[ -z ${FAAS_SKIP_PG_TESTS:-} ]] || die 'PostgreSQL may not be skipped'
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null
[[ $(psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atqc "SELECT has_database_privilege(current_database(), 'CREATE') AND to_regclass('public.goose_db_version') IS NULL") == t ]] || die 'dedicated database must allow isolated schemas and have no public migration ledger'
output=$FAAS_RUN_LIFECYCLE_OUTPUT
stage=${output%/evidence}
mkdir -p "$output" "$stage/work" "$stage/bin"
chmod 0755 "$stage" "$stage/bin"
export TMPDIR=$stage/work
export FAAS_TEST_RUN_FIXTURE_ROOT=$stage/work/source-fixture
export FAAS_E2E_BIN_DIR=$stage/bin
export GOCACHE=/var/cache/faas-native-e2e/go-build
export GOMODCACHE=/var/cache/faas-native-e2e/go-mod
export GOPATH=/var/cache/faas-native-e2e
export GOTOOLCHAIN=local GOMEMLIMIT=6GiB
go_bin_dir=$(dirname "$FAAS_RUN_LIFECYCLE_GO")
export PATH="$go_bin_dir:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# Match the repository's bounded compiler settings for the largest packages.
export GOFLAGS='-p=1 -trimpath -buildvcs=false "-gcflags=github.com/onebox-faas/faas/pkg/state=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/sched=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false"'
export FAAS_TEST_KERNEL=${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.134}
export FAAS_TEST_FC_VERSION=${FAAS_TEST_FC_VERSION:-1.7.0}
fc_version=$(firecracker --version 2>/dev/null)
jailer_version=$(jailer --version 2>/dev/null)
[[ ${fc_version%%$'\n'*} == "Firecracker v$FAAS_TEST_FC_VERSION" ]] || die 'Firecracker version does not match the selected fixture'
[[ ${jailer_version%%$'\n'*} == "Jailer v$FAAS_TEST_FC_VERSION" ]] || die 'jailer version does not match the selected fixture'
if [[ $cohort == extended-environments || $cohort == system-environments || $cohort == report-agent-hosted || $cohort == report-agent-setup || $cohort == report-agent-recovery ]]; then
  FAAS_PUBLIC_IFACE=$(ip -json route show default | python3 -c 'import json,sys; values={r["dev"] for r in json.load(sys.stdin) if r.get("dst")=="default" and r.get("dev")}; assert len(values)==1, "one outward interface is required"; print(next(iter(values)))')
  [[ $FAAS_PUBLIC_IFACE =~ ^[A-Za-z0-9_.:-]+$ ]] || die 'invalid outward interface'
  ip link show dev "$FAAS_PUBLIC_IFACE" >/dev/null || die 'outward interface unavailable'
  export FAAS_PUBLIC_IFACE
fi
export FAAS_TEST_REFERENCE_SSD=0
# The dedicated acceptance role needs CREATE on its database, not CREATEDB.
unset FAAS_PGTEST_TEMPLATE_DATABASE
source_rc=125 oci_rc=125 warm_oom_rc=125 before_rc=125 after_rc=125
cleanup() {
  rc=$?
  trap - EXIT INT TERM
  set +e
  local fixture_cleanup_rc=0
  if declare -F cleanup_hosted_report >/dev/null; then
    cleanup_hosted_report || { fixture_cleanup_rc=1; rc=1; }
  fi
  bash deploy/scripts/leakcheck.sh >"$output/leakcheck-after.log" 2>&1
  after_rc=$?
  python3 - "$output" "$cohort" "$source_rc" "$oci_rc" "$warm_oom_rc" "$before_rc" "$after_rc" <<'PY'
import json,sys
from pathlib import Path
source,oci,warm,before,after=map(int,sys.argv[3:])
cohort=sys.argv[2]
exits={"source":source,"oci":oci} if cohort=="baseline" else {cohort.replace("-","_"):warm}
exits.update(leakcheck_before=before,leakcheck_after=after)
Path(sys.argv[1],"process-exits.json").write_text(json.dumps(exits,indent=2)+"\n")
PY
  if ((rc == 0 && after_rc == 0)); then
    python3 scripts/ci/run-lifecycle-verdict.py --cohort "$cohort" "$output" || rc=1
  else
    rc=1
  fi
  if ((after_rc == 0 && fixture_cleanup_rc == 0)); then
    rm -rf --one-file-system "$stage/work"
  else
    printf '%s\n' 'Fixture cleanup or final leakcheck failed. Preserve staging and investigate owned resources under the acceptance lock.' >"$stage/RECOVERY_REQUIRED"
  fi
  echo "Run lifecycle acceptance: evidence=$output exit=$rc"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
set +e
bash deploy/scripts/leakcheck.sh >"$output/leakcheck-before.log" 2>&1
before_rc=$?
set -e
((before_rc == 0)) || die 'initial leakcheck failed; host resources were not changed'
python3 - "$output" "$FAAS_RUN_LIFECYCLE_SOURCE_SHA" "$FAAS_RUN_LIFECYCLE_GO" "$cohort" <<'PY'
import hashlib,json,platform,subprocess,sys
from pathlib import Path
out,source,go=Path(sys.argv[1]),sys.argv[2],Path(sys.argv[3])
virt=subprocess.run(["systemd-detect-virt"],capture_output=True,text=True)
if virt.returncode not in (0,1) or not virt.stdout.strip(): raise SystemExit("virtualization probe failed")
host={"hostname":platform.node(),"architecture":platform.machine(),"host_kernel":platform.release(),"virtualization":virt.stdout.strip(),"machine_id":Path("/etc/machine-id").read_text().strip(),"boot_id":Path("/proc/sys/kernel/random/boot_id").read_text().strip(),"kvm":True,"acceptance_marker":True}
(out/"host.json").write_text(json.dumps(host,indent=2)+"\n")
source_record={"snapshot_commit":source,"go_version":subprocess.check_output([str(go),"version"],text=True).strip(),"go_sha256":hashlib.sha256(go.read_bytes()).hexdigest()}
if sys.argv[4]=="files-contracts":
    manifest=Path("run-files-source.json")
    if manifest.exists():
        delta=json.loads(manifest.read_text())
        if delta.get("base")!=source: raise SystemExit("file/contract source base mismatch")
        for name,digest in delta.get("files",{}).items():
            p=Path(name)
            if p.is_absolute() or ".." in p.parts or p.is_symlink() or hashlib.sha256(p.read_bytes()).hexdigest()!=digest: raise SystemExit("file/contract source delta mismatch")
        source_record.pop("snapshot_commit")
        source_record.update(base_snapshot_commit=source,delta_manifest_sha256=hashlib.sha256(manifest.read_bytes()).hexdigest(),delta_files=delta["files"])
if sys.argv[4] in ("extended-environments","system-environments","tasks","controls","report-agent-json","report-agent-files","report-agent-hosted","report-agent-setup","report-agent-recovery","report-agent-background"):
    manifest=Path("run-environment-source-manifest.json")
    raw=manifest.read_bytes()
    record=json.loads(raw)
    for name,digest in record["files"].items():
        p=Path(name)
        if p.is_absolute() or ".." in p.parts or p.is_symlink() or hashlib.sha256(p.read_bytes()).hexdigest()!=digest: raise SystemExit("environment source manifest mismatch: "+name)
    source_record.pop("snapshot_commit", None)
    source_record.update(base_snapshot_commit=source,source_manifest_sha256=hashlib.sha256(raw).hexdigest(),source_files=len(record["files"]))
(out/"source.json").write_text(json.dumps(source_record,indent=2)+"\n")
PY
echo 'Run lifecycle acceptance: building exact-source daemons and guest init'
export FAAS_GUEST_INIT=$stage/bin/guest-init
CGO_ENABLED=0 "$FAAS_RUN_LIFECYCLE_GO" build -ldflags=-w -o "$FAAS_GUEST_INIT" ./guest/init >"$output/build.log" 2>&1
# All harness daemons are fresh; no previously staged executable is adopted.
for daemon in apid schedd vmmd imaged gatewayd-internal gatewayd-public meterd builderd vmmd-stream-bridge vmmd-raw-bridge vmmd-tcp-bridge vmmd-udp-bridge vmmd-jail-helper vmmd-guestdisk gregale; do
  echo "building $daemon" >>"$output/build.log"
  CGO_ENABLED=0 "$FAAS_RUN_LIFECYCLE_GO" build -tags metal -ldflags=-w -o "$stage/bin/$daemon" "./cmd/$daemon" >>"$output/build.log" 2>&1
done
chmod 0755 "$stage"/bin/*
echo 'Run lifecycle acceptance: staging a private Node 24 base with exact-source guest init'
export FAAS_TEST_EXECUTION_BASE=$stage/work/run-node24.ext4
cp --reflink=auto --sparse=always /srv/fc/base/runner-node24-amd64.ext4 "$FAAS_TEST_EXECUTION_BASE"
chmod 0600 "$FAAS_TEST_EXECUTION_BASE"
# debugfs resolves links for reads, but its write commands require a real parent.
# Accept only the two layouts used by the pinned runtime bases.
sbin_stat=$(debugfs -R 'stat /sbin' "$FAAS_TEST_EXECUTION_BASE" 2>>"$output/build.log")
case "$sbin_stat" in
  *'Type: directory'*) init_path=/sbin/init ;;
  *'Fast link dest: "usr/sbin"'*|*'Fast link dest: "/usr/sbin"'*) init_path=/usr/sbin/init ;;
  *) die 'unsupported base /sbin layout' ;;
esac
{
  debugfs -w -R "rm $init_path" "$FAAS_TEST_EXECUTION_BASE"
  debugfs -w -R "write $FAAS_GUEST_INIT $init_path" "$FAAS_TEST_EXECUTION_BASE"
  debugfs -w -R "set_inode_field $init_path mode 0100755" "$FAAS_TEST_EXECUTION_BASE"
  debugfs -R "dump /sbin/init $stage/work/staged-init" "$FAAS_TEST_EXECUTION_BASE"
} >>"$output/build.log" 2>&1
cmp "$FAAS_GUEST_INIT" "$stage/work/staged-init" || die 'guest init staging did not preserve exact bytes'
e2fsck -fn "$FAAS_TEST_EXECUTION_BASE" >>"$output/build.log" 2>&1 || die 'staged base filesystem is inconsistent'
chmod 0444 "$FAAS_TEST_EXECUTION_BASE"
if [[ $cohort == report-agent-background ]]; then
  source scripts/ci/run-hosted-report-fixture.sh
  prepare_report_bundle
  prepare_report_database
  echo 'Run acceptance: TestSourceHostedBackgroundReportMetal'
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=15m -run '^TestSourceHostedBackgroundReportMetal$' ./pkg/e2etest/runlifecycle >"$output/$cohort.jsonl" 2>"$output/$cohort.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'hosted Report Agent background delivery failed'
  exit 0
fi
if [[ $cohort == report-agent-hosted || $cohort == report-agent-setup || $cohort == report-agent-recovery ]]; then
  # The sourced fixture registers its database and private IP cleanup with
  # the same exit trap before starting anything. We still hold FD 9.
  source scripts/ci/run-hosted-report-fixture.sh
  prepare_hosted_report
  if [[ $cohort != report-agent-hosted ]]; then
    export FAAS_TEST_REPORT_SETUP_BIN="$stage/bin"
    if [[ $cohort == report-agent-recovery ]]; then
      export FAAS_TEST_REPORT_RECOVERY=1
    else
      export FAAS_TEST_REPORT_SETUP=1
    fi
    : "${FAAS_TEST_REPORT_SETUP_NPM:?select the verified npm CLI script for Node 24}"
    ln -s "$FAAS_TEST_REPORT_AGENT_NODE" "$stage/bin/node"
    ln -s "$FAAS_TEST_REPORT_SETUP_NPM" "$stage/bin/npm"
    sha256sum "$FAAS_TEST_REPORT_SETUP_NPM" >>"$output/artifacts.sha256"
  fi
  echo 'Run acceptance: TestDeployedReportAgentMetal'
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=30m -run '^TestDeployedReportAgentMetal$' ./pkg/e2etest/runlifecycle >"$output/$cohort.jsonl" 2>"$output/$cohort.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'deployed Report Agent journey failed'
  exit 0
fi
if [[ $cohort == report-agent-json || $cohort == report-agent-files ]]; then
  : "${FAAS_TEST_REPORT_AGENT_NODE:?select a pinned Node 24 executable}"
  [[ -x $FAAS_TEST_REPORT_AGENT_NODE ]] || die 'Report Agent Node 24 is missing'
  sha256sum "$FAAS_TEST_REPORT_AGENT_NODE" "$FAAS_TEST_EXECUTION_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
  test_name=TestSourceHostedReportAgentMetal
  if [[ $cohort == report-agent-files ]]; then test_name=TestSourceScopedReportFileMetal; fi
  echo "Run acceptance: $test_name"
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=15m -run "^${test_name}$" ./pkg/e2etest/runlifecycle >"$output/$cohort.jsonl" 2>"$output/$cohort.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'hosted Report Agent journey failed'
  exit 0
fi
if [[ $cohort == tasks || $cohort == controls ]]; then
  test_name=TestSourceTaskContinuationMetal
  [[ $cohort != controls ]] || test_name=TestSourceRunControlsContinuationMetal
  export FAAS_RUN_TASK_QUALIFICATION=1
  sha256sum "$FAAS_TEST_EXECUTION_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=15m -run "^${test_name}$" ./pkg/e2etest/runlifecycle >"$output/$cohort.jsonl" 2>"$output/$cohort.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'versioned task/agent continuation journey failed'
  exit 0
fi
if [[ $cohort == extended-environments || $cohort == system-environments ]]; then
  export FAAS_TEST_ENVIRONMENT_BUILDER_BASE=$stage/work/run-builder.ext4
  cp --reflink=auto --sparse=always /srv/fc/base/runner-builder-amd64.ext4 "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
  chmod 0600 "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
  builder_sbin=$(debugfs -R 'stat /sbin' "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE" 2>>"$output/build.log")
  case "$builder_sbin" in
    *'Type: directory'*) builder_init=/sbin/init ;;
    *'Fast link dest: "usr/sbin"'*|*'Fast link dest: "/usr/sbin"'*) builder_init=/usr/sbin/init ;;
    *) die 'unsupported builder /sbin layout' ;;
  esac
  {
    debugfs -w -R "rm $builder_init" "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
    debugfs -w -R "write $FAAS_GUEST_INIT $builder_init" "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
    debugfs -w -R "set_inode_field $builder_init mode 0100755" "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
    debugfs -R "dump /sbin/init $stage/work/staged-builder-init" "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
  } >>"$output/build.log" 2>&1
  cmp "$FAAS_GUEST_INIT" "$stage/work/staged-builder-init" || die 'builder init staging changed bytes'
  e2fsck -fn "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE" >>"$output/build.log" 2>&1 || die 'staged builder base is inconsistent'
  chmod 0444 "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE"
  sha256sum "$FAAS_TEST_EXECUTION_BASE" "$FAAS_TEST_ENVIRONMENT_BUILDER_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
  echo 'Run acceptance: five-minute class and real dependency environment build/publication journey'
  set +e
  environment_test='^TestSourceExtendedEnvironmentMetal$'
  if [[ $cohort == system-environments ]]; then
    : "${FAAS_TEST_SYSTEM_ENVIRONMENT_BASE_REF:?select a digest-pinned Wolfi installer base}"
    : "${FAAS_TEST_SYSTEM_ENVIRONMENT_PACKAGES:?select exact Python and Poppler package pins}"
    environment_test='^TestSourceSystemEnvironmentMetal$'
  fi
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=30m -run "$environment_test" ./pkg/e2etest/runlifecycle >"$output/$cohort.jsonl" 2>"$output/$cohort.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'extended/environment journey failed; inspect retained logs'
  exit 0
fi
if [[ $cohort == files-contracts ]]; then
  sha256sum "$FAAS_TEST_EXECUTION_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
  echo 'Run acceptance: stored-file, contract, CLI and concurrent warm-clone journey'
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=15m -run '^TestSourceFilesContractsMetal$' ./pkg/e2etest/runlifecycle >"$output/files-contracts.jsonl" 2>"$output/files-contracts.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'stored-file/contract journey failed; inspect retained logs'
  exit 0
fi
if [[ $cohort == warm-oom || $cohort == template-clones || $cohort == native-recovery ]]; then
  sha256sum "$FAAS_TEST_EXECUTION_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
  echo 'Run lifecycle acceptance: checking guest resume fence and OOM attribution'
  "$FAAS_RUN_LIFECYCLE_GO" test -json -race -count=1 -timeout=3m -run '^(TestExecutionPayloadRevokesPristineRestoreFence|TestResumeWithoutEntropyNacks|TestResumeCapabilityAdvertisement|TestServeExecutionOnceHandlesOneRequest)$' ./guest/init >"$output/warm-oom-guest-checks.jsonl" 2>"$output/warm-oom-guest-checks.stderr"
  "$FAAS_RUN_LIFECYCLE_GO" test -json -race -count=1 -timeout=3m -run '^(TestGuestOOMCounterRequiresValidEvidence|TestExecutionOOMNeedsKernelCounterAndKilledProcess)$' ./guest/executor >"$output/warm-oom-executor-checks.jsonl" 2>"$output/warm-oom-executor-checks.stderr"
  if [[ $cohort == template-clones || $cohort == native-recovery ]]; then
    echo 'Run lifecycle acceptance: checking production template publication and retention'
    "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=8m -run '^Test(ExecutionTemplate|RuntimeSnapshot|MemoryRuntimeSnapshot|StorageRuntimeSnapshot|StateRuntimeSnapshot|ExecutionClaimResolver|ExecutionProfileSnapshot|VMMClientExecutionRestoreWaitsForReconnectWithoutReplaying|VMMClientDestroyWaitsForReconnectWithoutReplaying|ExecutionSessionDestroyRequiresConfirmedRetirement|VmmdExecutionBackend)' ./pkg/sched >"$output/template-publisher-checks.jsonl" 2>"$output/template-publisher-checks.stderr"
    "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=3m -run '^Test(ExecutionTemplate|AdoptionStateInventoryClassifiesEvery|NativeRuntimeCapture|NativeSnapshotCapturePermit|ManagerRequestsNativeRuntimeCapture|NativeLaunchOwner|NativeJailDevice|NativeImage|NativeRecovery)' ./pkg/fcvm >"$output/template-runtime-checks.jsonl" 2>"$output/template-runtime-checks.stderr"
    "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=3m -run '^TestCallerPolicy' ./pkg/vmmdgrpc >"$output/template-auth-checks.jsonl" 2>"$output/template-auth-checks.stderr"
    "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=3m -run '^TestExecutionTemplateShapesFailClosed$' ./cmd/schedd >"$output/template-wiring-checks.jsonl" 2>"$output/template-wiring-checks.stderr"
    "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=3m -run '^TestExecutionTemplateGCProtectsCatalogAndReclaimsOnlyOldOrphans$' ./pkg/imaged >"$output/template-retention-checks.jsonl" 2>"$output/template-retention-checks.stderr"
    if [[ $cohort == native-recovery ]]; then
      "$FAAS_RUN_LIFECYCLE_GO" test -json -race -ldflags=-w -count=1 -timeout=3m ./pkg/jailsetup >"$output/template-device-checks.jsonl" 2>"$output/template-device-checks.stderr"
    fi
    echo 'Run lifecycle acceptance: running production templates, concurrent clones and warm/OOM regression'
    tests='^(TestSourceTemplateClonesMetal|TestSourceWarmRestoreOOMMetal)$'
    if [[ $cohort == native-recovery ]]; then tests='^(TestSourceTemplateClonesMetal|TestSourceWarmRestoreOOMMetal|TestSourceNativeRecoveryMetal)$'; fi
    set +e
    "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=20m -run "$tests" ./pkg/e2etest/runlifecycle >"$output/template-clones.jsonl" 2>"$output/template-clones.stderr"
    warm_oom_rc=$?
    set -e
    ((warm_oom_rc == 0)) || die 'template/clone matrix failed; inspect retained logs'
    exit 0
  fi
  echo 'Run lifecycle acceptance: running warm restore and real guest OOM matrix'
  set +e
  "$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=12m -run '^TestSourceWarmRestoreOOMMetal$' ./pkg/e2etest/runlifecycle >"$output/warm-oom.jsonl" 2>"$output/warm-oom.stderr"
  warm_oom_rc=$?
  set -e
  ((warm_oom_rc == 0)) || die 'warm restore/OOM matrix failed; inspect retained logs'
  exit 0
fi
# OCI fixtures must never publish test bases into the host's shared store.
# Source tests override this again with their own per-fixture local store.
export FAAS_STORAGE_BACKEND=local FAAS_STORAGE_ROOT=$stage/work/oci-storage
mkdir -p "$FAAS_STORAGE_ROOT/kernel"
cp --reflink=auto "$FAAS_TEST_KERNEL" "$FAAS_STORAGE_ROOT/kernel/$FAAS_TEST_FC_VERSION"
chmod 0444 "$FAAS_STORAGE_ROOT/kernel/$FAAS_TEST_FC_VERSION"
# Export the operator's minimal userland as an explicit OCI test fixture.
# imaged will produce its private base and inject the exact-source guest init;
# no shared ext4 or provenance records are imported into the test database.
mkdir -p "$stage/work/oci-base-root"
debugfs -R "rdump / $stage/work/oci-base-root" /srv/fc/base/base-amd64.ext4 >>"$output/build.log" 2>&1
chmod 0755 "$stage/work/oci-base-root"
export FAAS_TEST_RUN_MINIMAL_BASE_LAYER=$stage/work/oci-minimal.tar.gz
tar -C "$stage/work/oci-base-root" -czf "$FAAS_TEST_RUN_MINIMAL_BASE_LAYER" .
sha256sum /srv/fc/base/base-amd64.ext4 "$FAAS_TEST_RUN_MINIMAL_BASE_LAYER" >"$output/oci-fixture.sha256"
sha256sum "$FAAS_TEST_EXECUTION_BASE" "$FAAS_GUEST_INIT" "$FAAS_TEST_KERNEL" "$(command -v firecracker)" "$(command -v jailer)" "$stage"/bin/* >"$output/artifacts.sha256"
echo 'Run lifecycle acceptance: checking daemon liveness and subscription readiness'
"$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -count=1 -timeout=2m -run '^(TestFirstDeadDaemon.*|TestRequireDaemonsAliveSkipsDeliberateKills|TestSessionListens_RequiresALISTEN)$' ./pkg/e2etest >"$output/harness-checks.jsonl" 2>"$output/harness-checks.stderr"
"$FAAS_RUN_LIFECYCLE_GO" test -json -race -count=1 -timeout=3m -run '^(TestMaterializeJob.*|TestMaterializePendingJobs.*)$' ./pkg/imaged >"$output/job-materialization-checks.jsonl" 2>"$output/job-materialization-checks.stderr"
echo 'Run lifecycle acceptance: running source lifecycle matrix'
set +e
"$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -timeout=15m -run '^TestSourceRunLifecycleMetal$' ./pkg/e2etest/runlifecycle >"$output/source.jsonl" 2>"$output/source.stderr"
source_rc=$?
set -e
((source_rc == 0)) || die 'source lifecycle matrix failed; inspect retained logs'
echo 'Run lifecycle acceptance: running OCI and interactive lifecycle matrix'
set +e
"$FAAS_RUN_LIFECYCLE_GO" test -json -tags metal -race -ldflags=-w -count=1 -failfast -timeout=20m -run '^(TestImageRunsMetal|TestImageRunProcessMetal|TestInteractiveImageRunMetal)$' ./cmd/e2e >"$output/oci.jsonl" 2>"$output/oci.stderr"
oci_rc=$?
set -e
((oci_rc == 0)) || die 'OCI lifecycle matrix failed; inspect retained logs'
