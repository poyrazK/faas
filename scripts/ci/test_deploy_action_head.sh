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
case "${@: -1}" in
*/git/tags/*) printf '%s' "$MOCK_TAG_JSON" ;;
*) printf '{"commit":{"sha":"%s"}}' "$MOCK_HEAD_SHA" ;;
esac
EOF
chmod +x "$tmp/curl"
export PATH="$tmp:$PATH"
export MOCK_CURL_URL_FILE="$tmp/url"
export GITHUB_EVENT_NAME=push GITHUB_REPOSITORY=acme/api
export GITHUB_REF=refs/heads/release/canary
export GITHUB_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export INPUT_REPO="$GITHUB_REPOSITORY" INPUT_REF="$GITHUB_SHA" INPUT_REF_EXPLICIT=false
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
if [ "$(current_run_source_branch)" != "release/canary" ]; then
    echo 'current branch push did not return its source branch' >&2
    exit 1
fi
export INPUT_REF_EXPLICIT=true
if [ -n "$(current_run_source_branch)" ]; then
    echo 'explicit ref was unexpectedly branch-bound' >&2
    exit 1
fi
export INPUT_REF_EXPLICIT=false

export MOCK_HEAD_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
if verify_current_push_head; then
    echo 'superseded push was accepted' >&2
    exit 1
fi
grep -qx 'status=skipped' "$GITHUB_OUTPUT"

export INPUT_REF_EXPLICIT=true INPUT_REF=cccccccccccccccccccccccccccccccccccccccc
 : > "$MOCK_CURL_URL_FILE"
: > "$GITHUB_OUTPUT"
verify_current_push_head
if [ -s "$GITHUB_OUTPUT" ] || [ -s "$MOCK_CURL_URL_FILE" ]; then
    echo 'explicit commit ref did not bypass branch-event preflight' >&2
    exit 1
fi
export INPUT_REF_EXPLICIT=false INPUT_REF="$GITHUB_SHA"

export MOCK_CURL_FAIL=1
if (verify_current_push_head) >/dev/null 2>&1; then
    echo 'GitHub API failure was treated as a current head' >&2
    exit 1
fi

export GITHUB_EVENT_NAME=push GITHUB_REF=refs/tags/v1.2.3
export INPUT_REF="$GITHUB_SHA"
if [ -n "$(current_run_source_branch)" ]; then
    echo 'tag push was unexpectedly branch-bound' >&2
    exit 1
fi
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

# Annotated tags: the push payload names the tag object, GITHUB_SHA the
# commit. Accept only the same tag object pointing directly at GITHUB_SHA.
unset MOCK_CURL_FAIL
export GITHUB_REF=refs/tags/v1.2.3
tag_object=dddddddddddddddddddddddddddddddddddddddd
tag_json() { # object tag type target
    printf '{"sha":"%s","tag":"%s","object":{"type":"%s","sha":"%s"}}' "$1" "$2" "$3" "$4"
}
printf '{"before":"%s","after":"%s","created":true,"forced":false,"deleted":false}' \
    0000000000000000000000000000000000000000 "$tag_object" > "$GITHUB_EVENT_PATH"
export MOCK_TAG_JSON; MOCK_TAG_JSON="$(tag_json "$tag_object" v1.2.3 commit "$GITHUB_SHA")"
: > "$GITHUB_OUTPUT"
: > "$MOCK_CURL_URL_FILE"
verify_release_tag_push
if [ -s "$GITHUB_OUTPUT" ] || ! grep -q "/repos/acme/api/git/tags/$tag_object" "$MOCK_CURL_URL_FILE"; then
    echo 'annotated release tag pointing at GITHUB_SHA was not accepted' >&2
    exit 1
fi
for bad in \
    "$(tag_json "$tag_object" v1.2.3 commit bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)" \
    "$(tag_json "$tag_object" v9.9.9 commit "$GITHUB_SHA")" \
    "$(tag_json "$tag_object" v1.2.3 tag "$GITHUB_SHA")" \
    "$(tag_json eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee v1.2.3 commit "$GITHUB_SHA")"; do
    MOCK_TAG_JSON="$bad"
    if (verify_release_tag_push) >/dev/null 2>&1; then
        echo "annotated tag with a mismatched target was accepted: $bad" >&2
        exit 1
    fi
done
MOCK_TAG_JSON="$(tag_json "$tag_object" v1.2.3 commit "$GITHUB_SHA")"
export MOCK_CURL_FAIL=1
if (verify_release_tag_push) >/dev/null 2>&1; then
    echo 'annotated tag was accepted without GitHub confirming its target' >&2
    exit 1
fi
unset MOCK_CURL_FAIL

echo 'deploy-action-ref-checks: OK'
