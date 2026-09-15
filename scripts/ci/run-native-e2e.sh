#!/usr/bin/env bash
# Run the whole end-to-end platform suite (./cmd/e2e, metal build tag) on the
# designated native KVM host: apid -> builderd -> builder microVM -> imaged ->
# schedd -> vmmd -> jailer -> Firecracker -> snapshot -> park -> gateway wake.
#
# The caller supplies an exact source archive and a pinned Go toolchain. This
# script owns host locking, pre-flight, fixture staging, service quiescing,
# cleanup, and service restoration so an interrupted run cannot leave the node
# in a test state.
#
# Companion to run-native-metal-smoke.sh, which runs the pkg/fcvm package. That
# gate proves a microVM boots; this one proves the product does.

set -Eeuo pipefail

die() {
  echo "native e2e: $*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die "must run as root"

: "${FAAS_E2E_SOURCE_SHA:?set FAAS_E2E_SOURCE_SHA to the tested commit}"
: "${FAAS_E2E_GO:?set FAAS_E2E_GO to the pinned Go binary}"

[[ "${FAAS_E2E_SOURCE_SHA}" =~ ^[0-9a-f]{40}$ ]] ||
  die "source SHA must be 40 lowercase hex characters"
[[ -x "${FAAS_E2E_GO}" ]] || die "Go binary is not executable: ${FAAS_E2E_GO}"
[[ -c /dev/kvm ]] || die "/dev/kvm is unavailable"
[[ "$(uname -m)" == "x86_64" ]] || die "the production e2e gate requires x86_64"
[[ -f /etc/faas/builder-acceptance-host ]] ||
  die "/etc/faas/builder-acceptance-host is missing; this node is not designated for disruptive acceptance tests"

# debugfs + mkfs.ext4 back imaged's ext4 assembly, tc the per-instance rate
# limits, nft/iptables the tenant egress policy, gcc the race detector
# (-race needs cgo), and pg_isready/psql the Postgres pre-flight below.
for tool in debugfs e2fsck firecracker flock gcc ip iptables jailer make \
  mkfs.ext4 nft pg_isready psql readlink systemctl systemd-run tar tc \
  truncate; do
  command -v "${tool}" >/dev/null || die "required host tool is missing: ${tool}"
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source "${repo_root}/scripts/ci/native-e2e-verdict.sh"
# shellcheck source=scripts/ci/native-e2e-phases.sh
source "${repo_root}/scripts/ci/native-e2e-phases.sh"
marker_sha="$(tr -d '\n' < "${repo_root}/.faas-e2e-source-sha")"
[[ "${marker_sha}" == "${FAAS_E2E_SOURCE_SHA}" ]] ||
  die "source archive marker ${marker_sha} does not match ${FAAS_E2E_SOURCE_SHA}"

kernel="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.134}"
builder_base="${FAAS_BUILDER_BASE_PATH:-/srv/fc/base/builder-base.ext4}"
fc_version="${FAAS_TEST_FC_VERSION:-1.7.0}"
run_id="${FAAS_E2E_RUN_ID:-manual}"
[[ "${run_id}" =~ ^[A-Za-z0-9._-]+$ ]] || die "run ID contains unsupported characters"
transfer_root="${FAAS_E2E_TRANSFER_ROOT:-}"
if [[ -n "${transfer_root}" && ! "${transfer_root}" =~ ^/var/tmp/faas-native-e2e-[A-Za-z0-9._-]+$ ]]; then
  die "transfer root is outside the native e2e staging namespace"
fi

stage_root="/srv/fc/acceptance/e2e-${FAAS_E2E_SOURCE_SHA}-${run_id}"
guest_init="${stage_root}/faas-guest-init"
active_services="${stage_root}/active-services"
cache_root="/var/cache/faas-native-e2e"
# The suite drives the platform through its own per-test sockets, ports and
# Postgres schema, but vmmd, jailer, cgroups, netns and the tenant IP leases
# are host-global. A production daemon left running would fight the test VMs
# and make the closing leak check meaningless, so the whole local stack is
# quiesced for the duration. Only units that were actually active are stopped,
# and every one of them is restarted on every exit path.
candidate_services=(faas-apid faas-schedd faas-vmmd faas-builderd faas-imaged
  faas-gatewayd-internal faas-gatewayd-public faas-realtimed faas-outboundd)

# Postgres is the one thing that must stay up: cmd/e2e is a database-backed
# suite. pgtest.Open SKIPS every Postgres-backed test when DATABASE_URL is
# unreachable, which would turn this whole gate green while testing nothing —
# so an unreachable cluster is a hard failure here, never a skip.
#
# The DSN is host-owned, not passed down from CI: a DSN on the gcloud ssh
# command line would be visible in the node's process list. Drop it in
# /etc/faas/e2e-acceptance.env (root-owned, 0600) as FAAS_E2E_DATABASE_URL.
e2e_env_file=/etc/faas/e2e-acceptance.env
if [[ -f "${e2e_env_file}" ]]; then
  env_perms="$(stat -c '%a %U' "${e2e_env_file}")"
  [[ "${env_perms}" == "600 root" ]] ||
    die "${e2e_env_file} must be root-owned mode 0600 (found: ${env_perms})"
  # shellcheck disable=SC1090
  source "${e2e_env_file}"
  # If the host bothered to write this file, its DSN wins — falling back to the
  # default would point the suite at a DIFFERENT cluster than the operator
  # chose, silently. Observed for real on 2026-09-12: the DSN contains an
  # unescaped `&`, and written unquoted it makes the shell background the
  # assignment in a subshell, so the variable arrives here EMPTY. Quote the
  # value in the env file.
  [[ -n "${FAAS_E2E_DATABASE_URL:-}" ]] ||
    die "${e2e_env_file} exists but FAAS_E2E_DATABASE_URL is empty after sourcing it.
  The value must be quoted — the DSN contains an '&', and unquoted the shell
  parses it as a background job plus a stray command:
    FAAS_E2E_DATABASE_URL='postgres:///faas_e2e?host=/run/postgresql&user=faas'"
fi
database_url="${FAAS_E2E_DATABASE_URL:-${DATABASE_URL:-postgres:///faas_e2e?host=/run/postgresql&user=faas}}"

# Artifact storage: the harness daemons MUST resolve runtime bases the way the
# node's own daemons do. Without this the harness falls back to pkg/storage's
# default local backend rooted at /srv/fc — and on an OCI-backed node that
# directory holds no scan sidecars at all, so vmmd's issue #299 admission gate
# refuses every cold boot with "scan sidecar missing". Observed on 2026-09-14:
# /srv/fc/scans was empty while the builder base's sidecar sat in the node's
# OCI store, correctly staged and CRITICAL-clean. The base was never missing;
# the harness was simply looking in a different store than the one imaged
# staged into.
#
# Export rather than re-derive: these are the same values the production units
# load via EnvironmentFile, so the harness exercises the real storage route
# (OCI + read-through cache) instead of a test-only one. Secrets stay in the
# process environment exactly as systemd delivers them — never on a command
# line, never logged. Only key NAMES are printed below.
storage_env_file=/etc/faas/storage.env
if [[ -f "${storage_env_file}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${storage_env_file}"
  set +a
  # Same failure shape as the DSN above: a value containing '&' written
  # unquoted makes the shell background the assignment, and the variable
  # arrives here empty. Fail loudly rather than silently falling back to the
  # local backend, which is precisely the bug this block exists to prevent.
  [[ -n "${FAAS_STORAGE_BACKEND:-}" ]] ||
    die "${storage_env_file} exists but FAAS_STORAGE_BACKEND is empty after sourcing it;
  quote any value containing '&' or '#'"
  echo "native e2e: storage backend for harness daemons: ${FAAS_STORAGE_BACKEND}"
  echo "native e2e: storage keys exported: $(cut -d= -f1 "${storage_env_file}" | grep -E '^FAAS_' | paste -sd, -)"
else
  echo "native e2e: no ${storage_env_file}; harness daemons use the default local backend at /srv/fc"
fi

# Outward NIC for tenant egress NAT. vmmd defaults this to "eth0"
# (pkg/netns.DefaultHostPolicy.PublicIface); production overrides it per host
# through a systemd drop-in, because the name is provider-specific — this node
# has no eth0 at all, its NIC is ens4. Without the override the harness's vmmd
# installs its masquerade rule against an interface that does not exist, so a
# builder microVM boots correctly and then has no egress. It dies at guest-init's
# 5s DNS preflight:
#
#   guest-init: build failed: registry DNS preflight: signal: killed
#
# which reads like a broken build (0-byte build.log, failure_class=user_error)
# rather than a NAT rule pointed at a missing NIC. Observed 2026-09-14 on every
# build of dispatch 34904036016.
#
# Prefer the value production uses on THIS host; fall back to the interface the
# default route actually leaves by, which is what the setting means.
#
# The `|| true` is load-bearing, not defensive noise. A DEDICATED acceptance
# host runs no vmmd service, so /etc/systemd/system/faas-vmmd.service.d does
# not exist; grep exits 1, `set -o pipefail` propagates that out of the command
# substitution, and `set -e` kills the runner before a single test runs. That
# is exactly what happened on faas-acceptance-1's first dispatch (34954126133,
# exit code 2, two lines of log). The dual-use node hid it because the
# directory exists there.
if [[ -z "${FAAS_PUBLIC_IFACE:-}" ]]; then
  FAAS_PUBLIC_IFACE="$(
    {
      grep -rhoE 'FAAS_PUBLIC_IFACE=[A-Za-z0-9._-]+' \
        /etc/systemd/system/faas-vmmd.service.d/ 2>/dev/null || true
    } | head -1 | cut -d= -f2
  )"
fi
if [[ -z "${FAAS_PUBLIC_IFACE:-}" ]]; then
  # Same guard, same reason: a host with no default route must reach the die
  # below with an actionable message, not exit 2 with none.
  FAAS_PUBLIC_IFACE="$(
    { ip route show default 2>/dev/null || true; } | awk '{print $5; exit}'
  )"
fi
[[ -n "${FAAS_PUBLIC_IFACE}" ]] ||
  die "cannot determine the outward NIC; set FAAS_PUBLIC_IFACE or give the host a default route"
ip link show "${FAAS_PUBLIC_IFACE}" >/dev/null 2>&1 ||
  die "FAAS_PUBLIC_IFACE=${FAAS_PUBLIC_IFACE} does not exist on this host; tenant egress NAT would silently do nothing"
export FAAS_PUBLIC_IFACE
echo "native e2e: tenant egress NIC: ${FAAS_PUBLIC_IFACE}"

mkdir -p /var/lock
# Same lock as the builder and metal gates: all three stop services on this
# node, so they must never overlap.
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w "${FAAS_E2E_LOCK_TIMEOUT_SECONDS:-1800}" 9 ||
  die "another native acceptance run holds the host lock"

mkdir -p "${stage_root}" "${cache_root}/go-build" "${cache_root}/go-mod" \
  "${cache_root}/home"
: > "${active_services}"

cleanup() {
  local rc=$?
  local restore_failed=0
  trap - EXIT HUP INT TERM
  set +e

  if ! bash "${repo_root}/deploy/scripts/leakcheck.sh"; then
    echo "native e2e: final leak check failed" >&2
    [[ "${rc}" -ne 0 ]] || rc=1
  fi

  while IFS= read -r service; do
    [[ -n "${service}" ]] || continue
    if ! systemctl start "${service}"; then
      echo "native e2e: failed to restore ${service}" >&2
      restore_failed=1
    fi
  done < "${active_services}" 2>/dev/null || true
  if [[ "${restore_failed}" -ne 0 ]]; then
    [[ "${rc}" -ne 0 ]] || rc=1
  fi

  rm -rf "${stage_root}"
  if [[ -n "${transfer_root}" ]]; then
    systemd-run --quiet --collect --unit="faas-native-e2e-clean-${run_id}" \
      --on-active=5m /usr/bin/find "${transfer_root}" -depth -delete >/dev/null 2>&1
  fi

  if [[ "${rc}" -eq 0 ]]; then
    echo "native e2e: PASS; services restored and staging removed"
  else
    echo "native e2e: FAIL (${rc}); services restored and staging removed" >&2
  fi
  exit "${rc}"
}
trap cleanup EXIT HUP INT TERM

firecracker_running() {
  local exe target
  for exe in /proc/[0-9]*/exe; do
    target="$(readlink "${exe}" 2>/dev/null || true)"
    if [[ "${target##*/}" == firecracker* ]]; then
      return 0
    fi
  done
  return 1
}

if firecracker_running; then
  die "Firecracker workloads are active; drain the designated acceptance node before retrying"
fi
bash "${repo_root}/deploy/scripts/leakcheck.sh"

# ---------------------------------------------------------------------------
# Pre-flight. Every fixture this suite needs is checked HERE, with the command
# that repairs it, instead of letting the affected tests t.Skip() their way to
# a green check. A missing fixture is a broken gate, not a smaller gate.
# ---------------------------------------------------------------------------
[[ -r "${kernel}" ]] || die "kernel is unreadable: ${kernel} (stage it, or set FAAS_TEST_KERNEL)"
# The builder base is only a local FILE on a local-backend node. With an OCI
# backend, builderd resolves it through storage.LocalPathResolver into the
# read-through cache (see resolveBuilderBasePath in cmd/builderd/main.go) and
# nothing is required to exist under /srv/fc/base at all — demanding a file
# there would fail a correctly pre-staged node. Note also that the legacy
# builder-base.ext4 spelling below is deliberately NOT the canonical key:
# builderd rewrites it to runner-builder-<arch>.ext4, so this path is an
# identity hint, never the drive vmmd attaches.
if [[ "${FAAS_STORAGE_BACKEND:-local}" == "local" ]]; then
  [[ -r "${builder_base}" ]] ||
    die "builder base is unreadable: ${builder_base}; start faas-imaged once so EnsureBaseExt4 stages it, or set FAAS_BUILDER_BASE_PATH"
fi
ip link show br-tenants >/dev/null 2>&1 || die "tenant bridge br-tenants is unavailable"
[[ "$(cat /proc/sys/net/ipv4/ip_forward)" == "1" ]] || die "IPv4 forwarding is disabled"

# FAAS_SKIP_PG_TESTS is pgtest's opt-out. On this gate it is a way to make the
# suite vacuous, so refuse to run with it set rather than honour it.
if [[ -n "${FAAS_SKIP_PG_TESTS:-}" ]]; then
  die "FAAS_SKIP_PG_TESTS is set; this gate must not run with Postgres tests disabled"
fi
unset FAAS_SKIP_PG_TESTS

echo "native e2e: probe Postgres"
if ! pg_isready -d "${database_url}" >/dev/null 2>&1; then
  die "Postgres is not reachable at the configured DSN.
  cmd/e2e is database-backed and pgtest SKIPS rather than fails when the
  cluster is unreachable, so this gate refuses to run without one.
  Provision a cluster on this node and record the DSN:
    sudo -u postgres createuser --createdb faas
    sudo -u postgres createdb -O faas faas_e2e
    printf 'FAAS_E2E_DATABASE_URL=%s\\n' 'postgres:///faas_e2e?host=/run/postgresql&user=faas' \\
      | sudo install -m 0600 -o root -g root /dev/stdin ${e2e_env_file}"
fi
# Reachable is not the same as usable: pgtest creates a schema per test and
# installs citext into public. Prove both privileges now, with the same DSN
# the tests will use, so a permission error surfaces here and not as 400
# individually skipped tests.
probe_schema="faas_e2e_probe_${run_id//[^A-Za-z0-9]/_}"
if ! psql -v ON_ERROR_STOP=1 -q -d "${database_url}" \
  -c "create schema \"${probe_schema}\"" \
  -c "create extension if not exists citext with schema public" \
  -c "drop schema \"${probe_schema}\" cascade" >/dev/null 2>&1; then
  # pg_isready only proves the server accepts connections — it says nothing
  # about the database in the DSN existing or the role's rights, so both
  # failures land here.
  die "the DSN connects but cannot be used by pgtest: the database may not exist,
  or the role may lack CREATE on it. Verify with:
    psql -d '${database_url}' -c 'create schema probe' -c 'drop schema probe'"
fi

echo "native e2e: build exact-commit guest init"
export HOME="${cache_root}/home"
export GOCACHE="${cache_root}/go-build"
export GOMODCACHE="${cache_root}/go-mod"
export GOPATH="${cache_root}"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${FAAS_E2E_GO}" build \
  -trimpath -buildvcs=false -tags linux -o "${guest_init}" ./guest/init

if firecracker_running; then
  die "a Firecracker workload started during pre-flight; drain the node before retrying"
fi
for service in "${candidate_services[@]}"; do
  if systemctl is-active --quiet "${service}"; then
    printf '%s\n' "${service}" >> "${active_services}"
  fi
done
while IFS= read -r service; do
  [[ -n "${service}" ]] || continue
  systemctl stop "${service}" || die "could not stop ${service}"
done < "${active_services}"

PATH="$(dirname "${FAAS_E2E_GO}"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
export DATABASE_URL="${database_url}"
export FAAS_TEST_KERNEL="${kernel}"
export FAAS_TEST_FC_VERSION="${fc_version}"
export FAAS_GUEST_INIT="${guest_init}"
export FAAS_BUILDER_BASE_PATH="${builder_base}"
# This gate runs on the HDD correctness node. It exercises wake correctness and
# reports latency, but must never enforce or feed the reference-SSD p95 cohort
# (docs/ops/builder-native-ci.md).
export FAAS_TEST_REFERENCE_SSD=0

# The metal-tagged tests in ./cmd/e2e — the ones that need this hardware.
#
# The filter is DERIVED FROM SOURCE (every func in a //go:build metal file), not
# hand-listed, so it cannot quietly shrink: a `-run` naming one test would have
# to survive the enumeration checks below and run-native-e2e_test.sh, which
# executes the same derivation against the tree. That is the guarantee the old
# "no -run at all" rule was reaching for; running all ~330 tests on a 4-vCPU
# node turned out to defeat the gate instead (see native-e2e-verdict.sh).
metal_tests="$(native_e2e_metal_tests "${repo_root}")"
[[ -n "${metal_tests}" ]] ||
  die "no metal-tagged tests found in cmd/e2e; the build tag or the derivation is wrong"
metal_test_count="$(printf '%s\n' "${metal_tests}" | wc -l | tr -d ' ')"

# Every required test must be in the derived set. A required test that loses its
# metal tag would otherwise silently stop being run AND stop being required.
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
  printf '%s\n' "${metal_tests}" | grep -qx "${required}" ||
    die "required test ${required} is not in the metal-tagged set; it lost its //go:build metal tag or was renamed"
done

# Passed to make through the ENVIRONMENT, never through RUN_ARGS: Make eats the
# trailing `$` and the shell then chokes on the unquoted `(` and `|`, which ran
# zero tests on 2026-09-13 while every guard reported healthy.
# Phases must be an exact partition of the derived set. Checked on every run,
# not just in the contract test: a metal file added without a phase would
# otherwise never execute while the suite still reported 50 tests.
native_e2e_assert_phase_partition "${repo_root}" ||
  die "the metal phases are not a partition of the metal suite"

# FAAS_E2E_PHASE selects one phase; unset runs the whole suite as before, which
# keeps `bash scripts/ci/run-native-e2e.sh` usable by hand.
phase="${FAAS_E2E_PHASE:-}"
if [[ -n "${phase}" ]]; then
  printf '%s\n' "${NATIVE_E2E_PHASES[@]}" | grep -qx "${phase}" ||
    die "unknown phase ${phase}; known: ${NATIVE_E2E_PHASES[*]}"
  RUN_REGEX="$(native_e2e_phase_regex "${phase}" "${repo_root}")"
  run_count="$(native_e2e_phase_tests "${phase}" "${repo_root}" | grep -c . || true)"
  echo "native e2e: phase ${phase} — ${run_count} of ${metal_test_count} metal tests"
  native_e2e_phase_tests "${phase}" "${repo_root}" | sed 's/^/  - /'
  e2e_log="${FAAS_E2E_TRANSFER_ROOT:-/var/tmp}/cmd-e2e-${phase}.log"
  # Per-phase budget. The whole-suite 75m was one opaque ceiling; a phase that
  # wedges now fails its own step instead of consuming the run's remaining
  # time. build is the outlier — real builder microVMs, 10 min each.
  case "${phase}" in
    build) phase_timeout=40m ;;
    twonode | deploy | streaming) phase_timeout=25m ;;
    *) phase_timeout=15m ;;
  esac
