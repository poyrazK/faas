#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
resolver="$repo_root/scripts/ci/resolve-github-action-pin.sh"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fake_bin="$tmpdir/bin"
mkdir -p "$fake_bin"
cat > "$fake_bin/git" <<'GIT_STUB'
#!/usr/bin/env bash
if [[ "$#" -ne 5 || "$1" != "ls-remote" || "$2" != "--exit-code" || "$3" != "https://github.com/poyrazK/faas.git" || "$4" != "refs/tags/v0" || "$5" != 'refs/tags/v0^{}' ]]; then
	printf 'unexpected git arguments: %s\n' "$*" >&2
	exit 97
fi
printf '%s\n' "$GIT_LS_REMOTE_OUTPUT"
exit "${GIT_LS_REMOTE_STATUS:-0}"
GIT_STUB
chmod +x "$fake_bin/git"

expect_pin() {
	local name="$1"
	local output="$2"
	local expected="$3"
	local actual
	actual="$(PATH="$fake_bin:$PATH" GIT_LS_REMOTE_OUTPUT="$output" bash "$resolver")"
	if [[ "$actual" != "$expected" ]]; then
		printf '%s: resolved %q, want %q\n' "$name" "$actual" "$expected" >&2
		exit 1
	fi
}

expect_failure() {
	local name="$1"
	local output="$2"
	local status="${3:-0}"
	if PATH="$fake_bin:$PATH" GIT_LS_REMOTE_OUTPUT="$output" GIT_LS_REMOTE_STATUS="$status" bash "$resolver" >"$tmpdir/output" 2>&1; then
		printf '%s: expected resolver failure\n' "$name" >&2
		exit 1
	fi
}

expect_pin \
	"lightweight tag" \
	$'29aa7ea51128dca7f8e823f86a9ac0e482357737\trefs/tags/v0' \
	"29aa7ea51128dca7f8e823f86a9ac0e482357737"
expect_pin \
	"annotated tag prefers peeled commit" \
	$'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\trefs/tags/v0\n29aa7ea51128dca7f8e823f86a9ac0e482357737\trefs/tags/v0^{}' \
	"29aa7ea51128dca7f8e823f86a9ac0e482357737"
expect_failure "missing tag" $'29aa7ea51128dca7f8e823f86a9ac0e482357737\trefs/tags/v1'
expect_failure "invalid SHA" $'not-a-sha\trefs/tags/v0'
expect_failure "duplicate tag" $'29aa7ea51128dca7f8e823f86a9ac0e482357737\trefs/tags/v0\n29aa7ea51128dca7f8e823f86a9ac0e482357737\trefs/tags/v0'
expect_failure "git lookup error" "" 2

printf 'GitHub Action pin resolver contract: PASS\n'
