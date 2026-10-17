#!/usr/bin/env bash
# Verify a downloaded per-unit release artifact before anything reaches a
# host (docs/adr/1650-per-unit-control-plane-releases.md).
#
#   verify_unit_release.sh DIR
#
# DIR holds units.tar.gz, units.cosign.bundle, units-manifest.json and
# SHA256SUMS as release-units.yml uploads them. The tarball must be signed
# by release-units.yml on main, its manifest must equal the uploaded one,
# and it must hold exactly the binaries the manifest names, each with its
# recorded digest. Unpacks into DIR/unpacked and prints the release commit.
# UNIT_RELEASE_IDENTITY overrides the signing identity (tests only).
set -euo pipefail

dir="${1:?usage: verify_unit_release.sh DIR}"
identity="${UNIT_RELEASE_IDENTITY:-https://github.com/poyrazK/faas/.github/workflows/release-units.yml@refs/heads/main}"
umask 0022
(cd "$dir" && sha256sum -c --quiet SHA256SUMS) >&2
# Only release-units.yml on main signs deployable unit releases.
cosign verify-blob \
  --bundle "$dir/units.cosign.bundle" \
  --certificate-identity "$identity" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$dir/units.tar.gz" >&2
rm -rf -- "$dir/unpacked"
mkdir -p "$dir/unpacked"
tar -xzf "$dir/units.tar.gz" -C "$dir/unpacked" --no-same-owner
manifest="$dir/unpacked/units-manifest.json"
cmp "$manifest" "$dir/units-manifest.json" >&2
jq -e '.kind == "gregale-unit-release" and .format_version == 2 and .target == "linux/amd64"' "$manifest" >/dev/null || {
  echo "verify_unit_release: not a format 2 linux/amd64 unit release" >&2
  exit 1
}
# The tarball holds exactly the binaries the signed manifest names.
jq -r '.units | to_entries[] | "\(.value.sha256)  bin/\(.key)"' "$manifest" | (cd "$dir/unpacked" && sha256sum -c --quiet -) >&2
if [[ "$(find "$dir/unpacked/bin" -type f | wc -l | tr -d ' ')" -ne "$(jq '.units | length' "$manifest")" ]]; then
  echo "verify_unit_release: the tarball carries binaries its manifest does not name" >&2
  exit 1
fi
git_sha="$(jq -er .git_sha "$manifest")"
[[ "$git_sha" =~ ^[0-9a-f]{40}$ ]] || {
  echo "verify_unit_release: bad release commit $git_sha" >&2
  exit 1
}
printf '%s\n' "$git_sha"
