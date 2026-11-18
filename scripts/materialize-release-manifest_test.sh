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
FIRECRACKER_DIGEST=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc

cat > "$RUNTIME_BASES_ENV" <<'EOF'
FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/poyrazk/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000
FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT=ghcr.io/poyrazk/base-debian-parent@sha256:7777777777777777777777777777777777777777777777777777777777777777
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
  --firecracker-digest "sha256:$FIRECRACKER_DIGEST" \
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
grep -Fqx "  firecracker_digest: $FIRECRACKER_DIGEST" "$OUT_OVERRIDE"
# Without an override the template's upstream digest stays.
grep -Eq '^  firecracker_digest: [0-9a-f]{64}$' "$OUT_A"
if grep -Fqx "  firecracker_digest: $FIRECRACKER_DIGEST" "$OUT_A"; then
  echo "firecracker digest override leaked into the default manifest" >&2
  exit 1
fi
if bash "$SCRIPT" --template "$TEMPLATE" --git-sha "$SHA" --firecracker-digest nothex \
  --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$TMP_DIR/bad.yaml" 2>/dev/null; then
  echo "materializer accepted a malformed firecracker digest" >&2
  exit 1
fi
# Every compute daemon whose metrics are discovered by Prometheus must be
# declared in the production manifest. A missing block renders an empty TOML
# and silently falls back to loopback, which makes the discovered target down.
grep -Fqx '  imaged:' "$OUT_A"
grep -Fqx '    bind: tcp://127.0.0.1:9102' "$OUT_A"
grep -Fqx '    minimal: ghcr.io/poyrazk/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000' "$OUT_A"
grep -Fqx '    debian_parent: ghcr.io/poyrazk/base-debian-parent@sha256:7777777777777777777777777777777777777777777777777777777777777777' "$OUT_A"
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

