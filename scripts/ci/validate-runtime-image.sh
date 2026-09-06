#!/usr/bin/env bash
set -euo pipefail

image_ref=${1:?image reference is required}
runtime_image=${2:?runtime image name is required}
expected_platform=${3:-linux/amd64}

expected_os=${expected_platform%%/*}
expected_arch=${expected_platform##*/}
if [[ "${expected_os}" == "${expected_platform}" || -z "${expected_os}" || -z "${expected_arch}" ]]; then
  echo "expected platform must be OS/ARCH, got ${expected_platform}" >&2
  exit 2
fi

case "${runtime_image}" in
  base-debian-parent)
    # Debian bookworm uses merged-/usr; /bin is a symlink and docker export
    # records the target path as usr/bin/sh.
    required=(etc/passwd usr/bin/sh)
    ;;
  base-minimal)
    required=(etc/passwd bin/busybox bin/sh)
    ;;
  runner-node22|runner-node24)
    required=(etc/passwd usr/local/bin/node)
    ;;
  runner-python312|runner-python313)
    required=(etc/passwd usr/local/bin/python3)
    ;;
  runner-go124)
    # Go customer artifacts are compiled in the builder and executed
    # directly; the compiler is deliberately absent from the runtime base.
    required=(etc/passwd usr/lib/libc.so.6 usr/lib/ld-linux-x86-64.so.2 lib64)
    ;;
  runner-go124-alpine)
    required=(etc/passwd lib/ld-musl-x86_64.so.1)
    ;;
  *)
    echo "unsupported runtime image ${runtime_image}" >&2
    exit 2
    ;;
esac

actual_platform=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "${image_ref}")
if [[ "${actual_platform}" != "${expected_platform}" ]]; then
  echo "::error::${image_ref} has platform ${actual_platform}, want ${expected_platform}" >&2
  exit 1
fi

container_id=$(docker create --entrypoint /bin/sh "${image_ref}" -c true)
rootfs_tar=""
rootfs_listing=""
cleanup() {
  docker rm -f "${container_id}" >/dev/null 2>&1 || true
  if [[ -n "${rootfs_tar}" ]]; then
    rm -f "${rootfs_tar}"
  fi
  if [[ -n "${rootfs_listing}" ]]; then
    rm -f "${rootfs_listing}"
  fi
}
trap cleanup EXIT

rootfs_tar=$(mktemp)
docker export "${container_id}" -o "${rootfs_tar}"
rootfs_listing=$(mktemp)
# Materialise the listing before matching. Piping tar into grep -q under
# pipefail makes tar receive SIGPIPE as soon as grep finds a match, which
# falsely turns a present path into a missing-path failure.
tar -tf "${rootfs_tar}" >"${rootfs_listing}"
for path in "${required[@]}"; do
  if ! grep -Eq "^(\./)?${path}(/|$)" "${rootfs_listing}"; then
    echo "::error::${image_ref} is missing /${path}" >&2
    exit 1
  fi
done

# Go handlers are compiled in the builder VM. A compiler in either runtime
# image expands the trusted guest surface and makes old toolchain CVEs part of
# every function base even though no request needs `go build`.
if [[ "${runtime_image}" == runner-go124 || "${runtime_image}" == runner-go124-alpine ]]; then
  if grep -Eq '^(\./)?usr/local/go(/|$)' "${rootfs_listing}"; then
    echo "::error::${image_ref} contains the Go toolchain; compile in builderd and keep it out of the runtime base" >&2
    exit 1
  fi
fi

# The OCI image is also booted as a container here. Runtime bases deliberately
# receive the Firecracker PID 1 binary during imaged's ext4 staging, so this
# checks the shell contract at the OCI boundary and the staged /sbin/init
# contract is checked by the runtime smoke script below.
docker run --rm --platform "${expected_platform}" --entrypoint /bin/sh "${image_ref}" \
  -ceu 'test -x /bin/sh && test -r /etc/passwd'
# Railpack emits /bin/bash -c entrypoints, including for its shell provider.
# Execute Bash to check its loader and shared libraries as well as its path.
docker run --rm --platform "${expected_platform}" --entrypoint /bin/bash "${image_ref}" \
  -ceu 'test -n "${BASH_VERSION}"'
echo "OK: ${image_ref} contains the ${runtime_image} rootfs contract"
