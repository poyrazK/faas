#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/gcp-oslogin-preflight.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/gcloud" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"instances describe"* ]]; then
  if [[ -n "${FAKE_INSTANCE_METADATA:-}" ]]; then
    printf '%s\n' "$FAKE_INSTANCE_METADATA"
  else
    printf '%s\n' '{"metadata":{"items":[]}}'
  fi
else
  if [[ -n "${FAKE_PROJECT_METADATA:-}" ]]; then
    printf '%s\n' "$FAKE_PROJECT_METADATA"
  else
    printf '%s\n' '{"commonInstanceMetadata":{"items":[]}}'
  fi
fi
EOF
chmod +x "$tmp/gcloud"
export PATH="$tmp:$PATH"

export FAKE_INSTANCE_METADATA='{"metadata":{"items":[{"key":"enable-oslogin","value":"TRUE"}]}}'
export FAKE_PROJECT_METADATA='{"commonInstanceMetadata":{"items":[{"key":"enable-oslogin","value":"FALSE"}]}}'
"$checker" --project test --zone test --instance node | grep -q 'source=instance'

export FAKE_INSTANCE_METADATA='{"metadata":{"items":[]}}'
export FAKE_PROJECT_METADATA='{"commonInstanceMetadata":{"items":[{"key":"enable-oslogin","value":"true"}]}}'
"$checker" --project test --zone test --instance node | grep -q 'source=project'

export FAKE_INSTANCE_METADATA='{"metadata":{"items":[{"key":"enable-oslogin","value":"FALSE"}]}}'
if "$checker" --project test --zone test --instance node >"$tmp/out" 2>"$tmp/err"; then
  echo "expected FALSE metadata to fail" >&2
  exit 1
fi
grep -q 'narrow CI role cannot write fallback SSH keys' "$tmp/err"

export FAKE_INSTANCE_METADATA='{"metadata":{"items":[]}}'
export FAKE_PROJECT_METADATA='{"commonInstanceMetadata":{"items":[]}}'
if "$checker" --project test --zone test --instance node >"$tmp/out" 2>"$tmp/err"; then
  echo "expected missing metadata to fail" >&2
  exit 1
fi
grep -q 'no effective enable-oslogin metadata' "$tmp/err"

echo "gcp-oslogin-preflight tests: OK"
