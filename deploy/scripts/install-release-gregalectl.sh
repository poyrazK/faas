#!/usr/bin/env bash
# Install the gregalectl of one signed release on this host before its first
# activation: a control plane rebuilt from backups needs it for
# `backup restore-cp-state`, `secrets init` and bootstrap's signd role, and
# only the first activation links it from /opt/faas/current
# (docs/runbooks/ControlPlaneStateBackup.md, "Rebuild a control plane from
# backups"). Use the release the old control plane ran.
#
# It verifies exactly as cd-controlplane does: SHA256SUMS, then cosign
# against the release workflow's identity for that tag, with the same pinned
# cosign. Run it on the host as root:
#
#   ssh <admin>@<host> 'sudo bash -s -- v0.1.18-rc.360' < deploy/scripts/install-release-gregalectl.sh
#
# It refuses a host that already has an active release: there CD owns
# /usr/local/bin/gregalectl.
set -euo pipefail

die() { echo "install-release-gregalectl: $*" >&2; exit 1; }

tag="${1:-}"
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "usage: install-release-gregalectl.sh RELEASE_TAG (got '${tag}')"
repo="${GREGALE_GITHUB_REPO:-poyrazK/faas}"
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "bad repository: $repo"
current="${GREGALE_CURRENT_RELEASE:-/opt/faas/current}"
dest="${GREGALE_GREGALECTL_DEST:-/usr/local/bin/gregalectl}"

# The same pins as cd-controlplane.yml's "Install pinned cosign verifier"
# (pkg/daemonunitspec checks they stay equal).
COSIGN_VERSION="2.4.1"
COSIGN_SHA256="8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b"

[[ "$(id -u)" == 0 ]] || die "run as root"
[[ "$(uname -s)/$(uname -m)" == Linux/x86_64 ]] || die "needs Linux on x86_64"
if [[ -e "$current" || -L "$current" ]]; then
  die "$current exists: this host has an active release, and CD manages $dest"
fi
for tool in curl sha256sum tar install; do
  command -v "$tool" >/dev/null || die "$tool is required"
done

work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
fetch() { curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --retry 3 --retry-all-errors -o "$1" "$2"; }

fetch "$work/cosign" "https://github.com/sigstore/cosign/releases/download/v${COSIGN_VERSION}/cosign-linux-amd64"
echo "${COSIGN_SHA256}  $work/cosign" | sha256sum -c - >/dev/null || die "cosign checksum mismatch"
chmod 0755 "$work/cosign"

for asset in release.tar.gz release.cosign.bundle SHA256SUMS; do
  fetch "$work/$asset" "https://github.com/${repo}/releases/download/${tag}/${asset}"
done
(
  cd "$work"
  grep -E '  (release\.tar\.gz|release\.cosign\.bundle)$' SHA256SUMS > checked
  [[ "$(wc -l < checked)" -eq 2 ]] || die "SHA256SUMS does not list release.tar.gz and release.cosign.bundle"
  sha256sum -c checked >/dev/null || die "release checksum mismatch"
)
TUF_ROOT="$work/sigstore-tuf" "$work/cosign" verify-blob \
  --bundle "$work/release.cosign.bundle" \
  --certificate-identity "https://github.com/${repo}/.github/workflows/release.yml@refs/tags/${tag}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$work/release.tar.gz" >/dev/null 2>&1 || die "cosign did not verify ${tag}'s release.tar.gz"

tar -xzf "$work/release.tar.gz" -C "$work" gregalectl
install -o root -g root -m 0755 "$work/gregalectl" "$dest"
echo "installed ${tag}'s gregalectl at $dest ($(sha256sum "$dest" | cut -c1-16)…)"
