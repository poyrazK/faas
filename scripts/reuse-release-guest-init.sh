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
#
# Amendment 2026-11-11 (release train): with GUEST_INIT_HOLD_HOURS set, a
# guest-init whose code changed only through the shared tables below is still
# held back (the prior bytes ship) until the held code is that many hours old
# or a commit asks with a "Guest-Init-Ship: now" trailer. Any other change to
# the code guest-init links ships with its release.
# --provenance-out writes guest-init.json: the shipped digest and the commit
# its code came from, which the next release reads to know the held age.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
init_file=""
git_sha=""
provenance_out=""
hold_hours="${GUEST_INIT_HOLD_HOURS:-0}"

# Packages in guest-init's closure whose changes may be held: shared tables
# that guest-init links but whose changes rarely concern the guest (plan
# limits, error codes, the product catalogue). The host-to-guest JSON in
# pkg/api is additive-only (docs/release-compatibility.md), which snapshots
# carrying older guest-inits already rely on. A change anywhere else in the
# closure (guest/, every *proto and *wire package, dbguest, datarepair,
# workloadidentity, a package new to the closure) ships with its release:
# nearly every guest change lands with host code that depends on it.
holdable_dirs=(pkg/api pkg/api/canary pkg/productcap)

usage() {
  echo "usage: reuse-release-guest-init.sh --init PATH --git-sha SHA [--provenance-out PATH]" >&2
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
    --provenance-out)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      provenance_out=$2
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
[[ "$hold_hours" =~ ^[0-9]+$ ]] || { echo "reuse-release-guest-init: GUEST_INIT_HOLD_HOURS must be a whole number" >&2; exit 2; }

commit_time() { git -C "$repo_root" show -s --format=%ct "$1"; }

# write_provenance SOURCE_SHA records the shipped binary and where its code
# came from. Without --provenance-out it does nothing.
write_provenance() {
  [[ -n "$provenance_out" ]] || return 0
  local source=$1
  jq -n \
    --arg digest "sha256:$(sha256sum "$init_file" | awk '{print $1}')" \
    --arg source "$source" \
    --argjson time "$(commit_time "$source")" \
    '{format_version: 1, digest: $digest, source_sha: $source, source_time: $time}' > "$provenance_out"
}

holdable() {
  local dir
  for dir in "${holdable_dirs[@]}"; do
    [[ "$1" != "$dir" ]] || return 0
  done
  return 1
}

# guest_init_closure lists the repository directories of the packages
# guest-init links, as the release builds it. GUEST_INIT_CLOSURE overrides it
# (tests).
guest_init_closure() {
  if [[ -n "${GUEST_INIT_CLOSURE+set}" ]]; then
    printf '%s\n' "$GUEST_INIT_CLOSURE"
    return
  fi
  local module
  module=$(cd "$repo_root" && go list -m) || return 1
  (cd "$repo_root" && GOOS=linux GOARCH=amd64 go list -deps -f '{{.ImportPath}}' ./guest/init) |
    sed -n "s|^$module/||p"
}

# ship_reason SOURCE_SHA prints why a changed guest-init must ship now, or
# nothing when it may be held behind SOURCE_SHA's code.
ship_reason() {
  local source=$1
  if [[ "$hold_hours" == 0 ]]; then
    echo "holding is off"
    return
  fi
  if ! git -C "$repo_root" merge-base --is-ancestor "$source" "$git_sha" 2>/dev/null; then
    echo "held code $source is not an ancestor of this release"
    return
  fi
  local age=$(( $(date +%s) - $(commit_time "$source") ))
  if (( age >= hold_hours * 3600 )); then
    echo "held code is $(( age / 3600 )) h old (limit ${hold_hours} h)"
    return
  fi
  local closure changed
  closure=$(guest_init_closure) || {
    echo "guest-init's package closure could not be listed"
    return
  }
  changed=$(git -C "$repo_root" diff --name-only "$source" "$git_sha" | while IFS= read -r file; do
    [[ "$file" != *_test.go ]] || continue
    dir=$(dirname "$file")
    grep -Fxq "$dir" <<< "$closure" || continue
    holdable "$dir" || echo "$file"
  done | awk 'NR <= 5')
  if [[ -n "$changed" ]]; then
    echo "guest code changed outside the held tables: $(tr '\n' ' ' <<< "$changed")"
    return
  fi
  if git -C "$repo_root" log --format=%B "$source..$git_sha" | grep -Eiq '^Guest-Init-Ship:[[:space:]]*now[[:space:]]*$'; then
    echo "a commit asks for it (Guest-Init-Ship: now)"
    return
  fi
}
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
  write_provenance "$git_sha"
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
  write_provenance "$git_sha"
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

# Where the prior guest-init's code came from: the prior release's
# guest-init.json when it describes these exact verified bytes, else the prior
# release's own commit (a release from before the train). The file only moves
# when a changed guest-init ships; it never decides which bytes ship.
prior_source=$prior_release_sha
if gh release download "$previous_tag" --repo "$GITHUB_REPOSITORY" \
  --pattern guest-init.json --dir "$work_root" >/dev/null 2>&1; then
  recorded=$(jq -r --arg d "$expected_digest" \
    'select(.digest == $d) | .source_sha // empty' "$work_root/guest-init.json" 2>/dev/null || true)
  if [[ "$recorded" =~ ^[0-9a-f]{40}$ ]] && git -C "$repo_root" cat-file -e "$recorded^{commit}" 2>/dev/null; then
    prior_source=$recorded
  fi
fi

# compare exits 1 for different code and 2 for a file it cannot read.
verdict=$(python3 -I "$repo_root/scripts/ci/guest_init_reuse.py" compare \
  --head "$init_file" --prior "$work_root/init") && status=0 || status=$?
case "$status" in
  0) ;;
  1)
    reason=$(ship_reason "$prior_source")
    if [[ -n "$reason" ]]; then
      echo "guest-init code differs from $previous_tag's ($actual_digest): shipping the new binary ($reason)"
      write_provenance "$git_sha"
      exit 3
    fi
    verdict="held: code changed, held behind $prior_source for up to ${hold_hours} h"
    ;;
  *)
    echo "reuse-release-guest-init: comparing guest-init failed" >&2
    exit 1
    ;;
esac

install -m 0755 "$work_root/init" "$init_file"
write_provenance "$prior_source"
echo "reused guest-init from $previous_tag ($actual_digest, $verdict)"
