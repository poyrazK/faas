#!/usr/bin/env bash
# Materialize the immutable release identity into a production manifest.
#
# The checked-in production manifest is a topology template. Release CI
# writes the tag commit into its release tuple and publishes the resulting
# bytes with the signed daemon bundle. Keeping this projection out of git
# avoids the impossible self-reference of a commit containing its own SHA.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEMPLATE="$REPO_ROOT/deploy/manifest/production/gcp-live.template.yaml"
GIT_SHA=""
OUTPUT=""
BUILDER_BASE_DIGEST=""
RUNTIME_BASES_ENV=""
KERNEL_DIGEST=""

usage() {
  cat >&2 <<'USAGE'
usage: materialize-release-manifest.sh --git-sha SHA --output PATH --runtime-bases-env PATH [--template PATH] [--builder-base-digest DIGEST] [--kernel-digest DIGEST]
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --template)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      TEMPLATE=$2
      shift 2
      ;;
    --git-sha)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      GIT_SHA=$2
      shift 2
      ;;
    --output)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      OUTPUT=$2
      shift 2
      ;;
    --builder-base-digest)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      BUILDER_BASE_DIGEST=$2
      shift 2
      ;;
    --runtime-bases-env)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      RUNTIME_BASES_ENV=$2
      shift 2
      ;;
    --kernel-digest)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      KERNEL_DIGEST=$2
      shift 2
      ;;
    -h|--help)
      usage >&2
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [[ ! "$GIT_SHA" =~ ^[0-9a-f]{40}$ ]]; then
  echo "materialize-release-manifest: --git-sha must be a 40-character lowercase SHA" >&2
  exit 2
fi
if [[ -z "$OUTPUT" ]]; then
  echo "materialize-release-manifest: --output is required" >&2
  exit 2
fi
if [[ ! -f "$TEMPLATE" ]]; then
  echo "materialize-release-manifest: template not found: $TEMPLATE" >&2
  exit 1
fi
if [[ -n "$BUILDER_BASE_DIGEST" && ! "$BUILDER_BASE_DIGEST" =~ ^(sha256:)?[0-9a-f]{64}$ ]]; then
  echo "materialize-release-manifest: --builder-base-digest must be a sha256 digest" >&2
  exit 2
fi
BUILDER_BASE_DIGEST="${BUILDER_BASE_DIGEST#sha256:}"
if [[ -n "$KERNEL_DIGEST" && ! "$KERNEL_DIGEST" =~ ^(sha256:)?[0-9a-f]{64}$ ]]; then
  echo "materialize-release-manifest: --kernel-digest must be a sha256 digest" >&2
  exit 2
fi
KERNEL_DIGEST="${KERNEL_DIGEST#sha256:}"
if [[ -z "$RUNTIME_BASES_ENV" || ! -f "$RUNTIME_BASES_ENV" ]]; then
  echo "materialize-release-manifest: --runtime-bases-env must name a generated runtime contract" >&2
  exit 2
fi

runtime_ref() {
  local key=$1
  awk -F= -v key="$key" '$1 == key { print substr($0, index($0, "=") + 1); found = 1; exit } END { if (!found) exit 1 }' "$RUNTIME_BASES_ENV"
}

