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
[[ -r "${builder_base}" ]] ||
  die "builder base is unreadable: ${builder_base}; start faas-imaged once so EnsureBaseExt4 stages it, or set FAAS_BUILDER_BASE_PATH"
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

# The whole ./cmd/e2e package, both build tags' worth of tests, in one binary.
#
# -run is deliberately absent. The self-hosted `metal` job this family replaces
# executed exactly one test for 100 consecutive dispatches; a -run added "just
# to triage a flake" is how that happens again. The required-test contract
# below is the narrower lever: it names the tests that must actually execute,
# so the suite can grow without the gate silently shrinking.
echo "native e2e: run the ./cmd/e2e suite with the metal build tag"
# Distinct from the transient unit's own native-e2e.log: the unit already
# appends this script's stdout there, and tee-ing into the same file would
# interleave every line with itself and corrupt the tally greps below.
e2e_log="${FAAS_E2E_TRANSFER_ROOT:-/var/tmp}/cmd-e2e.log"
set +e
make GO="${FAAS_E2E_GO}" PKGS=./cmd/e2e/... \
  RUN_ARGS='-timeout=75m -v' test-metal 2>&1 | tee "${e2e_log}"
e2e_rc="${PIPESTATUS[0]}"
set -e

# Tally + required-test contract. The rules live in native-e2e-verdict.sh so
# run-native-e2e_test.sh can drive them with synthetic go-test output instead
# of grepping this file for its own strings.
native_e2e_verdict "${e2e_log}" || e2e_rc=1

exit "${e2e_rc}"
