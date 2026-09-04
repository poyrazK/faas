#!/usr/bin/env bash
# Build the deterministic unsigned portion of ADR-113's canonical release.
# The CI workflow adds the keyless cosign bundle and SPDX SBOM after this
# script returns; local callers may use it to inspect the exact tarball bytes.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
git_sha=${GIT_SHA:-$(git -C "$repo_root" rev-parse HEAD)}
out_dir=${OUT_DIR:-"$repo_root/out"}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "neither sha256sum nor shasum is available" >&2
    exit 2
  fi
}

# Prefer the materialized manifest itself as the source of the hash. The
# legacy MANIFEST_HASH input remains accepted for local callers, but when
# both are supplied they must agree. This prevents a release from signing
# one manifest while publishing a hash for different bytes.
manifest_file=${MANIFEST_FILE:-}
if [[ -n "$manifest_file" ]]; then
  [[ -f "$manifest_file" ]] || {
    echo "MANIFEST_FILE does not exist: $manifest_file" >&2
    exit 2
  }
  manifest_hash="sha256:$(sha256_file "$manifest_file")"
  if [[ -n "${MANIFEST_HASH:-}" && "$MANIFEST_HASH" != "$manifest_hash" ]]; then
    echo "MANIFEST_HASH=$MANIFEST_HASH does not match $manifest_file ($manifest_hash)" >&2
    exit 2
  fi
elif [[ -n "${MANIFEST_HASH:-}" ]]; then
  manifest_hash=$MANIFEST_HASH
else
  echo "set MANIFEST_FILE or MANIFEST_HASH=sha256:<64hex>" >&2
  exit 2
fi

case "$git_sha" in
  [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]* ) ;;
  * ) echo "GIT_SHA must be a lowercase hexadecimal commit SHA" >&2; exit 2 ;;
esac
if [[ ! "$git_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "GIT_SHA must be exactly 40 lowercase hexadecimal characters" >&2
  exit 2
fi
if [[ ! "$manifest_hash" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "MANIFEST_HASH must match sha256:<64 lowercase hexadecimal characters>" >&2
  exit 2
fi

work_root=$(mktemp -d "${TMPDIR:-/tmp}/gregale-release.XXXXXX")
trap 'rm -rf "$work_root"' EXIT
mkdir -p "$work_root/$git_sha/bin" "$out_dir"
rm -f "$out_dir/release.tar.gz" "$out_dir/release-manifest.json" "$out_dir/release.cosign.bundle" "$out_dir/release.sbom.json" "$out_dir/SHA256SUMS"

make -C "$repo_root" \
  BINDIR="$work_root/$git_sha/bin" \
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 VERSION="$git_sha" build

if [[ -n "${KERNEL_FILE:-}" ]]; then
  [[ -f "$KERNEL_FILE" ]] || {
    echo "KERNEL_FILE does not exist: $KERNEL_FILE" >&2
    exit 2
  }
  [[ -r "$KERNEL_FILE" ]] || {
    echo "KERNEL_FILE is not readable: $KERNEL_FILE" >&2
    exit 2
  }
  # vmlinux is a release support artifact, not a daemon. Copy it into the
  # canonical bin tree so releaseinstall hashes it into tool_hashes and the
  # signed tarball carries the exact kernel every compute node will use.
  install -m 0644 "$KERNEL_FILE" "$work_root/$git_sha/bin/vmlinux"
fi

go -C "$repo_root" run ./cmd/release-artifact \
  --root "$work_root" \
  --git-sha "$git_sha" \
  --manifest-hash "$manifest_hash" \
  --out-dir "$out_dir"

if [[ -n "${SYFT_BIN:-}" ]]; then
  "$SYFT_BIN" "dir:$work_root/$git_sha" -o "spdx-json=$out_dir/release.sbom.json"
fi

if [[ -f "$out_dir/release.sbom.json" ]]; then
  (cd "$out_dir" && sha256sum release.tar.gz release.sbom.json > SHA256SUMS)
else
  (cd "$out_dir" && sha256sum release.tar.gz > SHA256SUMS)
fi
echo "canonical tarball ready in $out_dir"
