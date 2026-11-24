#!/usr/bin/env bash
# Publishes packaging/cursor-plugin to its public mirror,
# github.com/gregale-dev/gregale-cursor-plugin, which the Cursor Marketplace
# reviews and installs from. Run it from the repository root after changing
# the plugin (and bumping its version); it needs push access to the mirror.
#
# usage: scripts/publish-cursor-plugin.sh [--dry-run]
set -euo pipefail

mirror="${CURSOR_PLUGIN_MIRROR:-git@github.com:gregale-dev/gregale-cursor-plugin.git}"
src="packaging/cursor-plugin"
[ -f "$src/.cursor-plugin/plugin.json" ] || { echo "run from the faas repository root" >&2; exit 1; }

version="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$src/.cursor-plugin/plugin.json")"
source_commit="$(git rev-parse --short HEAD)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git clone --quiet --depth 1 "$mirror" "$work/mirror"
# Replace everything but the mirror's .git with the plugin source.
find "$work/mirror" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
cp -R "$src/." "$work/mirror/"

cd "$work/mirror"
git add -A
if git diff --cached --quiet; then
  echo "mirror already matches $src"
  exit 0
fi
git commit --quiet -m "gregale plugin $version (faas $source_commit)"
if [ "${1:-}" = "--dry-run" ]; then
  git show --stat HEAD
  exit 0
fi
git push --quiet origin HEAD
echo "published gregale plugin $version to $mirror"
