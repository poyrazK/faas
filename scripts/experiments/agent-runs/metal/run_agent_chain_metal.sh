#!/usr/bin/env bash
# Run the agent PR-triage chain (pkg/e2etest/agentchain TestMetalAgentPRTriage,
# ADR-20261027090010 Milestone A) on a shared native KVM host. Run as root.
#
#   run_agent_chain_metal.sh DIR
#
# DIR holds bin/ (every daemon e2etest builds, plus agentd, outboundd,
# githubd, vmmd-jail-helper and guest-init), agentchain.test, go.mod,
# memory/ (scripts/experiments/agent-runs/memory, for the agent base) and
# leakcheck/ (deploy/scripts/leakcheck.sh and its helper).
#
# DIR also holds native_journal_finished.py, for setting aside a journal
# another run left behind.
#
# AGENT_BASE=imaged has the harness's imaged stage the agent base, as an
# agent-pool node does: build_agent_base_layer.sh builds the root filesystem
# from images/agent-base.Dockerfile into one layer, and DIR also holds
# context/ (images/agent-base.Dockerfile, images/agent-base/ and
# pkg/agentpresets/presets.json) and build_agent_base_layer.sh. The default
# boots a base this script builds, admitted with a fixture scan sidecar.
#
# An Anthropic key in KEYS_FILE (default /etc/faas/agent-chain-keys.env,
# root-owned 0600, as FAAS_E2E_ANTHROPIC_API_KEY) runs Claude Code; it is
# never printed, and the test hands it to the platform only through the
# credential API. Without the file the run is keyless: a stand-in harness
# sends one request through the relay with a placeholder credential, and
# Anthropic's 401 proves the call got there.
#
# The run never touches the host's own services: it starts a private
# Postgres on its own socket and port, and the daemons run on the harness's
# per-test sockets. It holds the host acceptance lock, refuses to start while
# another session's Firecracker VMs run, runs leakcheck before and after,
# and removes its work directory. Logs stay in DIR/logs.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

dir=$(cd "$1" && pwd)
bin=$dir/bin work=$dir/work logs=$dir/logs
keys=${KEYS_FILE:-/etc/faas/agent-chain-keys.env}
kernel=${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.134}
pg_port=${PG_PORT:-55431}
pg_bin=/usr/lib/postgresql/16/bin
mkdir -p "$logs"

if [[ -f $keys ]]; then
  [[ $(stat -c '%a %U' "$keys") == "600 root" ]] || { echo "run: $keys must be root-owned mode 0600" >&2; exit 2; }
  echo "run: with the key in $keys"
else
  echo "run: no $keys; keyless"
  keys=/dev/null
fi
[[ -f $kernel ]] || { echo "run: kernel $kernel is missing" >&2; exit 2; }
if ss -ltnH "sport = :$pg_port" | grep -q .; then
  echo "run: port $pg_port is in use; set PG_PORT" >&2
  exit 2
fi