# The canonical builder must validate with the gregalectl binary it just
# built. Stub make so the test can prove that ordering without compiling the
# complete release bundle.
FAKE_BIN="$TMP_DIR/fake-bin"
VALIDATION_MARKER="$TMP_DIR/bundled-gregalectl.args"
mkdir -p "$FAKE_BIN"
cat > "$FAKE_BIN/make" <<'FAKE_MAKE'
#!/usr/bin/env bash
set -euo pipefail
bindir=
for arg in "$@"; do
  case "$arg" in
    BINDIR=*) bindir=${arg#BINDIR=} ;;
  esac
done
[[ -n "$bindir" ]]
mkdir -p "$bindir"
cat > "$bindir/gregalectl" <<'FAKE_GREGLECTL'
#!/usr/bin/env bash
printf '%s\n' "$*" > "$VALIDATION_MARKER"
exit 42
FAKE_GREGLECTL
chmod +x "$bindir/gregalectl"
FAKE_MAKE
chmod +x "$FAKE_BIN/make"

set +e
PATH="$FAKE_BIN:$PATH" \
  VALIDATION_MARKER="$VALIDATION_MARKER" \
  MANIFEST_FILE="$OUT_A" \
  GIT_SHA="$SHA" \
  OUT_DIR="$TMP_DIR/bundled-validation" \
  "$REPO_ROOT/scripts/build-canonical-tarball.sh" >/dev/null 2>&1
validation_status=$?
set -e
if [[ "$validation_status" -ne 42 ]]; then
  echo "build-canonical-tarball did not use bundled gregalectl for validation (status=$validation_status)" >&2
  exit 1
fi
grep -Fqx "manifest validate --file $OUT_A" "$VALIDATION_MARKER"

# --- The release template pointer (deploy/manifest/production/release-template).
RESOLVER="$REPO_ROOT/scripts/release-template.sh"
POINTED=$(bash "$RESOLVER")
[[ "$(dirname "$POINTED")" == "$REPO_ROOT/deploy/manifest/production" && -f "$POINTED" ]] || {
  echo "release-template resolved outside the production directory: $POINTED" >&2
  exit 1
}
OUT_DEFAULT="$TMP_DIR/production-manifest-default.yaml"
OUT_POINTED="$TMP_DIR/production-manifest-pointed.yaml"
bash "$SCRIPT" --git-sha "$SHA" --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$OUT_DEFAULT" >/dev/null
bash "$SCRIPT" --template "$POINTED" --git-sha "$SHA" --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$OUT_POINTED" >/dev/null
cmp -s "$OUT_DEFAULT" "$OUT_POINTED" || { echo "materializer default ignored the release-template pointer" >&2; exit 1; }

POINTER_DIR="$TMP_DIR/pointer"
mkdir -p "$POINTER_DIR"
cp "$TEMPLATE" "$POINTER_DIR/next-live.template.yaml"
printf '# comment\nnext-live.template.yaml\n' >"$POINTER_DIR/release-template"
[[ "$(bash "$RESOLVER" "$POINTER_DIR/release-template")" == "$(cd "$POINTER_DIR" && pwd)/next-live.template.yaml" ]] || {
  echo "release-template did not resolve beside the pointer" >&2
  exit 1
}
for bad in '../gcp-live.template.yaml' '/etc/passwd' 'missing.template.yaml' 'notes.txt' $'a.template.yaml\nb.template.yaml' ''; do
  printf '%s\n' "$bad" >"$POINTER_DIR/release-template"
  if bash "$RESOLVER" "$POINTER_DIR/release-template" >/dev/null 2>&1; then
    echo "release-template accepted pointer content: $bad" >&2
    exit 1
  fi
done

# --- Credentials never reach a published manifest. Fake values are built
# at runtime so no credential-shaped literal sits in the repository.
GUARD="$REPO_ROOT/scripts/check-manifest-credentials.sh"
for template in "$REPO_ROOT"/deploy/manifest/production/*.template.yaml; do
  bash "$GUARD" "$template" || { echo "checked-in template carries a credential: $template" >&2; exit 1; }
done
fakes=(
  "    access_key_id: AKIA""IOSFODNN7EXAMPLE"
  "  note: gh""p_$(printf 'a%.0s' {1..36})"
  "-----BEGIN ""OPENSSH PRIVATE KEY-----"
  "  recipient: AGE-SECRET-KEY-1""$(printf 'Q%.0s' {1..58})"
  "  dsn: postgres://faas:hunter""2@db.example:5432/faas"
  "    secret_access_key: wJalrXUtnFEMI""example"
  "  password: hunter""2"
  '  "private_key": "x"'
  "  webhook: https://hooks.slack.com/services/""T0/B0/x"
  "# token: abc""def"
)
for fake in "${fakes[@]}"; do
  planted="$TMP_DIR/planted.yaml"
  { cat "$TEMPLATE"; printf '%s\n' "$fake"; } >"$planted"
  if bash "$GUARD" "$planted" 2>"$TMP_DIR/guard.err"; then
    echo "credential guard accepted: ${fake:0:24}…" >&2
    exit 1
  fi
  secret_part="${fake#*: }"
  if [[ "$secret_part" != "$fake" ]] && grep -Fq -- "$secret_part" "$TMP_DIR/guard.err"; then
    echo "credential guard echoed the credential it refused" >&2
    exit 1
  fi
done
allowed="$TMP_DIR/allowed.yaml"
{
  cat "$TEMPLATE"
  printf '%s\n' '  credentials_src: /operator/secrets/log-archive-s3.json' \
    '  token_ttl: 60s' '  secret: ""' '  password: ~' '  dsn: postgres://faas@db.example:5432/faas'
} >"$allowed"
bash "$GUARD" "$allowed" || { echo "credential guard refused credential-free lines" >&2; exit 1; }
{ cat "$TEMPLATE"; printf '%s\n' "    access_key_id: AKIA""IOSFODNN7EXAMPLE"; } >"$TMP_DIR/leaky.template.yaml"
if bash "$SCRIPT" --template "$TMP_DIR/leaky.template.yaml" --git-sha "$SHA" \
  --runtime-bases-env "$RUNTIME_BASES_ENV" --output "$TMP_DIR/leaky.yaml" 2>/dev/null; then
  echo "materializer published a template that carries a credential" >&2
  exit 1
fi
[[ ! -e "$TMP_DIR/leaky.yaml" ]] || { echo "materializer wrote a manifest carrying a credential" >&2; exit 1; }


go run "$REPO_ROOT/cmd/gregalectl" manifest validate --file "$OUT_A" >/dev/null
echo "materialize-release-manifest: test passed"
