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

export GITHUB_EVENT_NAME=push GITHUB_REF=refs/tags/v1.2.3
export INPUT_REF="$GITHUB_SHA"
export GITHUB_EVENT_PATH="$tmp/event.json"
cat > "$GITHUB_EVENT_PATH" <<'EOF'
{"before":"0000000000000000000000000000000000000000","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created":true,"forced":false,"deleted":false}
EOF
: > "$GITHUB_OUTPUT"
verify_release_tag_push
if [ -s "$GITHUB_OUTPUT" ]; then
    echo 'new SemVer tag was not accepted' >&2
    exit 1
fi

for tag in v1.2.3-rc.1 v1.2.3+build.7; do
    export GITHUB_REF="refs/tags/$tag"
    verify_release_tag_push
done

for tag in v1.2 v01.2.3; do
    export GITHUB_REF="refs/tags/$tag"
    : > "$GITHUB_OUTPUT"
    if verify_release_tag_push; then
        echo "invalid SemVer release tag was accepted: $tag" >&2
        exit 1
    fi
    grep -qx 'status=skipped' "$GITHUB_OUTPUT"
done

export GITHUB_REF=refs/tags/v1.2.3
cat > "$GITHUB_EVENT_PATH" <<'EOF'
{"before":"0123456789abcdef0123456789abcdef01234567","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created":false,"forced":true,"deleted":false}
EOF
: > "$GITHUB_OUTPUT"
if verify_release_tag_push; then
    echo 'moved release tag was accepted' >&2
    exit 1
fi
grep -qx 'status=skipped' "$GITHUB_OUTPUT"

cat > "$GITHUB_EVENT_PATH" <<'EOF'
{"before":"0000000000000000000000000000000000000000","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created":true,"forced":false,"deleted":true}
EOF
: > "$GITHUB_OUTPUT"
if verify_release_tag_push; then
    echo 'deleted release tag was accepted' >&2
    exit 1
fi
grep -qx 'status=skipped' "$GITHUB_OUTPUT"

cat > "$GITHUB_EVENT_PATH" <<'EOF'
{"before":"0000000000000000000000000000000000000000","after":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","created":true,"forced":false,"deleted":false}
EOF
if (verify_release_tag_push) >/dev/null 2>&1; then
    echo 'tag deployment accepted a SHA different from the push payload' >&2
    exit 1
fi

echo 'deploy-action-ref-checks: OK'
