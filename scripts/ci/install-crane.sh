#!/usr/bin/env bash
# install-crane.sh DEST_DIR — install the checksum-pinned crane
# (go-containerregistry v0.21.1, linux/amd64) into DEST_DIR, through
# fetch-release-asset.sh (pin checked; GitHub API fallback, needs GH_TOKEN).
set -euo pipefail

dest="${1:?usage: install-crane.sh DEST_DIR}"
asset=go-containerregistry_Linux_x86_64.tar.gz

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
bash "$(dirname "${BASH_SOURCE[0]}")/fetch-release-asset.sh" \
  google/go-containerregistry v0.21.1 "$asset" \
  ffca5eafe0af8078f2c627fb852104f9d762fad1eb36c04e93ca14fce76d47d2 \
  "$tmp/$asset"
mkdir -p "$dest"
tar -xzf "$tmp/$asset" -C "$dest" crane
chmod 0755 "$dest/crane"
