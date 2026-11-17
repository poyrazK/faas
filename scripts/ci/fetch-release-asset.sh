#!/usr/bin/env bash
# fetch-release-asset.sh OWNER/REPO TAG ASSET SHA256 OUT — download one GitHub
# release asset to OUT, and keep it only if it matches the pinned SHA-256.
#
# github.com's release-download path can fail on its own while the GitHub API
# asset endpoint serves the same bytes: on 2026-11-08 release downloads
# returned 504 for over an hour (cosign, crane, grype) and failed the rc.329
# release four times. The download falls back to `gh release download` (the
# API endpoint; needs GH_TOKEN). The pin is checked either way, so the
# fallback cannot change what is used.
set -euo pipefail

repo="${1:?usage: fetch-release-asset.sh OWNER/REPO TAG ASSET SHA256 OUT}"
tag="${2:?tag}"
asset="${3:?asset}"
sha256="${4:?sha256}"
out="${5:?out}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  --retry 3 --retry-all-errors --retry-delay 2 \
  -o "$tmp/$asset" \
  "https://github.com/${repo}/releases/download/${tag}/${asset}"; then
  echo "fetch-release-asset: release download of ${repo} ${tag} ${asset} failed; fetching the same asset through the GitHub API" >&2
  rm -f "$tmp/$asset"
  gh release download "$tag" --repo "$repo" --pattern "$asset" --dir "$tmp"
fi

got="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
if [[ "$got" != "$sha256" ]]; then
  echo "fetch-release-asset: ${asset} checksum FAILED: got ${got}, want ${sha256}" >&2
  exit 1
fi
mkdir -p "$(dirname "$out")"
mv "$tmp/$asset" "$out"
