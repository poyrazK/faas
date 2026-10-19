#!/usr/bin/env bash
# Hermetic tests for unit-release-verify.sh: systemctl, curl and /proc are
# stubbed, so no systemd or daemon is needed.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
verify="$repo_root/scripts/unit-release-verify.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

release="$test_root/releases/new"
old="$test_root/releases/old"
mkdir -p "$release/bin" "$old/bin" "$test_root/stub" "$test_root/proc"

# Stub state, per service: <svc>.active (exists = active), <svc>.pid, and
# the proc symlink naming its binary. curl succeeds unless <port>.down.
cat > "$test_root/stub/systemctl" <<'EOF'
#!/usr/bin/env bash
state="$STUB_STATE"
case "$1" in
  is-active) [[ -e "$state/${3%.service}.active" ]] ;;
  show) cat "$state/${2%.service}.pid" 2>/dev/null || echo 0 ;;
  restart)
    svc="${2%.service}"
    echo "$svc" >> "$state/restarts"
    ln -sfn "$STUB_RELEASE/bin/${svc#faas-}" "$STUB_PROC/$(cat "$state/$svc.pid")/exe" ;;
  status) ;;
  *) exit 1 ;;
esac
EOF
cat > "$test_root/stub/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
port="${url#http://127.0.0.1:}"
port="${port%%/*}"
[[ ! -e "$STUB_STATE/$port.down" ]]
EOF
printf '#!/usr/bin/env bash\n' > "$test_root/stub/journalctl"
chmod +x "$test_root/stub/"*

pid=100
service() { # service NAME active(true|false) BINARY_ROOT
  local name="$1" active="$2" root="$3"
  pid=$((pid + 1))
  echo "$pid" > "$test_root/state/faas-$name.pid"
  mkdir -p "$test_root/proc/$pid"
  ln -sfn "$root/bin/$name" "$test_root/proc/$pid/exe"
  [[ "$active" != true ]] || touch "$test_root/state/faas-$name.active"
}

reset() {
  rm -rf "$test_root/state" "$test_root/proc"
  mkdir -p "$test_root/state" "$test_root/proc"
}

run() {
  PATH="$test_root/stub:$PATH" STUB_STATE="$test_root/state" STUB_PROC="$test_root/proc" \
    STUB_RELEASE="$release" UNIT_VERIFY_PROC="$test_root/proc" UNIT_VERIFY_TIMEOUT=1 \
    bash "$verify" "$release" "$@"
}

fail() { echo "FAIL: $*" >&2; exit 1; }

# 1. Healthy restarted service on the new release passes.
reset
service apid true "$release"
run "apid=http://127.0.0.1:9101/readyz" >/dev/null || fail "healthy apid rejected"

# 2. A service still running the old binary fails.
reset
service apid true "$old"
if run "apid" >/dev/null 2>&1; then fail "apid on the old release accepted"; fi

# 3. A failing readiness URL fails.
reset
service meterd true "$release"
touch "$test_root/state/9106.down"
if run "meterd=http://127.0.0.1:9106/readyz" >/dev/null 2>&1; then fail "unready meterd accepted"; fi

# 4. An inactive core service fails.
reset
service githubd false "$release"
if run "githubd" >/dev/null 2>&1; then fail "inactive githubd accepted"; fi

# 5. An enabled optional unit on the old binary is restarted onto the new one.
reset
service apid true "$release"
service apid-clone-worker true "$old"
run "apid" "?apid-clone-worker" >/dev/null || fail "optional clone worker not converged"
grep -qx "faas-apid-clone-worker" "$test_root/state/restarts" || fail "clone worker was not restarted"

# 6. An optional unit already on the new release is not restarted again.
reset
service s3-gatewayd true "$release"
run "?s3-gatewayd=http://127.0.0.1:9096/readyz" >/dev/null || fail "converged s3-gatewayd rejected"
[[ ! -e "$test_root/state/restarts" ]] || fail "converged optional unit restarted"

# 7. An optional unit the operator never enabled is skipped.
reset
service bridged false "$old"
run "?bridged" >/dev/null || fail "inactive optional bridged failed the gate"
[[ ! -e "$test_root/state/restarts" ]] || fail "inactive optional unit started"

# 8. Bad input is a usage error.
if run "Bad Name" >/dev/null 2>&1; then fail "invalid service name accepted"; fi

echo "unit-release-verify tests passed"