MINIMAL_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_MINIMAL)
NODE22_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_NODE22)
PYTHON312_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_PYTHON312)
GO124_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_GO124)
GO124_ALPINE_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_GO124_ALPINE)
NODE24_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_NODE24)
PYTHON313_REF=$(runtime_ref FAAS_DEPLOY_BASE_REF_PYTHON313)
for runtime_ref_value in "$MINIMAL_REF" "$NODE22_REF" "$PYTHON312_REF" "$GO124_REF" "$GO124_ALPINE_REF" "$NODE24_REF" "$PYTHON313_REF"; do
  if [[ ! "$runtime_ref_value" =~ ^[^#[:space:]=]+@sha256:[0-9a-f]{64}$ ]]; then
    echo "materialize-release-manifest: runtime contract contains an invalid digest-pinned OCI reference" >&2
    exit 2
  fi
done

mkdir -p "$(dirname "$OUTPUT")"
tmp=$(mktemp "${OUTPUT}.tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT

release_id="pre-1.0-${GIT_SHA:0:8}"
awk -v release_id="$release_id" -v release_sha="$GIT_SHA" \
  -v builder_base_digest="$BUILDER_BASE_DIGEST" \
  -v kernel_digest="$KERNEL_DIGEST" \
  -v minimal_ref="$MINIMAL_REF" \
  -v node22_ref="$NODE22_REF" \
  -v python312_ref="$PYTHON312_REF" \
  -v go124_ref="$GO124_REF" \
  -v go124_alpine_ref="$GO124_ALPINE_REF" \
  -v node24_ref="$NODE24_REF" \
  -v python313_ref="$PYTHON313_REF" '
  function fail(message) {
    print "materialize-release-manifest: " message > "/dev/stderr"
    exit 1
  }
  BEGIN {
    in_release = 0
    release_sections = 0
    release_ids = 0
    release_shas = 0
    builder_base_digests = 0
    runtime_base_refs = 0
  }
  /^release:[[:space:]]*$/ {
    if (in_release || release_sections != 0) {
      fail("template must contain exactly one release section")
    }
    in_release = 1
    release_sections++
    print
    next
  }
  in_release && /^  id:[[:space:]]/ {
    print "  id: " release_id
    release_ids++
    next
  }
  in_release && /^  git_sha:[[:space:]]/ {
    print "  git_sha: " release_sha
    release_shas++
    next
  }
  in_release && builder_base_digest != "" && /^  builder_base_digest:[[:space:]]/ {
    print "  builder_base_digest: " builder_base_digest
    builder_base_digests++
    next
  }
  in_release && kernel_digest != "" && /^  kernel_digest:[[:space:]]/ {
    print "  kernel_digest: " kernel_digest
    kernel_digests++
    next
  }
  in_release && /^    minimal:[[:space:]]/ {
    print "    minimal: " minimal_ref
    runtime_base_refs++
    next
  }
  in_release && /^    node22:[[:space:]]/ {
    print "    node22: " node22_ref
    runtime_base_refs++
    next
  }
  in_release && /^    python312:[[:space:]]/ {
    print "    python312: " python312_ref
    runtime_base_refs++
    next
  }
  in_release && /^    go124:[[:space:]]/ {
    print "    go124: " go124_ref
    runtime_base_refs++
    next
  }
  in_release && /^    go124_alpine:[[:space:]]/ {
    print "    go124_alpine: " go124_alpine_ref
    runtime_base_refs++
    next
  }
  in_release && /^    node24:[[:space:]]/ {
    print "    node24: " node24_ref
    runtime_base_refs++
    next
  }
  in_release && /^    python313:[[:space:]]/ {
    print "    python313: " python313_ref
    runtime_base_refs++
    next
  }
  in_release && /^[^[:space:]#]/ {
    in_release = 0
  }
  { print }
  END {
    if (release_sections != 1 || release_ids != 1 || release_shas != 1) {
      fail("template must contain one indented release.id and release.git_sha")
    }
    if (builder_base_digest != "" && builder_base_digests != 1) {
      fail("template must contain one indented release.builder_base_digest when an override is supplied")
    }
    if (kernel_digest != "" && kernel_digests != 1) {
      fail("template must contain one indented release.kernel_digest when an override is supplied")
    }
    if (runtime_base_refs != 7) {
      fail("template must contain the seven runtime_base_refs entries")
    }
  }
' "$TEMPLATE" > "$tmp"

mv "$tmp" "$OUTPUT"
trap - EXIT
chmod 0644 "$OUTPUT"
printf 'materialized %s (release=%s, manifest hash computed by caller)\n' "$OUTPUT" "$release_id"
