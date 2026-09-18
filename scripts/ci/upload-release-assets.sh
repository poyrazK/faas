#!/usr/bin/env bash
# Upload GitHub Release assets serially and make failed-job retries idempotent.

set -euo pipefail

repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
tag="${RELEASE_TAG:-${GITHUB_REF_NAME:-}}"

[[ -n "$tag" ]] || {
  echo "release tag is required via RELEASE_TAG or GITHUB_REF_NAME" >&2
  exit 2
}
[[ -n "${GH_TOKEN:-}" ]] || {
  echo "GH_TOKEN is required" >&2
  exit 2
}
[[ "$#" -gt 0 ]] || {
  echo "at least one release asset path is required" >&2
  exit 2
}

release_json() {
  local attempt response
  for attempt in 1 2 3; do
    if response="$(timeout --kill-after=10s 1m \
      gh api "repos/${repo}/releases/tags/${tag}")"; then
      printf '%s\n' "$response"
      return 0
    fi
    if [[ "$attempt" -lt 3 ]]; then
      echo "release lookup failed (attempt ${attempt}/3); retrying" >&2
      sleep $((attempt * 2))
    fi
  done
  echo "could not read release ${tag} after 3 attempts" >&2
  return 1
}

remote_digest() {
  local name="$1"
  release_json | jq -r --arg name "$name" \
    '.assets[] | select(.name == $name) | .digest // empty' | head -n 1
}

for path in "$@"; do
  [[ -f "$path" ]] || {
    echo "release asset does not exist: $path" >&2
    exit 1
  }

  name="$(basename "$path")"
  expected="sha256:$(sha256sum "$path" | awk '{print $1}')"
  published=false

  for attempt in 1 2 3; do
    current="$(remote_digest "$name")"
    if [[ "$current" == "$expected" ]]; then
      echo "verified existing release asset ${name} (${expected})"
      published=true
      break
    fi
    if [[ -n "$current" ]]; then
      echo "release asset ${name} already exists with unexpected digest" >&2
      echo "  expected: ${expected}" >&2
      echo "  actual:   ${current}" >&2
      echo "refusing to overwrite an immutable release asset" >&2
      exit 1
    fi

    echo "uploading ${name} (attempt ${attempt}/3)"
    # Bound a wedged upload. rc.173 logged its first error after three minutes
    # but a sibling request kept the action alive until the twenty-minute mark.
    timeout --kill-after=15s 5m \
      gh release upload "$tag" "$path" --repo "$repo" || true

    # GitHub may make the asset visible just after the upload command returns.
    # Verify the server-computed digest rather than trusting the exit status.
    for _ in 1 2 3 4 5; do
      current="$(remote_digest "$name")"
      if [[ "$current" == "$expected" ]]; then
        echo "uploaded and verified ${name} (${expected})"
        published=true
        break 2
      fi
      if [[ -n "$current" ]]; then
        echo "uploaded release asset ${name} has unexpected digest" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${current}" >&2
        exit 1
      fi
      sleep 2
    done

    if [[ "$attempt" -lt 3 ]]; then
      sleep $((attempt * 5))
    fi
  done

  [[ "$published" == true ]] || {
    echo "failed to publish verified release asset ${name} after 3 attempts" >&2
    exit 1
  }
done