exec 9>/var/lock/faas-builder-acceptance.lock
echo "run: waiting for the acceptance lock"
flock -w "${LOCK_WAIT_S:-3600}" 9 || { echo "run: acceptance lock held" >&2; exit 75; }
for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null || true)
  if [[ ${target##*/} == firecracker* ]]; then
    echo "run: another session's Firecracker VMs are running; stopping" >&2
    exit 75
  fi
done
# A native e2e run on this host can leave its native-process journal, and
# vmmd then refuses to start without native_process_recovery. Under the
# lock, set it aside (renamed, never deleted), and only when no daemon holds
# the owner lock and every record is finished by its own kind's flag
# (native_journal_finished.py: resources_removed, removed, or a revoked
# qualification past its deadline).
journal=/srv/fc/jail/.native-processes
if [[ -e $journal ]]; then
  if ! summary=$(python3 -I "$dir/native_journal_finished.py" "$journal") || ! flock -n "$journal/.daemon-owner.lock" true; then
    echo "run: $journal has live records or an owner; stopping" >&2
    printf '%s\n' "$summary" >&2
    exit 75
  fi
  stale=$journal.stale-$(date -u +%Y%m%dT%H%M%SZ)
  mv "$journal" "$stale"
  echo "run: set aside $journal as $stale (all finished: ${summary%%$'\n'*})"
fi

# shellcheck disable=SC2329 # run by the EXIT trap
cleanup() {
  set +e
  if [[ -f $work/pg/postmaster.pid ]]; then
    runuser -u postgres -- "$pg_bin/pg_ctl" -D "$work/pg" -m fast stop >/dev/null
  fi
  for m in "$work/base/rootfs/dev" "$work/base/rootfs/proc"; do
    if mountpoint -q "$m" 2>/dev/null; then umount "$m"; fi
  done
  if ! findmnt -rn -o TARGET | grep -q "^$work"; then
    rm -rf --one-file-system "$work"
  fi
}
trap cleanup EXIT
mkdir -p "$work/tmp" "$work/pgsock"
bash "$dir/leakcheck/leakcheck.sh" | tee "$logs/leakcheck-before.log"

echo "== private postgres on port $pg_port"
chown postgres:postgres "$work/pgsock"
install -d -o postgres -g postgres "$work/pg"
runuser -u postgres -- "$pg_bin/initdb" -D "$work/pg" -A trust -U postgres --no-sync >"$logs/initdb.log"
install -o postgres -g postgres -m 0640 /dev/null "$logs/postgres.log"
runuser -u postgres -- "$pg_bin/pg_ctl" -D "$work/pg" -l "$logs/postgres.log" -w \
  -o "-p $pg_port -c listen_addresses='' -c unix_socket_directories=$work/pgsock -c max_connections=300 -c fsync=off -c max_locks_per_transaction=1024" start
chmod 0777 "$work/pgsock"
database_url="postgres://postgres@/postgres?host=$work/pgsock&port=$pg_port&sslmode=disable"

echo "== agent base (${AGENT_BASE:-built})"
if [[ ${AGENT_BASE:-built} == imaged ]]; then
  bash "$dir/build_agent_base_layer.sh" "$work/base" "$dir/context" "$dir/memory" >"$logs/base.log" 2>&1
  tail -1 "$logs/base.log"
  base_env=(FAAS_E2E_AGENT_BASE_LAYER="$work/base/agent-base-layer.tar.gz")
else
  bash "$dir/memory/build_agent_images.sh" "$work/base" "$bin/guest-init" "$dir/memory" pull install >"$logs/base.log" 2>&1
  rm -f "$work/base/rootfs/etc/resolv.conf"
  base_mb=$(( $(du -sm --exclude=proc --exclude=dev "$work/base/rootfs" | cut -f1) * 5 / 4 + 256 ))
  truncate -s "${base_mb}M" "$work/agent-base.ext4"
  mkfs.ext4 -q -O '^has_journal' -d "$work/base/rootfs" -L agent-base -F "$work/agent-base.ext4"
  rm -rf --one-file-system "$work/base/rootfs"
  chmod 0644 "$work/agent-base.ext4"
  base_env=(FAAS_E2E_AGENT_RUN_BASE="$work/agent-base.ext4")
fi

echo "== chain test"
rc=0
(
  set -a
  # shellcheck disable=SC1090
  source "$keys"
  set +a
  cd "$dir"
  env TMPDIR="$work/tmp" GOTOOLCHAIN=local DATABASE_URL="$database_url" FAAS_PGTEST_TEMPLATE_DATABASE=1 \
    FAAS_E2E_BIN_DIR="$bin" FAAS_GUEST_INIT="$bin/guest-init" FAAS_TEST_KERNEL="$kernel" FAAS_TEST_FC_VERSION="${FAAS_TEST_FC_VERSION:-1.7.0}" \
    "${base_env[@]}" \
    ./agentchain.test -test.run '^TestMetalAgentPRTriage$' -test.count=1 -test.v -test.timeout 90m
) >"$logs/chain.log" 2>&1 || rc=$?
echo "chain test exit $rc"
bash "$dir/leakcheck/leakcheck.sh" | tee "$logs/leakcheck-after.log" || rc=1
exit "$rc"
