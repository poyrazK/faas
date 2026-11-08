#!/usr/bin/env bash
# Keep shipping the previous signed release's guest-init while its code is the
# same as the one just built. Exit 3 when the release must ship the new binary
# (no prior signed release, or its guest-init runs different code); integrity
# and tooling failures stay fatal.
#
# Every runtime base embeds guest-init as PID 1, and ADR-510 refuses to restore
# a snapshot onto a base whose bytes differ from the one it was captured on, so
# a guest-init whose bytes change on every release retires every snapshot on
# every release. The kernel is reused the same way (reuse-release-kernel.sh).
# See docs/adr/20261105150000-guest-init-reuse-keeps-bases-and-snapshots.md.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
init_file=""
git_sha=""

usage() {
  echo "usage: reuse-release-guest-init.sh --init PATH --git-sha SHA" >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --init)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      init_file=$2
      shift 2
      ;;
    --git-sha)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      git_sha=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

[[ -n "$init_file" && "$git_sha" =~ ^[0-9a-f]{40}$ ]] || {
  usage
  exit 2
}
[[ -f "$init_file" ]] || { echo "reuse-release-guest-init: $init_file is not a file" >&2; exit 2; }
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must name the release repository}"
for tool in gh cosign jq python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "reuse-release-guest-init: $tool is required" >&2; exit 2; }
done

# The newest signed release reachable from this commit: the one whose guest-init
# the fleet's bases hold. An unsigned or unpublished tag is skipped, as for the
# kernel. Only that release is compared; code equality is transitive, so a
# chain of releases with the same code keeps the same bytes.
previous_tag=""
while IFS= read -r candidate; do
  [[ "$candidate" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || continue
  [[ "$candidate" != "${GITHUB_REF_NAME:-}" ]] || continue
  [[ "$(git -C "$repo_root" rev-list -n 1 "$candidate")" != "$git_sha" ]] || continue
  assets=$(gh release view "$candidate" --repo "$GITHUB_REPOSITORY" \
    --json assets --jq '[.assets[].name] | sort | join("\n")' 2>/dev/null || true)
  grep -Fxq release.tar.gz <<< "$assets" || continue
  grep -Fxq release.cosign.bundle <<< "$assets" || continue
  previous_tag=$candidate
  break
done < <(git -C "$repo_root" tag --merged "$git_sha" --sort=-version:refname)

if [[ -z "$previous_tag" ]]; then
  echo "no prior signed release to take guest-init from"
  exit 3
fi

work_root=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gregale-guest-init-reuse.XXXXXX")
trap 'rm -rf "$work_root"' EXIT
gh release download "$previous_tag" --repo "$GITHUB_REPOSITORY" \
  --pattern release.tar.gz \
  --pattern release.cosign.bundle \
  --dir "$work_root"
cosign verify-blob \
  --bundle "$work_root/release.cosign.bundle" \
  --certificate-identity "https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@refs/tags/${previous_tag}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$work_root/release.tar.gz"

tar -xOzf "$work_root/release.tar.gz" release-manifest.json > "$work_root/release-manifest.json"
prior_release_sha=$(jq -er '.git_sha' "$work_root/release-manifest.json")
[[ "$prior_release_sha" == "$(git -C "$repo_root" rev-list -n 1 "$previous_tag")" ]] || {
  echo "reuse-release-guest-init: signed manifest does not match $previous_tag" >&2
  exit 1
}
# A release from before the manifest hashed guest-init has nothing to verify
# the bytes against: ship the new one.
expected_digest=$(jq -r '.tool_hashes.init // empty' "$work_root/release-manifest.json")
if [[ -z "$expected_digest" ]]; then
  echo "$previous_tag's signed manifest has no guest-init digest"
  exit 3
fi
[[ "$expected_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo "reuse-release-guest-init: signed manifest has an invalid guest-init digest" >&2
  exit 1
}
tar -xOzf "$work_root/release.tar.gz" init > "$work_root/init"
actual_digest="sha256:$(sha256sum "$work_root/init" | awk '{print $1}')"
[[ "$actual_digest" == "$expected_digest" ]] || {
  echo "reuse-release-guest-init: guest-init digest does not match signed manifest" >&2
  exit 1
}

# compare exits 1 for different code and 2 for a file it cannot read.
verdict=$(python3 -I "$repo_root/scripts/ci/guest_init_reuse.py" compare \
  --head "$init_file" --prior "$work_root/init") && status=0 || status=$?
case "$status" in
  0) ;;
  1)
    echo "guest-init code differs from $previous_tag's ($actual_digest): shipping the new binary"
    exit 3
    ;;
  *)
    echo "reuse-release-guest-init: comparing guest-init failed" >&2
    exit 1
    ;;
esac

install -m 0755 "$work_root/init" "$init_file"
echo "reused guest-init from $previous_tag ($actual_digest, $verdict)"
