#!/usr/bin/env bash
# install-crane.sh DEST_DIR — install the checksum-pinned crane
# (go-containerregistry v0.21.1, linux/amd64) into DEST_DIR.
#
# github.com's release-download path can fail on its own while the GitHub API
# asset endpoint serves the same bytes: on 2026-11-08 every
# google/go-containerregistry tarball returned 504 there for over 30 minutes
# and failed the rc.329 release twice. The download falls back to
# `gh release download` (the API endpoint; needs GH_TOKEN). The SHA-256 pin is
# checked either way, so the fallback cannot change what is installed.
set -euo pipefail

dest="${1:?usage: install-crane.sh DEST_DIR}"
version=0.21.1
sha256=ffca5eafe0af8078f2c627fb852104f9d762fad1eb36c04e93ca14fce76d47d2
asset=go-containerregistry_Linux_x86_64.tar.gz

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  --retry 3 --retry-all-errors --retry-delay 2 \
  -o "$tmp/$asset" \
  "https://github.com/google/go-containerregistry/releases/download/v${version}/${asset}"; then
  echo "install-crane: release download failed; fetching the same asset through the GitHub API" >&2
  rm -f "$tmp/$asset"
  gh release download "v${version}" --repo google/go-containerregistry \
    --pattern "$asset" --dir "$tmp"
fi

got="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
if [[ "$got" != "$sha256" ]]; then
  echo "install-crane: $asset checksum FAILED: got $got, want $sha256" >&2
  exit 1
fi
mkdir -p "$dest"
tar -xzf "$tmp/$asset" -C "$dest" crane
chmod 0755 "$dest/crane"
