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
  runner-node22|runner-node24)
    fixture_name=handler.mjs
    fixture_body='console.log("runtime-smoke-ok");'
    runtime_command=(/usr/local/bin/node "/tmp/gregale-runtime-smoke/${fixture_name}")
    ;;
  runner-python312|runner-python313)
    fixture_name=handler.py
    fixture_body='print("runtime-smoke-ok")'
    runtime_command=(/usr/local/bin/python3 "/tmp/gregale-runtime-smoke/${fixture_name}")
    ;;
  runner-go124|runner-go124-alpine)
    fixture_name=main.go
    fixture_body=$'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("runtime-smoke-ok") }'
    runtime_command=(/tmp/gregale-runtime-smoke/handler)
    ;;
  *)
    echo "unsupported runtime image ${runtime_image}" >&2
    exit 2
    ;;
esac

fixture_dir=$(mktemp -d)
guest_init=$(mktemp)
rootfs_dir=$(mktemp -d)
rootfs_tar=$(mktemp)
ext4_image=$(mktemp)
cleanup() {
  rm -rf "${fixture_dir}" "${rootfs_dir}"
  rm -f "${guest_init}" "${rootfs_tar}" "${ext4_image}"
}
trap cleanup EXIT

printf '%s\n' "${fixture_body}" >"${fixture_dir}/${fixture_name}"

if [[ "${runtime_image}" == runner-go124 || "${runtime_image}" == runner-go124-alpine ]]; then
  GO111MODULE=off GOOS=linux GOARCH="${expected_arch}" CGO_ENABLED=0 \
    go build -trimpath -o "${fixture_dir}/handler" "${fixture_dir}/${fixture_name}"
fi

# This is a real runtime start, not just a tar listing: the image boots a
# container and executes a minimal program through its production interpreter
# or compiled Go handler. The shared static binary proves both Go runtime
# variants can execute the default Railpack output.
output=$(docker run --rm --platform "${expected_platform}" \
  --entrypoint "${runtime_command[0]}" \
  -e GO111MODULE=off -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache \
  -v "${fixture_dir}:/tmp/gregale-runtime-smoke:ro" \
  "${image_ref}" "${runtime_command[@]:1}")
if [[ "${output}" != *runtime-smoke-ok* ]]; then
  echo "::error::${image_ref} runtime smoke returned unexpected output: ${output}" >&2
  exit 1
fi

# The default Go runtime promises glibc compatibility for customers that turn
# CGO on. Build and execute a dynamically linked Go binary on the native CI
# host so a base-image switch cannot silently preserve the static smoke while
# breaking SQLite, DNS, or other CGO workloads. The runtime image is currently
# linux/amd64-only; non-native developer machines keep the portable checks
# above and CI supplies this architecture-specific proof.
if [[ "${runtime_image}" == runner-go124 ]]; then
  host_platform="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
  if [[ "${host_platform}" == "${expected_platform}" ]]; then
    if ! command -v gcc >/dev/null 2>&1; then
      echo "::error::gcc is required for the runner-go124 CGO smoke" >&2
      exit 1
    fi
    cgo_dir="${fixture_dir}/cgo"
    mkdir -p "${cgo_dir}"
    cat >"${cgo_dir}/main.go" <<'EOF'
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func main() {
	value := C.CString("runtime-cgo-ok")
	defer C.free(unsafe.Pointer(value))
	fmt.Println(C.GoString(value))
}
EOF
    GO111MODULE=off CGO_ENABLED=1 \
      go build -trimpath -o "${cgo_dir}/handler" "${cgo_dir}/main.go"
    if command -v file >/dev/null 2>&1 && \
        ! file "${cgo_dir}/handler" | grep -q 'dynamically linked'; then
      echo "::error::runner-go124 CGO fixture is not dynamically linked" >&2
      exit 1
    fi
    cgo_output=$(docker run --rm --platform "${expected_platform}" \
      --entrypoint /tmp/gregale-runtime-smoke/cgo/handler \
      -v "${fixture_dir}:/tmp/gregale-runtime-smoke:ro" \
      "${image_ref}")
    if [[ "${cgo_output}" != *runtime-cgo-ok* ]]; then
      echo "::error::${image_ref} CGO smoke returned unexpected output: ${cgo_output}" >&2
      exit 1
    fi
  else
    echo "SKIP: runner-go124 CGO smoke needs native ${expected_platform}; host is ${host_platform}"
  fi
fi

# Runtime OCI images intentionally do not bake in the release-specific
# guest-init. imaged injects the arch-matched binary into every staged ext4 as
# /sbin/init. Build the same Linux artifact used by production and assemble a
# temporary boot rootfs from the image export, then verify the boot path and
# executable architecture before the ext4 conversion.
if ! command -v go >/dev/null 2>&1; then
  echo "::error::Go is required to build guest-init for staged-rootfs smoke" >&2
  exit 1
fi
GOOS=linux GOARCH="${expected_arch}" CGO_ENABLED=0 \
go build -trimpath -o "${guest_init}" ./guest/init
docker create --platform "${expected_platform}" --entrypoint /bin/sh "${image_ref}" -c true >"${fixture_dir}/container-id"
container_id=$(<"${fixture_dir}/container-id")
trap 'docker rm -f "${container_id}" >/dev/null 2>&1 || true; cleanup' EXIT
docker export "${container_id}" -o "${rootfs_tar}"
# Some apko-built images carry placeholder character devices in their OCI
# rootfs. The guest mounts devtmpfs over /dev at boot, so those archive entries
# are neither used nor safe for an unprivileged CI tar process to materialise.
# Keep the directory and omit its contents from the staged-rootfs fixture.
tar --exclude='./dev/*' --exclude='dev/*' -xf "${rootfs_tar}" -C "${rootfs_dir}"
# Debian-derived images commonly expose /sbin as a merged-/usr symlink. The
# production injector replaces that link so /sbin/init is a real boot inode;
# mirror that behavior before assembling the test ext4.
if [[ -L "${rootfs_dir}/sbin" ]]; then
  rm "${rootfs_dir}/sbin"
fi
mkdir -p "${rootfs_dir}/sbin"
install -D -m 0755 "${guest_init}" "${rootfs_dir}/sbin/init"
test -x "${rootfs_dir}/sbin/init"
if command -v file >/dev/null 2>&1; then
  file_output=$(file "${rootfs_dir}/sbin/init")
  if [[ "${expected_arch}" == amd64 && "${file_output}" != *"x86-64"* ]]; then
    echo "::error::staged /sbin/init has unexpected architecture: ${file_output}" >&2
    exit 1
  fi
fi

if ! command -v mkfs.ext4 >/dev/null 2>&1 || ! command -v debugfs >/dev/null 2>&1; then
  echo "::error::e2fsprogs is required for staged ext4 smoke" >&2
  exit 1
fi
rootfs_mb=$(du -sm "${rootfs_dir}" | awk '{print $1}')
# Leave room for ext4 metadata and the boot inode without assuming a fixed
# runtime image size; the Go bases are substantially larger than Node/Python.
ext4_mb=$((rootfs_mb + rootfs_mb / 4 + 64))
truncate -s "${ext4_mb}M" "${ext4_image}"
mkfs.ext4 -q -F -O '^has_journal' -d "${rootfs_dir}" "${ext4_image}" >/dev/null
if ! debugfs -R 'stat /sbin/init' "${ext4_image}" 2>&1 | grep -q 'Inode:'; then
  echo "::error::staged ext4 does not contain /sbin/init" >&2
  exit 1
fi

echo "OK: ${image_ref} passed ${runtime_image} build/boot smoke with ${expected_platform} and staged /sbin/init"
