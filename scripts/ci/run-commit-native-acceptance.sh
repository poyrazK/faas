#!/usr/bin/env bash
# Exclusive, disposable-host Commit KVM qualification. Raw output stays private.
set -Eeuo pipefail
umask 077

expected="${1:?expected commit required}"
evidence="${2:?fresh evidence directory required}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[[ "$expected" =~ ^[a-f0-9]{40}$ ]] || { echo 'Commit native: invalid commit' >&2; exit 1; }
python3 - "$evidence" "$repo" <<'PY'
from pathlib import Path
import sys
directory, repo = map(Path, sys.argv[1:])
if not directory.is_absolute() or directory.exists() or directory.is_symlink() or directory.resolve().is_relative_to(repo.resolve()):
    raise SystemExit('Commit native: evidence must be a fresh absolute directory outside the checkout')
directory.mkdir(mode=0o700)
(directory / 'private').mkdir(mode=0o700)
PY
private="$evidence/private"
exec 3>&1
exec >"$private/runner.log" 2>&1
reason=setup_failed
native_started=false
host_class=unknown
leak_before=not_run
leak_after=not_run
postgres_cleanup=not_run
process_cleanup=not_run
binaries_cleanup=not_run
work_cleanup=not_run
gate_exit=""
pg_dir=""
bin_dir=""
pg_attempted=false
test_group=""
fingerprint_ready=false

finish() {
  local status=$?
  trap - EXIT INT TERM HUP
  set +e
  if [[ -n "$test_group" ]]; then
    process_cleanup=fail
    kill -TERM -- "-$test_group" 2>/dev/null
    sleep 2
    kill -KILL -- "-$test_group" 2>/dev/null
    wait "$test_group" 2>/dev/null
  fi
  if [[ "$pg_attempted" == true ]]; then
    postgres_cleanup=fail
    if runuser -u "$FAAS_COMMIT_TLS_PG_USER" -- "$FAAS_COMMIT_TLS_PG_BIN_DIR/pg_ctl" -D "$pg_dir/data" -m immediate -w stop >/dev/null 2>&1; then
      postgres_cleanup=pass
    elif runuser -u "$FAAS_COMMIT_TLS_PG_USER" -- "$FAAS_COMMIT_TLS_PG_BIN_DIR/pg_ctl" -D "$pg_dir/data" status >/dev/null 2>&1; then
      postgres_cleanup=fail
    else
      # pg_ctl status returns 3 only when there is no running server.
      pg_status=$?
      [[ "$pg_status" == 3 ]] && postgres_cleanup=pass
    fi
    if [[ "$postgres_cleanup" == pass ]]; then
      rm -rf -- "$pg_dir" || { postgres_cleanup=fail; status=1; }
    else
      status=1
    fi
  elif [[ -n "$pg_dir" ]]; then
    rm -rf -- "$pg_dir"
  fi
  if [[ "$native_started" == true ]]; then
    if ! python3 "$repo/scripts/ci/commit_native_evidence.py" process-check --binaries "$bin_dir" >"$private/process-check.log" 2>&1; then
      process_cleanup=fail
      status=1
    fi
    if make -C "$repo" leakcheck >"$private/leak-after.log" 2>&1; then leak_after=pass; else leak_after=fail; status=1; fi
    if ! cmp -s -- "$FAAS_TEST_KERNEL" "$FAAS_STORAGE_ROOT/kernel/$FAAS_TEST_FC_VERSION"; then
      echo 'Commit native: staged kernel changed or disappeared' >&2
      status=1
    fi
  fi
  if [[ "$fingerprint_ready" == true ]]; then
    python3 "$repo/scripts/ci/commit_native_evidence.py" fingerprint \
      --repo "$repo" --expected "$expected" --binaries "$FAAS_E2E_BIN_DIR" \
      --kernel "$FAAS_TEST_KERNEL" --builder-base "$FAAS_BUILDER_BASE_PATH" --guest-init "$FAAS_GUEST_INIT" \
      --firecracker "$firecracker" --jailer "$jailer" --go-tool "$go_binary" --output "$private/identities-after.json" || status=1
  fi
  if [[ -n "$bin_dir" && ( ( "$native_started" == false && "$reason" != interrupted ) || ( "$native_started" == true && "$leak_after" == pass && "$process_cleanup" == pass ) ) ]]; then
    if rm -rf -- "$bin_dir"; then binaries_cleanup=pass; else binaries_cleanup=fail; status=1; fi
  fi
  if [[ "$native_started" == true && "$gate_exit" == 0 && "$leak_after" == pass && "$process_cleanup" == pass && "$postgres_cleanup" == pass && "$binaries_cleanup" == pass && "$status" == 0 ]]; then
    if rm -rf -- "$private/work"; then work_cleanup=pass; else work_cleanup=fail; status=1; fi
  fi
  if [[ "$status" != 0 && "$reason" == completed ]]; then reason=cleanup_failed; fi
  if ! python3 - "$private/state.json" "$reason" "$native_started" "$host_class" "$leak_before" "$leak_after" "$postgres_cleanup" "$process_cleanup" "$binaries_cleanup" "$work_cleanup" "$gate_exit" <<'PY'
import json, sys
from pathlib import Path
path, reason, started, host, before, after, postgres, processes, binaries, work, gate = sys.argv[1:]
Path(path).write_text(json.dumps(dict(reason=reason, native_started=started == 'true', host_class=host,
    leak_before=before, leak_after=after, postgres_cleanup=postgres, process_cleanup=processes,
    binaries_cleanup=binaries, work_cleanup=work,
    gate_exit=int(gate) if gate else None)) + '\n')
PY
  then status=1; fi
  echo "Commit native: $reason (exit $status); raw evidence remains private" >&3
  exit "$status"
}
trap finish EXIT
trap 'reason=interrupted; exit 130' INT
trap 'reason=interrupted; exit 143' TERM HUP
die() { reason="$1"; echo "Commit native: $1" >&2; exit 1; }

