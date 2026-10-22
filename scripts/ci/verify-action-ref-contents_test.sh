#!/usr/bin/env bash
# Regression for rc.275: the pinned Action ref is the previous release,
# which vendors the bare bin/gregale, so requiring bin/gregale.gz there
# failed the release after v0 had already moved.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
# Fake gh: answers a file for every "<path>@<ref>" listed in $FAKE_FILES.
cat > "$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
url="$2"; path="${url#repos/*/*/contents/}"; path="${path%%\?ref=*}"; ref="${url##*\?ref=}"
for entry in $FAKE_FILES; do
	if [ "$entry" = "$path@$ref" ]; then printf '{"type":"file","size":42}'; exit 0; fi
done
echo "gh: Not Found (HTTP 404)" >&2; exit 1
GH
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH" GITHUB_REPOSITORY=poyrazK/faas
script="$root/scripts/ci/verify-action-ref-contents.sh"
a=.github/actions/deploy
pin=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
new_major="$a/action.yml@v0 $a/src/cli.sh@v0 $a/bin/gregale.gz@v0"

expect() {
	local want="$1" name="$2"; shift 2
	local got=0
	FAKE_FILES="$*" bash "$script" "$pin" v0 >/dev/null 2>&1 || got=$?
	if { [ "$want" = pass ] && [ "$got" -ne 0 ]; } || { [ "$want" = fail ] && [ "$got" -eq 0 ]; }; then
		echo "FAIL: $name (exit $got)" >&2
		exit 1
	fi
}

expect pass "pinned ref with the bare CLI (releases before rc.275)" "$a/action.yml@$pin $a/bin/gregale@$pin $new_major"
expect pass "pinned ref with the compressed CLI" "$a/action.yml@$pin $a/bin/gregale.gz@$pin $new_major"
expect fail "pinned ref without any CLI" "$a/action.yml@$pin $new_major"
expect fail "pinned ref without action.yml" "$a/bin/gregale@$pin $new_major"
expect fail "moving tag still on the bare CLI" "$a/action.yml@$pin $a/bin/gregale@$pin $a/action.yml@v0 $a/src/cli.sh@v0 $a/bin/gregale@v0"
expect fail "moving tag without cli.sh" "$a/action.yml@$pin $a/bin/gregale@$pin $a/action.yml@v0 $a/bin/gregale.gz@v0"
echo "verify-action-ref-contents: PASS"
