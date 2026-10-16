#!/usr/bin/env bash
# CI orchestration only; the existing guarded runner owns native/leak verdicts.
set -Eeuo pipefail
umask 077

expected_sha="${1:?expected source SHA required}"
evidence_root="${2:?new evidence directory required}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[[ "$expected_sha" =~ ^[a-f0-9]{40}$ ]] || { echo 'GitOps native CI: invalid source SHA' >&2; exit 1; }
python3 - "$evidence_root" "$repo_root" <<'PY'
from pathlib import Path
import sys
directory, repo = map(Path, sys.argv[1:])
if not directory.is_absolute() or directory.exists() or directory.is_symlink() or directory.resolve().is_relative_to(repo.resolve()):
    raise SystemExit("GitOps native CI: evidence must be a fresh absolute directory outside the checkout")
directory.mkdir(mode=0o700)
PY

# Neither setup output nor raw daemon logs belong in the Actions console.
exec 3>&1
exec >"$evidence_root/private-setup.log" 2>&1
phase=setup
reason=setup_failed
finish() {
  local status=$?
  trap - EXIT
  python3 - "$evidence_root/setup.json" "$phase" "$reason" "$status" <<'PY'
import json, sys
from pathlib import Path
Path(sys.argv[1]).write_text(json.dumps({"phase": sys.argv[2], "reason": sys.argv[3], "exit_code": int(sys.argv[4])}) + "\n")
PY
  echo "GitOps native CI: $reason (exit $status); raw evidence retained privately" >&3
  exit "$status"
}
trap finish EXIT
trap 'reason=interrupted; exit 130' INT
trap 'reason=interrupted; exit 143' TERM
die() { reason="$1"; exit 1; }

[[ "$(id -u)" == 0 ]] || die root_required
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -c /dev/kvm ]] || die unsupported_host
[[ -f /etc/faas/builder-acceptance-host ]] || die undesignated_host
config=/etc/faas/gitops-native-acceptance.env
[[ -f "$config" && ! -L "$config" ]] || die host_configuration_missing
[[ "$(stat -c '%u:%a' "$config")" == 0:600 ]] || die host_configuration_permissions
# This root-owned operator file supplies the disposable DB, kernel and three
# pinned registry refs. No DSN or registry credential enters command arguments.
# shellcheck disable=SC1090
source "$config"
export DATABASE_URL FAAS_TEST_KERNEL
export GREGALE_GITOPS_NATIVE_BUILDER_REF GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF
export GREGALE_GITOPS_NATIVE_HOST_CLASS=native
export GREGALE_GITOPS_NATIVE_REPORT_DIR="$evidence_root/private"
# sudo's root git process must read this exact runner-owned checkout, without
# modifying global Git configuration or trusting other repositories.
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0="$repo_root"
cd "$repo_root"
[[ "$(git rev-parse HEAD)" == "$expected_sha" && -z "$(git status --porcelain)" ]] || die source_mismatch
go_binary="$(command -v go)" || die pinned_go_missing
[[ "$("$go_binary" env GOVERSION)" == "$(awk '$1 == "go" {print "go" $2; exit}' go.mod)" ]] || die pinned_go_mismatch
export GO="$go_binary" GOTOOLCHAIN=local
# A caller-supplied guest-init cannot stand in for the tested commit.
export FAAS_GUEST_INIT="$evidence_root/guest-init"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_binary" build -trimpath -o "$FAAS_GUEST_INIT" ./guest/init
chmod 0700 "$FAAS_GUEST_INIT"
phase=native
reason=native_runner_failed
bash scripts/test-environment-gitops-native.sh
reason=completed