[[ "$(id -u)" == 0 && "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -c /dev/kvm ]] || die unsupported_host
for tool in git go make python3 flock timeout setsid unshare mount runuser ip nft iptables tc setpriv debugfs e2fsck mkfs.ext4 gcc systemctl systemd-detect-virt pgrep firecracker jailer; do
  command -v "$tool" >/dev/null || die missing_tool
done
python3 - <<'PY'
from pathlib import Path
import stat
marker = Path('/etc/faas/builder-acceptance-host')
s = marker.lstat()
if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022:
    raise SystemExit('invalid dedicated-host marker')
PY
config=/etc/faas/commit-native-acceptance.env
[[ -f "$config" && ! -L "$config" && "$(stat -c '%u:%a' "$config")" == 0:600 ]] || die invalid_host_configuration
[[ -z "${FAAS_SKIP_PG_TESTS:-}" ]] || die postgres_skipped
# Treat the root-owned file as data, not shell code. No inherited FAAS or
# GREGALE setting may silently redirect this run to an external service.
for environment_name in ${!FAAS_@} ${!GREGALE_@}; do unset "$environment_name"; done
unset DATABASE_URL GOFLAGS GOTOOLCHAIN GOCACHE GOMODCACHE GOPATH
config_values="$(python3 "$repo/scripts/ci/commit_native_evidence.py" config --file "$config")" || die invalid_host_configuration
while IFS='=' read -r key value; do
  printf -v "$key" '%s' "$value"
  export "${key?}"
done <<< "$config_values"
: "${FAAS_TEST_KERNEL:?}" "${FAAS_TEST_FC_VERSION:?}" "${FAAS_BUILDER_BASE_PATH:?}" "${FAAS_COMMIT_TLS_PG_BIN_DIR:?}" "${FAAS_COMMIT_TLS_PG_USER:?}"
[[ "$FAAS_TEST_FC_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die invalid_firecracker_version
[[ "$FAAS_TEST_KERNEL" == /* && "$FAAS_BUILDER_BASE_PATH" == /* && "$FAAS_COMMIT_TLS_PG_BIN_DIR" == /* ]] || die missing_fixture
[[ "$FAAS_COMMIT_TLS_PG_USER" =~ ^[a-z_][a-z0-9_-]*\$?$ ]] || die invalid_postgres_user
[[ -f "$FAAS_TEST_KERNEL" && -r "$FAAS_TEST_KERNEL" && -f "$FAAS_BUILDER_BASE_PATH" && -r "$FAAS_BUILDER_BASE_PATH" ]] || die missing_fixture
[[ -x "$FAAS_COMMIT_TLS_PG_BIN_DIR/initdb" && -x "$FAAS_COMMIT_TLS_PG_BIN_DIR/pg_ctl" ]] || die missing_postgres_binaries
[[ "$(id -u "$FAAS_COMMIT_TLS_PG_USER")" != 0 ]] || die invalid_postgres_user
[[ -d /srv/fc/snap && ! -L /srv/fc/snap ]] || die missing_snapshot_mountpoint
ip link show br-tenants >/dev/null || die missing_tenant_bridge
[[ "$(cat /proc/sys/net/ipv4/ip_forward)" == 1 ]] || die forwarding_disabled
FAAS_PUBLIC_IFACE="${FAAS_PUBLIC_IFACE:-$(ip route show default | awk 'NR == 1 {print $5}')}"
if [[ -z "$FAAS_PUBLIC_IFACE" ]] || ! ip link show "$FAAS_PUBLIC_IFACE" >/dev/null; then
  die missing_public_interface
fi
export FAAS_PUBLIC_IFACE
systemctl list-units --all --no-legend >/dev/null || die systemd_unavailable
container_rc=0
container="$(systemd-detect-virt --container)" || container_rc=$?
[[ "$container_rc" == 1 && "$container" == none ]] || die unsupported_container
vm_rc=0
vm="$(systemd-detect-virt --vm)" || vm_rc=$?
if [[ "$vm_rc" == 1 && "$vm" == none ]]; then host_class=bare-metal
elif [[ "$vm_rc" == 0 && -n "$vm" && "$vm" != none ]]; then host_class=nested-kvm
else die unknown_host_class
fi

exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 10 9 || die busy_host
active_services="$(systemctl list-units --all --type=service --state=active --no-legend --plain 'faas-*.service')" || die systemd_unavailable
[[ -z "${active_services//[[:space:]]/}" ]] || die active_service
process_status=0
pgrep -x 'firecracker(-v[0-9].*)?' >/dev/null || process_status=$?
[[ "$process_status" == 1 ]] || die active_firecracker
for volume in "$repo" "$private" /tmp; do
  available="$(df -Pk "$volume" | awk 'NR == 2 {print $4}')"
  [[ "$available" =~ ^[0-9]+$ && "$available" -ge 33554432 ]] || die insufficient_disk
done

export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0="$repo"
cd "$repo"
[[ "$(git rev-parse HEAD)" == "$expected" && -z "$(git status --porcelain --untracked-files=all)" ]] || die source_mismatch
[[ -z "$(git ls-files --others --ignored --exclude-standard)" ]] || die ignored_source_files
export GOCACHE="$private/work/go-build" GOMODCACHE="$private/work/go-mod" GOPATH="$private/work/gopath"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOPATH"
go_binary="$(command -v go)"
go_expected="$(awk '$1 == "toolchain" {print $2; exit}' go.mod)"
[[ -n "$go_expected" && "$(GOTOOLCHAIN=local "$go_binary" env GOVERSION)" == "$go_expected" ]] || die go_version_mismatch
firecracker="$(command -v firecracker)"
jailer="$(command -v jailer)"
fc_version="$("$firecracker" --version)"
[[ "${fc_version%%$'\n'*}" == "Firecracker v$FAAS_TEST_FC_VERSION" ]] || die firecracker_version_mismatch

export GO="$go_binary" GOTOOLCHAIN=local GOMAXPROCS=2 GOMEMLIMIT=6GiB GOGC=30
export GOFLAGS='-buildvcs=false'
export FAAS_COMMIT_TLS_PG_BIN_DIR FAAS_COMMIT_TLS_PG_USER FAAS_TEST_KERNEL FAAS_TEST_FC_VERSION FAAS_BUILDER_BASE_PATH
export FAAS_COMMIT_TLS_PG_TMPDIR=/tmp FAAS_PGTEST_TEMPLATE_DATABASE=1 FAAS_TEST_REFERENCE_SSD=0
bin_dir="$(mktemp -d /tmp/gregale-commit-bin.XXXXXXXX)"
echo "Private binary staging: $bin_dir"
chmod 755 "$bin_dir"
export FAAS_E2E_BIN_DIR="$bin_dir" FAAS_GUEST_INIT="$bin_dir/guest-init"
export FAAS_STORAGE_BACKEND=local FAAS_STORAGE_ROOT="$private/work/storage"
export FAAS_COMMIT_NATIVE_RESULTS="$private/test-events.jsonl"
mkdir -p "$FAAS_STORAGE_ROOT/snap" "$FAAS_STORAGE_ROOT/kernel" "$GOCACHE" "$GOMODCACHE" "$GOPATH"
# vmmd resolves the boot kernel by Firecracker version through storage, even
# when the harness also receives an absolute FAAS_TEST_KERNEL path.
install -m 0644 -- "$FAAS_TEST_KERNEL" "$FAAS_STORAGE_ROOT/kernel/$FAAS_TEST_FC_VERSION"
cmp -s -- "$FAAS_TEST_KERNEL" "$FAAS_STORAGE_ROOT/kernel/$FAAS_TEST_FC_VERSION" || die kernel_staging_failed

if make leakcheck >"$private/leak-before.log" 2>&1; then leak_before=pass; else leak_before=fail; die initial_leakcheck_failed; fi
for daemon in apid schedd vmmd imaged gatewayd-internal gatewayd-public meterd builderd vmmd-stream-bridge vmmd-raw-bridge vmmd-tcp-bridge vmmd-udp-bridge; do
  "$go_binary" build -tags metal -o "$FAAS_E2E_BIN_DIR/$daemon" "./cmd/$daemon"
done
for helper in vmmd-jail-helper vmmd-guestdisk; do
  CGO_ENABLED=0 "$go_binary" build -o "$FAAS_E2E_BIN_DIR/$helper" "./cmd/$helper"
done
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_binary" build -trimpath -o "$FAAS_GUEST_INIT" ./guest/init
python3 "$repo/scripts/ci/commit_native_evidence.py" fingerprint \
  --repo "$repo" --expected "$expected" --binaries "$FAAS_E2E_BIN_DIR" \
  --kernel "$FAAS_TEST_KERNEL" --builder-base "$FAAS_BUILDER_BASE_PATH" --guest-init "$FAAS_GUEST_INIT" \
  --firecracker "$firecracker" --jailer "$jailer" --go-tool "$go_binary" --output "$private/identities-before.json"
fingerprint_ready=true

pg_dir="$(mktemp -d /tmp/gregale-commit-pg.XXXXXXXX)"
echo "Private PostgreSQL staging: $pg_dir"
chown -- "$FAAS_COMMIT_TLS_PG_USER" "$pg_dir"
runuser -u "$FAAS_COMMIT_TLS_PG_USER" -- "$FAAS_COMMIT_TLS_PG_BIN_DIR/initdb" -D "$pg_dir/data" --no-locale --encoding=UTF8 -A trust -U commit_fixture
pg_attempted=true
runuser -u "$FAAS_COMMIT_TLS_PG_USER" -- "$FAAS_COMMIT_TLS_PG_BIN_DIR/pg_ctl" -D "$pg_dir/data" \
  -o "-c listen_addresses='' -c unix_socket_directories='$pg_dir'" -l "$pg_dir/server.log" -w start
export DATABASE_URL="postgres://commit_fixture@localhost/postgres?host=$pg_dir&sslmode=disable"

# Bind the legacy locator only within a private mount namespace. Never change
# the host's real snapshot mount, and never reap unrelated native resources.
native_started=true
# The mount source is passed as a positional argument to the inner shell.
# shellcheck disable=SC2016
setsid timeout --signal=TERM --kill-after=30s 75m unshare --mount --propagation private \
  bash -c 'set -e; mount --bind "$1" /srv/fc/snap; exec make test-commit-native' _ "$FAAS_STORAGE_ROOT/snap" &
test_group=$!
gate_exit=0
wait "$test_group" || gate_exit=$?
if kill -0 -- "-$test_group" 2>/dev/null; then
  process_cleanup=fail
  kill -TERM -- "-$test_group" 2>/dev/null || true
  sleep 2
  kill -KILL -- "-$test_group" 2>/dev/null || true
else
  process_cleanup=pass
fi
test_group=""
[[ "$gate_exit" == 0 ]] || die native_gate_failed
[[ "$process_cleanup" == pass ]] || die test_processes_remain
reason=completed
