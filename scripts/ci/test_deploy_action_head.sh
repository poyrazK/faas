#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s' "${@: -1}" > "$MOCK_CURL_URL_FILE"
if [ "${MOCK_CURL_FAIL:-}" = 1 ]; then
    exit 22
fi
printf '{"commit":{"sha":"%s"}}' "$MOCK_HEAD_SHA"
EOF
chmod +x "$tmp/curl"
export PATH="$tmp:$PATH"
export MOCK_CURL_URL_FILE="$tmp/url"
export GITHUB_EVENT_NAME=push GITHUB_REPOSITORY=acme/api
export GITHUB_REF=refs/heads/release/canary
export GITHUB_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export INPUT_REPO="$GITHUB_REPOSITORY" INPUT_REF="$GITHUB_SHA"
export GITHUB_TOKEN=test-token GITHUB_API_URL=https://github.example.test
export GITHUB_OUTPUT="$tmp/output"

# Source to exercise only the preflight without a vendored release binary.
source "$root/.github/actions/deploy/src/run.sh"
export MOCK_HEAD_SHA="$GITHUB_SHA"
verify_current_push_head
if [ -s "$GITHUB_OUTPUT" ] || ! grep -q '/repos/acme/api/branches/release%2Fcanary' "$MOCK_CURL_URL_FILE"; then
    echo 'current head was not accepted or branch was not encoded' >&2
    exit 1
fi

export MOCK_HEAD_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
if verify_current_push_head; then
    echo 'superseded push was accepted' >&2
    exit 1
fi
grep -qx 'status=skipped' "$GITHUB_OUTPUT"

export MOCK_CURL_FAIL=1
if (verify_current_push_head) >/dev/null 2>&1; then
    echo 'GitHub API failure was treated as a current head' >&2
    exit 1
fi

echo 'deploy-action-head-check: OK'
