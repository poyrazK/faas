#!/usr/bin/env bash
# Platform fixtures only. Customer builds still execute inside Firecracker.
# Run as root in the disposable Lima guest, from any checkout/worktree.
set -euo pipefail
REPO="${FAAS_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$REPO"
WORK=$(mktemp -d /tmp/faas-metal-stage.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
mkdir -p /srv/fc/base
CGO_ENABLED=0 go build -trimpath -o "$WORK/init" ./guest/init
install -m0755 "$WORK/init" /usr/local/bin/faas-guest-init

# Include the fixture recipe as well as the freshly built binary. A checkout
# change must invalidate the fixture even when the VM was already provisioned.
key=$(cat "$WORK/init" "$0" | sha256sum | cut -d' ' -f1)
base=/srv/fc/base/v6-base.ext4
layer=/srv/fc/base/v6-layer.ext4
if [[ ! -f "$base" || ! -f "$layer" || "$(cat "$base.recipe" 2>/dev/null || true)" != "$key" ]]; then
  tree="$WORK/base"
  mkdir -p "$tree"/{bin,sbin,dev,sys/fs/cgroup,proc,etc/faas,tmp,overlay,run} "$WORK/layer"/{etc/faas,tmp}
  install -m0755 "$WORK/init" "$tree/sbin/init"
  install -m0755 "$(command -v busybox)" "$tree/bin/busybox"
  for name in sh ash cat; do ln -s /bin/busybox "$tree/bin/$name"; done
  printf '%s\n' '{"entrypoint":["/bin/sh","-c","cat /proc/sys/kernel/random/uuid > /etc/faas/uuid.txt && exec /bin/busybox httpd -f -p 8080 -h /"],"port":8080}' > "$tree/etc/faas/app.json"
  # The hello app runs as the guest's unprivileged app UID.
  chmod 0777 "$tree/etc/faas"
  truncate -s 64M "$WORK/base.ext4"
  mkfs.ext4 -q -O '^has_journal' -d "$tree" -L faas-v6 -F "$WORK/base.ext4"
  truncate -s 16M "$WORK/layer.ext4"
  mkfs.ext4 -q -O '^has_journal' -d "$WORK/layer" -L faas-v6-layer -F "$WORK/layer.ext4"
  install -m0644 "$WORK/base.ext4" "$base"
  install -m0644 "$WORK/layer.ext4" "$layer"
  printf '%s\n' "$key" > "$base.recipe"
fi

# Retain the deploy/wake and function-runner fixtures from the provisioner.
# These are BusyBox acceptance aliases, not production language-runtime bases.
arch=$(go env GOARCH)
install -m0644 -o faas-vmmd -g faas "$base" /srv/fc/base/base.ext4
install -m0644 -o faas-vmmd -g faas "$base" "/srv/fc/base/base-$arch.ext4"
for runtime in node22 python312 go124 node24 python313; do
  CGO_ENABLED=0 go build -trimpath -o "$WORK/runner" "./guest/runners/$runtime"
  install -m0755 "$WORK/runner" "/usr/local/bin/faas-runner-$runtime"
  install -m0644 -o faas-vmmd -g faas "$base" "/srv/fc/base/runner-$runtime.ext4"
  install -m0644 -o faas-vmmd -g faas "$base" "/srv/fc/base/runner-$runtime-$arch.ext4"
done
chown faas-vmmd:faas "$base" "$layer"

if [[ "${FAAS_METAL_BUILD_ACCEPTANCE:-0}" != 1 ]]; then exit 0; fi
command -v docker >/dev/null
docker buildx version >/dev/null || {
  echo 'Install docker-buildx in the Lima guest before staging builder fixtures.' >&2
  exit 1
}
builder="${FAAS_BUILDER_BASE_PATH:-/srv/fc/base/runner-builder-$arch.ext4}"
# BuildKit tracks the complete Dockerfile context, including uncommitted edits.
# The exporter builds the platform base, never a customer source archive.
if ! docker buildx inspect faas-metal-fixtures >/dev/null 2>&1; then
  docker buildx create --name faas-metal-fixtures --driver docker-container >/dev/null
fi
docker buildx build --builder faas-metal-fixtures --platform "linux/$arch" \
  --progress plain --output "type=local,dest=$WORK/builder" \
  -f images/builder-base.Dockerfile .
for binary in buildkitd buildctl railpack faas-guest-init; do
  test -x "$WORK/builder/usr/local/bin/$binary"
done
# Match imaged's base preparation: PID 1 and the mountpoints must exist
# before the kernel mounts drive0 read-only.
mkdir -p "$WORK/builder"/{sbin,dev,sys/fs/cgroup,proc,tmp,overlay,run}
rm -f "$WORK/builder/sbin/init"
install -m0755 "$WORK/builder/usr/local/bin/faas-guest-init" "$WORK/builder/sbin/init"
size=$(du -sm "$WORK/builder" | awk '{print $1 + 256}')
truncate -s "${size}M" "$WORK/builder.ext4"
mkfs.ext4 -q -O '^has_journal' -d "$WORK/builder" -L faas-builder-bas -F "$WORK/builder.ext4"
install -m0644 "$WORK/builder.ext4" "$builder"
echo "Staged builder fixture: $builder ($arch)"
