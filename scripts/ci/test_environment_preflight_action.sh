#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

action_path="$tmp/action/.github/actions/environment-preflight"
mkdir -p "$action_path/src" "$tmp/action/.github/actions/deploy/bin" "$tmp/action/.github/actions/deploy/src" "$tmp/runner-bin"
cp "$root/.github/actions/environment-preflight/src/run.sh" "$action_path/src/run.sh"
cp "$root/.github/actions/deploy/src/version.txt" "$tmp/action/.github/actions/deploy/src/version.txt"
cp "$root/scripts/ci/fixtures/environment_preflight_fake_gregale.sh" "$tmp/action/.github/actions/deploy/bin/gregale"
cp "$root/scripts/ci/fixtures/environment_preflight_fake_curl.sh" "$tmp/runner-bin/curl"
chmod +x "$tmp/action/.github/actions/deploy/bin/gregale" "$tmp/runner-bin/curl"

setup_run() {
	export ACTION_PATH="$action_path"
	export PATH="$tmp/runner-bin:$PATH"
	export GITHUB_WORKSPACE="$root"
	export GITHUB_OUTPUT="$tmp/output"
	export GITHUB_STEP_SUMMARY="$tmp/summary"
	export ACTION_TEST_ARGS="$tmp/args"
	export ACTION_TEST_TOKEN="$tmp/token"
	export ACTION_TEST_API="$tmp/api"
	export ACTION_TEST_EXCHANGE_REQUEST="$tmp/exchange-request.json"
	export ACTION_TEST_CURL_SEEN="$tmp/curl-seen"
	export ACTIONS_ID_TOKEN_REQUEST_URL=https://actions.example.test/token?api-version=2.0
	export ACTIONS_ID_TOKEN_REQUEST_TOKEN=actions-request-token-secret
	export INPUT_API_KEY=""
	export INPUT_OIDC_AUDIENCE=gregale
	export INPUT_API_BASE=https://api.example.test/
	export INPUT_PROJECT=shop
	export INPUT_FROM=staging
	export INPUT_TO=production
	export INPUT_PROFILE="$root/.github/actions/environment-preflight/README.md"
	export INPUT_SYNC_CONFIG=false
	export ACTION_TEST_STATUS=ready
	export ACTION_TEST_BAD_SCOPES=false
	: >"$GITHUB_OUTPUT"
	: >"$GITHUB_STEP_SUMMARY"
	rm -f "$ACTION_TEST_ARGS" "$ACTION_TEST_TOKEN" "$ACTION_TEST_API" "$ACTION_TEST_CURL_SEEN" "$ACTION_TEST_EXCHANGE_REQUEST"
}

assert_no_token_leak() {
	local token="$1" file
	for file in "$GITHUB_OUTPUT" "$GITHUB_STEP_SUMMARY" "$tmp/stdout" "$tmp/stderr"; do
		if grep -Fq "$token" "$file"; then
			echo "credential leaked to $file" >&2
			exit 1
		fi
	done
}

setup_run
if ! bash "$action_path/src/run.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
	cat "$tmp/stderr" >&2
	exit 1
fi
grep -qx 'status=ready' "$GITHUB_OUTPUT"
grep -qx 'qualification-id=q-123' "$GITHUB_OUTPUT"
grep -qx 'can-promote=true' "$GITHUB_OUTPUT"
grep -qx 'approval-required=true' "$GITHUB_OUTPUT"
grep -qx 'blocking-reasons=\[\]' "$GITHUB_OUTPUT"
grep -Fqx 'fp_oidc_0123456789abcdef0123456789abcdef0123456789abcdef' "$ACTION_TEST_TOKEN"
grep -Fqx 'https://api.example.test' "$ACTION_TEST_API"
grep -Fq -- '--sync-config' "$ACTION_TEST_ARGS" && {
	echo 'sync-config=false unexpectedly enabled the config preview' >&2
	exit 1
}
jq -e '.provider == "github" and .capability == "environment-preflight" and .aud == "gregale" and .token == "fake-github-id-token"' "$ACTION_TEST_EXCHANGE_REQUEST" >/dev/null
assert_no_token_leak 'fp_oidc_0123456789abcdef0123456789abcdef0123456789abcdef'
assert_no_token_leak 'fake-github-id-token'

setup_run
export INPUT_API_KEY=fp_live_test_api_key
if ! bash "$action_path/src/run.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
	cat "$tmp/stderr" >&2
	exit 1
fi
[[ "$(<"$ACTION_TEST_TOKEN")" == "$INPUT_API_KEY" ]]
[[ ! -e "$ACTION_TEST_CURL_SEEN" ]]
assert_no_token_leak "$INPUT_API_KEY"

setup_run
export INPUT_API_KEY=fp_live_test_api_key ACTION_TEST_STATUS=blocked
if bash "$action_path/src/run.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
	echo 'blocked promotion preview unexpectedly passed the Action' >&2
	exit 1
fi
grep -qx 'status=blocked' "$GITHUB_OUTPUT"
grep -qx 'can-promote=false' "$GITHUB_OUTPUT"
grep -Fq 'release is not promotable' "$GITHUB_STEP_SUMMARY"

setup_run
export ACTION_TEST_BAD_SCOPES=true
if bash "$action_path/src/run.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
	echo 'an OIDC bearer with broad scopes was accepted' >&2
	exit 1
fi
if [[ -e "$ACTION_TEST_TOKEN" ]]; then
	echo 'CLI ran after OIDC returned a bearer with unexpected scopes' >&2
	exit 1
fi

setup_run
export INPUT_API_KEY=fp_live_test_api_key INPUT_API_BASE=http://api.example.test
if bash "$action_path/src/run.sh" >"$tmp/stdout" 2>"$tmp/stderr"; then
	echo 'an insecure non-local API URL was accepted' >&2
	exit 1
fi
grep -qx 'status=failed' "$GITHUB_OUTPUT"
if [[ -e "$ACTION_TEST_ARGS" ]]; then
	echo 'CLI ran against an insecure API URL' >&2
	exit 1
fi

echo 'environment preflight Action contract: OK'
