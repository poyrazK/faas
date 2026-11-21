#!/usr/bin/env bash
# Hermetic tests for observe_remote_rollout.sh: ssh and scp are stubbed to act
# on this machine, and the observer itself is not run; the test reads the
# command line it would have run on the control plane.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
stub="$test_root/stub"
mkdir -p "$stub"
cat > "$stub/ssh" <<'STUB'
#!/usr/bin/env bash
while [[ "$1" == -* ]]; do case "$1" in -i|-o|-p) shift 2 ;; *) shift ;; esac; done
shift # target
case "$*" in
  *"/opt/faas/current"*) printf '%s\n' "$STUB_RELEASE_STATE" ;;
  mktemp*) mktemp -d "$STUB_ROOT/gregale-rollout.XXXXXXXX" | sed "s#^$STUB_ROOT#/tmp#" ;;
  "rm -rf"*) ;;
  *) printf '%s\n' "$*" > "$STUB_ROOT/remote-command"; touch "$STUB_ROOT/ran" ;;
esac
STUB
cat > "$stub/scp" <<'STUB'
#!/usr/bin/env bash
args=()
while [[ $# -gt 0 ]]; do case "$1" in -i|-o|-P) shift 2 ;; -q|-r) shift ;; *) args+=("$1"); shift ;; esac; done
last=$(( ${#args[@]} - 1 )); dest="${args[$last]}"; unset "args[$last]"
map() { local p="${1#*:}"; [[ "$1" == *:* ]] && p="$STUB_ROOT${p#/tmp}"; printf '%s' "$p"; }
srcs=(); for a in "${args[@]}"; do srcs+=("$(map "$a")"); done
cp -R "${srcs[@]}" "$(map "$dest")"
STUB
chmod +x "$stub/ssh" "$stub/scp"
echo 'true' > "$test_root/activate.sh"

run() { # run STATE: the observer's exit status
  rm -f "$test_root/remote-command" "$test_root/ran"
  (cd "$repo_root" && env PATH="$stub:$PATH" STUB_ROOT="$test_root" STUB_RELEASE_STATE="$1" RUNNER_TEMP="$test_root" \
    bash scripts/ci/observe_remote_rollout.sh root@cp.example /dev/null "$test_root/activate.sh" >/dev/null 2>&1) \
    && echo 0 || echo $?
}

[[ "$(run active)" == 0 ]] || fail "an active control plane's rollout did not run"
grep -q 'ROLLOUT_REQUIRE_API_BASELINE=true' "$test_root/remote-command" || fail "an active control plane lost its API baseline gate"
if grep -q 'ROLLOUT_FIRST_ACTIVATION' "$test_root/remote-command"; then fail "an active control plane was treated as a first activation"; fi

[[ "$(run none)" == 0 ]] || fail "a rebuilt control plane's first activation did not run"
grep -q 'ROLLOUT_FIRST_ACTIVATION=true' "$test_root/remote-command" || fail "a rebuilt control plane was not a first activation"
if grep -q 'ROLLOUT_REQUIRE_API_BASELINE' "$test_root/remote-command"; then fail "a first activation still required an API baseline"; fi

[[ "$(run '')" == 2 ]] || fail "an unreadable release state did not stop the rollout"
[[ ! -e "$test_root/ran" ]] || fail "the rollout ran although the release state was unreadable"

echo "observe_remote_rollout tests passed"
