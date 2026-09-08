#!/usr/bin/env bash
# Run one production-shaped fcvm cold boot on the designated native KVM host.
# The caller supplies an exact main source archive and pinned Go toolchain.
# This script owns host locking, fixture staging, service quiescing, cleanup,
# and service restoration so an interrupted run cannot leave the node dirty.

set -Eeuo pipefail

die() {
  echo "native metal smoke: $*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die "must run as root"

: "${FAAS_METAL_SOURCE_SHA:?set FAAS_METAL_SOURCE_SHA to the tested commit}"
: "${FAAS_METAL_GO:?set FAAS_METAL_GO to the pinned Go binary}"

[[ "${FAAS_METAL_SOURCE_SHA}" =~ ^[0-9a-f]{40}$ ]] ||
  die "source SHA must be 40 lowercase hex characters"
[[ -x "${FAAS_METAL_GO}" ]] || die "Go binary is not executable: ${FAAS_METAL_GO}"
[[ -c /dev/kvm ]] || die "/dev/kvm is unavailable"
[[ "$(uname -m)" == "x86_64" ]] || die "the production metal gate requires x86_64"
[[ -f /etc/faas/builder-acceptance-host ]] ||
  die "/etc/faas/builder-acceptance-host is missing; this node is not designated for disruptive acceptance tests"

for tool in busybox e2fsck file firecracker flock gcc install ip iptables jailer \
  make mkfs.ext4 nft readlink systemctl systemd-run tc truncate; do
  command -v "${tool}" >/dev/null || die "required host tool is missing: ${tool}"
done

busybox_path="$(command -v busybox)"
file "${busybox_path}" | grep -q 'statically linked' ||
  die "${busybox_path} must be statically linked for the guest fixture"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
marker_sha="$(tr -d '\n' < "${repo_root}/.faas-metal-source-sha")"
[[ "${marker_sha}" == "${FAAS_METAL_SOURCE_SHA}" ]] ||
  die "source archive marker ${marker_sha} does not match ${FAAS_METAL_SOURCE_SHA}"

kernel="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.134}"
fc_version="${FAAS_TEST_FC_VERSION:-1.7.0}"
run_id="${FAAS_METAL_RUN_ID:-manual}"
[[ "${run_id}" =~ ^[A-Za-z0-9._-]+$ ]] || die "run ID contains unsupported characters"
transfer_root="${FAAS_METAL_TRANSFER_ROOT:-}"
if [[ -n "${transfer_root}" && ! "${transfer_root}" =~ ^/var/tmp/faas-metal-smoke-[A-Za-z0-9._-]+$ ]]; then
  die "transfer root is outside the metal smoke staging namespace"
fi

stage_root="/srv/fc/acceptance/metal-${FAAS_METAL_SOURCE_SHA}-${run_id}"
base_skeleton="${stage_root}/base-skeleton"
layer_skeleton="${stage_root}/layer-skeleton"
base_path="${stage_root}/hello-base.ext4"
layer_path="${stage_root}/hello-layer.ext4"
guest_init="${base_skeleton}/sbin/init"
active_services="${stage_root}/active-services"
cache_root="/var/cache/faas-metal-smoke"
services=(faas-vmmd faas-builderd faas-imaged faas-gatewayd-internal)
base_mountpoints=(dev overlay proc run sys sys/fs/cgroup tmp)

mkdir -p /var/lock
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w "${FAAS_METAL_LOCK_TIMEOUT_SECONDS:-900}" 9 ||
  die "another native acceptance run holds the host lock"

mkdir -p "${base_skeleton}/bin" "${base_skeleton}/sbin" \
  "${base_skeleton}/etc/faas" \
  "${layer_skeleton}/upper/etc/faas" "${layer_skeleton}/upper/tmp" \
  "${cache_root}/go-build" "${cache_root}/go-mod" "${cache_root}/home"
for mountpoint in "${base_mountpoints[@]}"; do
  mkdir -p "${base_skeleton}/${mountpoint}"
done
: > "${active_services}"

cleanup() {
  local rc=$?
  local restore_failed=0
  trap - EXIT HUP INT TERM
  set +e

  if ! bash "${repo_root}/deploy/scripts/leakcheck.sh"; then
    echo "native metal smoke: final leak check failed" >&2
    [[ "${rc}" -ne 0 ]] || rc=1
  fi

  while IFS= read -r service; do
    [[ -n "${service}" ]] || continue
    if ! systemctl start "${service}"; then
      echo "native metal smoke: failed to restore ${service}" >&2
      restore_failed=1
    fi
  done < "${active_services}" 2>/dev/null || true
  if [[ "${restore_failed}" -ne 0 ]]; then
    [[ "${rc}" -ne 0 ]] || rc=1
  fi

  rm -rf "${stage_root}"
  if [[ -n "${transfer_root}" ]]; then
    systemd-run --quiet --collect --unit="faas-metal-smoke-clean-${run_id}" \
      --on-active=5m /usr/bin/find "${transfer_root}" -depth -delete >/dev/null 2>&1
  fi

  if [[ "${rc}" -eq 0 ]]; then
    echo "native metal smoke: PASS; services restored and staging removed"
  else
    echo "native metal smoke: FAIL (${rc}); services restored and staging removed" >&2
  fi
  exit "${rc}"
}
trap cleanup EXIT HUP INT TERM

firecracker_running() {
  local exe target
  for exe in /proc/[0-9]*/exe; do
    target="$(readlink "${exe}" 2>/dev/null || true)"
    if [[ "${target##*/}" == firecracker* ]]; then
      return 0
    fi
  done
  return 1
}

if firecracker_running; then
  die "Firecracker workloads are active; drain the designated acceptance node before retrying"
fi
bash "${repo_root}/deploy/scripts/leakcheck.sh"

[[ -r "${kernel}" ]] || die "kernel is unreadable: ${kernel}"
ip link show br-tenants >/dev/null 2>&1 || die "tenant bridge br-tenants is unavailable"
[[ "$(cat /proc/sys/net/ipv4/ip_forward)" == "1" ]] || die "IPv4 forwarding is disabled"

echo "native metal smoke: build exact-commit guest fixture"
export HOME="${cache_root}/home"
export GOCACHE="${cache_root}/go-build"
export GOMODCACHE="${cache_root}/go-mod"
export GOPATH="${cache_root}"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${FAAS_METAL_GO}" build \
  -trimpath -buildvcs=false -tags linux -o "${guest_init}" ./guest/init

install -m 0755 "${busybox_path}" "${base_skeleton}/bin/busybox"
for name in bin/sh bin/ash bin/cat; do
  ln -s /bin/busybox "${base_skeleton}/${name}"
done
# Production app artifacts live beneath drive1's /upper directory. The M0 app
# runs as UID 1000 and only needs to listen; platform-owned /etc/faas stays
# read-only to it after guest-init assembles the overlay.
printf '%s\n' \
  '{"entrypoint":["/bin/busybox","httpd","-f","-p","8080","-h","/"],"port":8080}' \
  > "${layer_skeleton}/upper/etc/faas/app.json"

truncate -s 64M "${base_path}"
mkfs.ext4 -q -O '^has_journal' -d "${base_skeleton}" -L faas-metal-smoke -F "${base_path}"
truncate -s 16M "${layer_path}"
mkfs.ext4 -q -O '^has_journal' -d "${layer_skeleton}" -L faas-metal-layer -F "${layer_path}"
e2fsck -fn "${base_path}"
e2fsck -fn "${layer_path}"
chmod 0644 "${base_path}" "${layer_path}"

if firecracker_running; then
  die "a Firecracker workload started while the fixture was staged; drain the node before retrying"
fi
for service in "${services[@]}"; do
  if systemctl is-active --quiet "${service}"; then
    printf '%s\n' "${service}" >> "${active_services}"
  fi
done
systemctl stop "${services[@]}"

PATH="$(dirname "${FAAS_METAL_GO}"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
export FAAS_TEST_KERNEL="${kernel}"
export FAAS_TEST_BASE_ROOTFS="${base_path}"
export FAAS_TEST_LAYER_ROOTFS="${layer_path}"
export FAAS_TEST_FC_VERSION="${fc_version}"

echo "native metal smoke: run TestMetalHelloBoot"
make GO="${FAAS_METAL_GO}" PKGS=./pkg/fcvm \
  RUN_ARGS='-run=^TestMetalHelloBoot$$ -timeout=5m -v' test-metal
