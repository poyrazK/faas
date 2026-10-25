#!/usr/bin/env bash
# Hermetic tests for check_edge_store.sh: go is stubbed to print a linker
# dependency dump.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
check="$repo_root/scripts/ci/check_edge_store.sh"
stub="$(mktemp -d)"
trap 'rm -rf "$stub"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

cat > "$stub/go" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$STUB_LOG"
cat "$STUB_DUMP" >&2
exit "${STUB_EXIT:-0}"
STUB
chmod +x "$stub/go"
export GO="$stub/go" STUB_LOG="$stub/log" STUB_DUMP="$stub/dump"
m=github.com/onebox-faas/faas

cat > "$STUB_DUMP" <<DUMP
main.main -> $m/pkg/state.NewEdgeStore
type:*$m/pkg/state.EdgeStore <UsedInIface> -> $m/pkg/state.(*pgCore).AppBySlug
DUMP
out="$("$check" gatewayd-public)" || fail "a daemon on the edge store failed"
[[ "$out" == *"does not reach"* ]] || fail "no pass message: $out"
grep -q -- "-tags metal,grpcnotrace -ldflags=-dumpdep -o /dev/null ./cmd/gatewayd-public" "$STUB_LOG" \
  || fail "not linked with the release configuration: $(cat "$STUB_LOG")"
grep -q -- "build -trimpath -buildvcs=false" "$STUB_LOG" || fail "GO_BUILD_FLAGS not passed: $(cat "$STUB_LOG")"

cat > "$STUB_DUMP" <<DUMP
main.run -> type:*$m/pkg/state.PgStore
type:$m/pkg/runtimeconfig.Watcher -> type:$m/pkg/state.Store
type:*$m/pkg/state.PgStore <UsedInIface> -> type:$m/pkg/state.PgStore <UsedInIface>
type:$m/pkg/state.Store -> type:$m/pkg/state.Store
DUMP
if err="$("$check" gatewayd-public 2>&1)"; then fail "a daemon reaching *PgStore passed"; fi
[[ "$err" == *"main.run"* && "$err" == *"pkg/runtimeconfig.Watcher"* ]] || fail "referrers not named: $err"
[[ "$err" != *"<UsedInIface> -> type"* ]] || fail "the store types' own edges count as referrers: $err"

if STUB_EXIT=1 "$check" gatewayd-public 2>/dev/null; then fail "a failed build passed"; fi
if "$check" no-such-daemon 2>/dev/null; then fail "an unknown daemon passed"; fi
echo "check_edge_store tests passed"
