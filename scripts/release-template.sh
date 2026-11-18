#!/usr/bin/env bash
# Print the absolute path of the production topology template a release
# materializes: the one file name that deploy/manifest/production/release-template
# names. The release tag therefore decides which fleet a release targets, and
# a mutable CI variable cannot.
#
# usage: release-template.sh [POINTER_FILE]
#   POINTER_FILE defaults to deploy/manifest/production/release-template. The
#   template is looked up beside the pointer, never elsewhere.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
POINTER="${1:-$REPO_ROOT/deploy/manifest/production/release-template}"

fail() {
  echo "release-template: $*" >&2
  exit 1
}

[[ -f "$POINTER" ]] || fail "pointer not found: $POINTER"
names=()
while IFS= read -r line || [[ -n "$line" ]]; do
  line="${line%%#*}"
  line="${line//[[:space:]]/}"
  [[ -n "$line" ]] && names+=("$line")
done <"$POINTER"
((${#names[@]} == 1)) || fail "$POINTER must name exactly one template, found ${#names[@]}"
name="${names[0]}"
# A bare file name: no directory part, so the pointer cannot reach a file
# outside the production directory.
[[ "$name" =~ ^[a-z0-9][a-z0-9-]*\.template\.yaml$ ]] || fail "$POINTER names '$name', want <name>.template.yaml in the same directory"
template="$(cd "$(dirname "$POINTER")" && pwd)/$name"
[[ -f "$template" ]] || fail "$POINTER names $name, which does not exist"
printf '%s\n' "$template"
