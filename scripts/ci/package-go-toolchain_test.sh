#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

# Match the host-specific layout used by actions/setup-go. The archive must
# not leak the final "x64" directory into the compute-node path.
go_root="$test_root/hostedtoolcache/go/1.25.13/x64"
mkdir -p "$go_root/bin" "$go_root/pkg/tool/linux_amd64"
printf '#!/usr/bin/env bash\nexit 0\n' > "$go_root/bin/go"
chmod 0755 "$go_root/bin/go"
printf 'compile\n' > "$go_root/pkg/tool/linux_amd64/compile"

archive="$test_root/go.tar.gz"
bash "$repo_root/scripts/ci/package-go-toolchain.sh" "$go_root" "$archive"

destination="$test_root/tools/go"
mkdir -p "$destination"
tar -xzf "$archive" -C "$destination"

[[ -x "$destination/bin/go" ]] || {
  echo "packaged Go binary is missing or not executable" >&2
  exit 1
}
[[ -f "$destination/pkg/tool/linux_amd64/compile" ]] || {
  echo "packaged Go toolchain is incomplete" >&2
  exit 1
}
[[ ! -e "$destination/x64" ]] || {
  echo "archive leaked the host-specific GOROOT basename" >&2
  exit 1
}
