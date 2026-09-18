#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
helper="$repo_root/scripts/ci/upload-release-assets.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/bin" "$fixture/state" "$fixture/assets"

cat >"$fixture/bin/gh" <<'FAKE_GH'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-} ${2:-}" in
"api repos/"*)
  if [[ -f "$FAKE_GH_STATE/api-fail-once" ]]; then
    mv "$FAKE_GH_STATE/api-fail-once" "$FAKE_GH_STATE/api-failed"
    exit 1
  fi
  {
    shopt -s nullglob
    for digest_file in "$FAKE_GH_STATE"/*.digest; do
      name="$(basename "$digest_file" .digest)"
      digest="$(<"$digest_file")"
      jq -cn --arg name "$name" --arg digest "$digest" '{name:$name,digest:$digest}'
    done
  } | jq -sc '{assets:.}'
  ;;
"release upload")
  path="$4"
  name="$(basename "$path")"
  printf '%s\n' "$name" >>"$FAKE_GH_LOG"
  if [[ -f "$FAKE_GH_STATE/$name.fail-once" ]]; then
    mv "$FAKE_GH_STATE/$name.fail-once" "$FAKE_GH_STATE/$name.failed"
    exit 1
  fi
  printf 'sha256:%s\n' "$(sha256sum "$path" | awk '{print $1}')" \
    >"$FAKE_GH_STATE/$name.digest"
  ;;
*)
  echo "unexpected fake gh invocation: $*" >&2
  exit 64
  ;;
esac
FAKE_GH

cat >"$fixture/bin/sleep" <<'FAKE_SLEEP'
#!/usr/bin/env bash
exit 0
FAKE_SLEEP
chmod +x "$fixture/bin/gh" "$fixture/bin/sleep"

export PATH="$fixture/bin:$PATH"
export FAKE_GH_STATE="$fixture/state"
export FAKE_GH_LOG="$fixture/upload.log"
export GITHUB_REPOSITORY="example/gregale"
export RELEASE_TAG="v1.2.3"
export GH_TOKEN="test-token"

printf 'already present\n' >"$fixture/assets/existing.txt"
printf 'sha256:%s\n' \
  "$(sha256sum "$fixture/assets/existing.txt" | awk '{print $1}')" \
  >"$fixture/state/existing.txt.digest"
: >"$fixture/state/api-fail-once"
: >"$FAKE_GH_LOG"
"$helper" "$fixture/assets/existing.txt"
[[ ! -s "$FAKE_GH_LOG" ]] || {
  echo "helper re-uploaded an asset whose digest already matched" >&2
  exit 1
}

printf 'new asset\n' >"$fixture/assets/new.txt"
"$helper" "$fixture/assets/new.txt"
[[ "$(grep -c '^new.txt$' "$FAKE_GH_LOG")" == 1 ]] || {
  echo "helper did not upload a missing asset exactly once" >&2
  exit 1
}

printf 'retry asset\n' >"$fixture/assets/retry.txt"
: >"$fixture/state/retry.txt.fail-once"
"$helper" "$fixture/assets/retry.txt"
[[ "$(grep -c '^retry.txt$' "$FAKE_GH_LOG")" == 2 ]] || {
  echo "helper did not retry a transient upload failure exactly once" >&2
  exit 1
}

printf 'immutable asset\n' >"$fixture/assets/immutable.txt"
printf '%s\n' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  >"$fixture/state/immutable.txt.digest"
if "$helper" "$fixture/assets/immutable.txt" >/dev/null 2>&1; then
  echo "helper overwrote a release asset with a conflicting digest" >&2
  exit 1
fi
if grep -q '^immutable.txt$' "$FAKE_GH_LOG"; then
  echo "helper attempted an upload after detecting a conflicting digest" >&2
  exit 1
fi

echo "sequential release asset uploader regression: PASS"
