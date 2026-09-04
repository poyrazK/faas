#!/usr/bin/env bash
# Static + behavioral test for the release manifest materializer.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SCRIPT="$REPO_ROOT/scripts/materialize-release-manifest.sh"
TEMPLATE="$REPO_ROOT/deploy/manifest/production/gcp-live.template.yaml"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/gregale-manifest-test.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT

SHA=0123456789abcdef0123456789abcdef01234567
OUT_A="$TMP_DIR/production-manifest.yaml"
OUT_B="$TMP_DIR/production-manifest-second.yaml"
OUT_OVERRIDE="$TMP_DIR/production-manifest-override.yaml"
RUNTIME_BASES_ENV="$TMP_DIR/runtime-bases.env"
BUILDER_DIGEST=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
KERNEL_DIGEST=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

cat > "$RUNTIME_BASES_ENV" <<'EOF'
FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/poyrazk/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000
FAAS_DEPLOY_BASE_REF_NODE22=ghcr.io/poyrazk/runner-node22@sha256:1111111111111111111111111111111111111111111111111111111111111111
FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/poyrazk/runner-python312@sha256:2222222222222222222222222222222222222222222222222222222222222222
FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/poyrazk/runner-go124@sha256:3333333333333333333333333333333333333333333333333333333333333333
FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/poyrazk/runner-go124-alpine@sha256:4444444444444444444444444444444444444444444444444444444444444444
FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/poyrazk/runner-node24@sha256:5555555555555555555555555555555555555555555555555555555555555555
FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/poyrazk/runner-python313@sha256:6666666666666666666666666666666666666666666666666666666666666666
EOF

bash "$SCRIPT" --template "$TEMPLATE" --git-sha "$SHA" --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$OUT_A"
bash "$SCRIPT" --template "$TEMPLATE" --git-sha "$SHA" --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$OUT_B"
bash "$SCRIPT" --template "$TEMPLATE" --git-sha "$SHA" \
  --builder-base-digest "sha256:$BUILDER_DIGEST" \
  --kernel-digest "sha256:$KERNEL_DIGEST" \
  --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$OUT_OVERRIDE"

grep -Fqx '  id: pre-1.0-01234567' "$OUT_A"
grep -Fqx "  git_sha: $SHA" "$OUT_A"
cmp -s "$OUT_A" "$OUT_B"
if cmp -s "$TEMPLATE" "$OUT_A"; then
  echo "materializer returned the unchanged template" >&2
  exit 1
fi
grep -Fqx "  builder_base_digest: $BUILDER_DIGEST" "$OUT_OVERRIDE"
grep -Fqx "  kernel_digest: $KERNEL_DIGEST" "$OUT_OVERRIDE"
grep -Fqx '    minimal: ghcr.io/poyrazk/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000' "$OUT_A"
grep -Fqx '    node22: ghcr.io/poyrazk/runner-node22@sha256:1111111111111111111111111111111111111111111111111111111111111111' "$OUT_A"
go run "$REPO_ROOT/cmd/gregalectl" manifest validate --file "$OUT_OVERRIDE" >/dev/null

if MANIFEST_FILE="$OUT_A" \
  MANIFEST_HASH="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
  GIT_SHA="$SHA" \
  OUT_DIR="$TMP_DIR/unused" \
  "$REPO_ROOT/scripts/build-canonical-tarball.sh" >/dev/null 2>&1; then
  echo "build-canonical-tarball accepted a mismatched manifest hash" >&2
  exit 1
fi

go run "$REPO_ROOT/cmd/gregalectl" manifest validate --file "$OUT_A" >/dev/null
echo "materialize-release-manifest: test passed"