else
  # Passed to make through the ENVIRONMENT, never through RUN_ARGS: Make eats the
  # trailing `$` and the shell then chokes on the unquoted `(` and `|`, which ran
  # zero tests on 2026-09-13 while every guard reported healthy.
  RUN_REGEX="^($(printf '%s\n' "${metal_tests}" | paste -sd'|' -))$"
  echo "native e2e: run ${metal_test_count} metal-tagged tests from ./cmd/e2e"
  e2e_log="${FAAS_E2E_TRANSFER_ROOT:-/var/tmp}/cmd-e2e.log"
  phase_timeout=75m
fi
export RUN_REGEX

# Compile the daemons once into a stage-owned directory and let every phase
# reuse them. The link is per process and is NOT covered by the Go build cache,
# so without this each phase would re-link all eight binaries.
export FAAS_E2E_BIN_DIR="${stage_root}/bin"
mkdir -p "${FAAS_E2E_BIN_DIR}"

# Distinct from the transient unit's own native-e2e.log: the unit already
# appends this script's stdout there, and tee-ing into the same file would
# interleave every line with itself and corrupt the tally greps below.
set +e
make GO="${FAAS_E2E_GO}" PKGS=./cmd/e2e/... \
  RUN_ARGS="-timeout=${phase_timeout} -v" test-metal 2>&1 | tee "${e2e_log}"
e2e_rc="${PIPESTATUS[0]}"
set -e

# Tally + required-test contract. The rules live in native-e2e-verdict.sh so
# run-native-e2e_test.sh can drive them with synthetic go-test output instead
# of grepping this file for its own strings.
#
# The required-test contract is a WHOLE-SUITE claim: no single phase contains
# all eight required tests, so applying it per phase would fail every phase for
# tests it was never meant to run. In phase mode report the phase's own tally
# and let the workflow's final verdict step own the contract.
if [[ -n "${phase}" ]]; then
  native_e2e_phase_tally "${e2e_log}" "${phase}" || e2e_rc=1
else
  native_e2e_verdict "${e2e_log}" || e2e_rc=1
fi

exit "${e2e_rc}"
