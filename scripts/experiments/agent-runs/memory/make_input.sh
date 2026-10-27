#!/usr/bin/env bash
# Stage the run input: express at one tag as the repo, the diff to the next
# tag as PR.diff, and about 1.1 MB of real JavaScript as big.txt (script S2
# reads it in 50 KB sections, so tool output compresses like code does).
#
#   make_input.sh DEST OLD_TAG NEW_TAG ROOTFS
set -euo pipefail
dest=$1 old=$2 new=$3 rootfs=$4
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for tag in "$old" "$new"; do
  mkdir -p "$tmp/$tag"
  curl -fsSL "https://codeload.github.com/expressjs/express/tar.gz/refs/tags/$tag" |
    tar -xz -C "$tmp/$tag" --strip-components=1 --no-same-owner
done
cp -a "$tmp/$old" "$dest/repo"
(cd "$tmp" && diff -ruN "$old" "$new" > "$dest/PR.diff") || true
# head closes the pipe early; that SIGPIPE is expected.
{ find "$rootfs/usr/local/lib/node_modules" -name '*.js' -type f -print0 | sort -z | xargs -0 cat; } 2>/dev/null |
  head -c 1153434 > "$dest/big.txt" || true
[[ $(wc -c < "$dest/big.txt") -eq 1153434 ]] || { echo "make_input: big.txt too short" >&2; exit 1; }
wc -c "$dest/PR.diff" "$dest/big.txt"
