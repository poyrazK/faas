#!/usr/bin/env bash
# Reuse the kernel from the newest reachable signed release when the kernel
# build inputs have not changed. Exit 3 when no reusable release exists so the
# caller can build a fresh kernel; integrity and tooling failures stay fatal.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output=""
git_sha=""

usage() {
  echo "usage: reuse-release-kernel.sh --output PATH --git-sha SHA" >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      output=$2
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

[[ -n "$output" && "$git_sha" =~ ^[0-9a-f]{40}$ ]] || {
  usage
  exit 2
}
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must name the release repository}"
command -v gh >/dev/null 2>&1 || { echo "reuse-release-kernel: gh is required" >&2; exit 2; }
command -v cosign >/dev/null 2>&1 || { echo "reuse-release-kernel: cosign is required" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "reuse-release-kernel: jq is required" >&2; exit 2; }

kernel_paths=(
  deploy/ansible/roles/firecracker/defaults/main.yml
  scripts/build-firecracker-kernel.sh
)
current_source=$(git -C "$repo_root" log --first-parent --format='%H' -n 1 \
  "$git_sha" -- "${kernel_paths[@]}")
[[ "$current_source" =~ ^[0-9a-f]{40}$ ]] || {
  echo "reuse-release-kernel: could not resolve current kernel source" >&2
  exit 2
}

previous_tag=""
while IFS= read -r candidate; do
  [[ "$candidate" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || continue
  [[ "$candidate" != "${GITHUB_REF_NAME:-}" ]] || continue
  candidate_sha=$(git -C "$repo_root" rev-list -n 1 "$candidate")
  candidate_source=$(git -C "$repo_root" log --first-parent --format='%H' -n 1 \
    "$candidate_sha" -- "${kernel_paths[@]}")
  [[ "$candidate_source" == "$current_source" ]] || continue
  assets=$(gh release view "$candidate" --repo "$GITHUB_REPOSITORY" \
    --json assets --jq '[.assets[].name] | sort | join("\n")' 2>/dev/null || true)
  grep -Fxq release.tar.gz <<< "$assets" || continue
  grep -Fxq release.cosign.bundle <<< "$assets" || continue
  previous_tag=$candidate
  break
done < <(git -C "$repo_root" tag --merged "$git_sha" --sort=-version:refname)

if [[ -z "$previous_tag" ]]; then
  echo "no prior signed release has the current kernel inputs"
  exit 3
fi

work_root=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gregale-kernel-reuse.XXXXXX")
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
  echo "reuse-release-kernel: signed manifest does not match $previous_tag" >&2
  exit 1
}
expected_digest=$(jq -er '.tool_hashes.vmlinux' "$work_root/release-manifest.json")
[[ "$expected_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo "reuse-release-kernel: signed manifest has no valid vmlinux digest" >&2
  exit 1
}
tar -xOzf "$work_root/release.tar.gz" vmlinux > "$work_root/vmlinux"
actual_digest="sha256:$(sha256sum "$work_root/vmlinux" | awk '{print $1}')"
[[ "$actual_digest" == "$expected_digest" ]] || {
  echo "reuse-release-kernel: vmlinux digest does not match signed manifest" >&2
  exit 1
}
file "$work_root/vmlinux" | grep -Eq 'ELF 64-bit.*x86-64' || {
  echo "reuse-release-kernel: prior vmlinux is not an x86-64 ELF" >&2
  exit 1
}
mkdir -p "$(dirname "$output")"
install -m 0644 "$work_root/vmlinux" "$output"
echo "reused release kernel from $previous_tag ($actual_digest)"
