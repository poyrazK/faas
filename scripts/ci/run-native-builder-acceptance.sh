#!/usr/bin/env bash
# Run the release-shaped builder acceptance suite on a designated native KVM
# host. The caller supplies an exact main-commit image and a matching source
# checkout. This script owns host locking, service quiescing, fixture staging,
# cleanup, and service restoration so an interrupted CI run cannot leave the
# node in a test state.

set -Eeuo pipefail

die() {
  echo "native builder acceptance: $*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die "must run as root"

: "${FAAS_ACCEPTANCE_SOURCE_SHA:?set FAAS_ACCEPTANCE_SOURCE_SHA to the tested commit}"
: "${FAAS_ACCEPTANCE_IMAGE:?set FAAS_ACCEPTANCE_IMAGE to the immutable builder image tag}"
: "${FAAS_ACCEPTANCE_GO:?set FAAS_ACCEPTANCE_GO to the pinned Go binary}"
: "${FAAS_ACCEPTANCE_CRANE:?set FAAS_ACCEPTANCE_CRANE to the pinned crane binary}"
: "${FAAS_ACCEPTANCE_NODE_NAME:?set FAAS_ACCEPTANCE_NODE_NAME to the compute_nodes identity}"

[[ "${FAAS_ACCEPTANCE_SOURCE_SHA}" =~ ^[0-9a-f]{40}$ ]] || die "source SHA must be 40 lowercase hex characters"
[[ "${FAAS_ACCEPTANCE_IMAGE}" == "ghcr.io/poyrazk/builder-base:sha-${FAAS_ACCEPTANCE_SOURCE_SHA}" ]] ||
  die "builder image must be the exact sha-<source commit> tag"
[[ -x "${FAAS_ACCEPTANCE_GO}" ]] || die "Go binary is not executable: ${FAAS_ACCEPTANCE_GO}"
[[ -x "${FAAS_ACCEPTANCE_CRANE}" ]] || die "crane binary is not executable: ${FAAS_ACCEPTANCE_CRANE}"
[[ -c /dev/kvm ]] || die "/dev/kvm is unavailable"
[[ "$(uname -m)" == "x86_64" ]] || die "the production acceptance gate requires x86_64"
[[ -f /etc/faas/builder-acceptance-host ]] ||
  die "/etc/faas/builder-acceptance-host is missing; this node is not designated for disruptive acceptance tests"

for tool in busybox debugfs e2fsck find flock jq mkfs.ext4 readlink seq sha256sum \
  systemctl systemd-run tar truncate; do
  command -v "${tool}" >/dev/null || die "required host tool is missing: ${tool}"
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
marker_sha="$(tr -d '\n' < "${repo_root}/.faas-acceptance-source-sha")"
[[ "${marker_sha}" == "${FAAS_ACCEPTANCE_SOURCE_SHA}" ]] ||
  die "source archive marker ${marker_sha} does not match ${FAAS_ACCEPTANCE_SOURCE_SHA}"

kernel="${FAAS_TEST_KERNEL:-/srv/fc/base/vmlinux-6.1.134}"
runtime_base="${FAAS_TEST_BASE_ROOTFS:-/srv/fc/base/runner-base-debian-parent-amd64.ext4}"
fc_version="${FAAS_TEST_FC_VERSION:-1.7.0}"
run_id="${FAAS_ACCEPTANCE_RUN_ID:-manual}"
[[ "${run_id}" =~ ^[A-Za-z0-9._-]+$ ]] || die "run ID contains unsupported characters"
transfer_root="${FAAS_ACCEPTANCE_TRANSFER_ROOT:-}"
if [[ -n "${transfer_root}" && ! "${transfer_root}" =~ ^/var/tmp/faas-builder-acceptance-[A-Za-z0-9._-]+$ ]]; then
  die "transfer root is outside the acceptance staging namespace"
fi

stage_root="/srv/fc/acceptance/${FAAS_ACCEPTANCE_SOURCE_SHA}-${run_id}"
cache_root="/var/cache/faas-builder-acceptance"
rootfs_dir="${stage_root}/rootfs"
builder_path="${stage_root}/base/runner-builder-amd64.ext4"
guest_init="${stage_root}/faas-guest-init"
gregalectl_path="${stage_root}/gregalectl"
rootfs_tar="${stage_root}/builder-rootfs.tar"
active_services="${stage_root}/active-services"
maintenance_state="${stage_root}/maintenance.state"
# Start vmmd last during cleanup. Its startup registration can mark the row
# active, so the other customer-serving daemons must already be ready first.
services=(faas-builderd faas-imaged faas-gatewayd-internal faas-vmmd)

mkdir -p /var/lock
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w "${FAAS_ACCEPTANCE_LOCK_TIMEOUT_SECONDS:-900}" 9 ||
  die "another native builder acceptance run holds the host lock"

mkdir -p "${stage_root}/base" "${rootfs_dir}" "${cache_root}/go-build" \
  "${cache_root}/go-mod" "${cache_root}/home"
: > "${active_services}"

cleanup() {
  local rc=$?
  local restore_failed=0
  local services_ready
  trap - EXIT HUP INT TERM
  set +e

  if ! bash "${repo_root}/deploy/scripts/leakcheck.sh"; then
    echo "native builder acceptance: final leak check failed" >&2
    [[ "${rc}" -ne 0 ]] || rc=1
  fi

  while IFS= read -r service; do
    [[ -n "${service}" ]] || continue
    if ! systemctl start "${service}"; then
      echo "native builder acceptance: failed to restore ${service}" >&2
      restore_failed=1
    fi
  done < "${active_services}" 2>/dev/null || true

  if [[ -r "${maintenance_state}" ]]; then
    services_ready=true
    if [[ "${restore_failed}" -ne 0 ]]; then
      services_ready=false
    fi
    if ! FAAS_ACCEPTANCE_GREGALECTL="${gregalectl_path}" \
      bash "${repo_root}/scripts/ci/native-builder-node-maintenance.sh" \
        restore "${maintenance_state}" "${services_ready}"; then
      echo "native builder acceptance: failed to restore compute-node placement state" >&2
      restore_failed=1
    fi
  fi
  if [[ "${restore_failed}" -ne 0 ]]; then
    [[ "${rc}" -ne 0 ]] || rc=1
  fi
  rm -rf "${stage_root}"
  if [[ -n "${transfer_root}" ]]; then
    systemd-run --quiet --collect --unit="faas-builder-acceptance-clean-${run_id}" \
      --on-active=5m /usr/bin/find "${transfer_root}" -depth -delete >/dev/null 2>&1
  fi

  if [[ "${rc}" -eq 0 ]]; then
    echo "native builder acceptance: PASS; services restored and staging removed"
  else
    echo "native builder acceptance: FAIL (${rc}); services restored and staging removed" >&2
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
[[ -r "${runtime_base}" ]] || die "runtime base is unreadable: ${runtime_base}"

echo "native builder acceptance: resolve ${FAAS_ACCEPTANCE_IMAGE} for linux/amd64"
builder_digest=""
for attempt in $(seq 1 20); do
  if builder_digest="$("${FAAS_ACCEPTANCE_CRANE}" digest --platform linux/amd64 "${FAAS_ACCEPTANCE_IMAGE}" 2>/dev/null)"; then
    break
  fi
  builder_digest=""
  if [[ "${attempt}" -eq 20 ]]; then
    die "builder image was not available after 10 minutes"
  fi
  echo "native builder acceptance: image unavailable (attempt ${attempt}/20); retrying in 30s"
  sleep 30
done
[[ "${builder_digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || die "crane returned an invalid child digest"

config_json="$("${FAAS_ACCEPTANCE_CRANE}" config --platform linux/amd64 "${FAAS_ACCEPTANCE_IMAGE}")"
revision="$(jq -r '.config.Labels["org.opencontainers.image.revision"] // empty' <<<"${config_json}")"
[[ "${revision}" == "${FAAS_ACCEPTANCE_SOURCE_SHA}" ]] ||
  die "builder image revision ${revision:-<missing>} does not match source commit"

"${FAAS_ACCEPTANCE_CRANE}" export "${FAAS_ACCEPTANCE_IMAGE}@${builder_digest}" "${rootfs_tar}"
tar --extract --file "${rootfs_tar}" --directory "${rootfs_dir}" --numeric-owner --same-owner
rm -f "${rootfs_tar}"

image_guest_init="${rootfs_dir}/usr/local/bin/faas-guest-init"
[[ -x "${image_guest_init}" ]] || die "builder image does not contain executable faas-guest-init"
guest_init_sha="$(sha256sum "${image_guest_init}" | awk '{print $1}')"
install -D -m 0755 "${image_guest_init}" "${rootfs_dir}/sbin/init"
mkdir -p "${rootfs_dir}/dev" "${rootfs_dir}/overlay" "${rootfs_dir}/proc" \
  "${rootfs_dir}/run" "${rootfs_dir}/sys/fs/cgroup" "${rootfs_dir}/tmp"
chmod 1777 "${rootfs_dir}/tmp"

rootfs_bytes="$(du -sb "${rootfs_dir}" | awk '{print $1}')"
image_bytes=$((rootfs_bytes + rootfs_bytes / 4 + 256 * 1024 * 1024))
minimum_bytes=$((1024 * 1024 * 1024))
(( image_bytes >= minimum_bytes )) || image_bytes=${minimum_bytes}
extent=$((64 * 1024 * 1024))
image_bytes=$(((image_bytes + extent - 1) / extent * extent))

fixture_tmp="${builder_path}.tmp"
truncate -s "${image_bytes}" "${fixture_tmp}"
mkfs.ext4 -q -F -L faas-builder -d "${rootfs_dir}" "${fixture_tmp}"
e2fsck -fn "${fixture_tmp}"
mv "${fixture_tmp}" "${builder_path}"
rm -rf "${rootfs_dir}"

printf '%s\n%s\n%s\n' "${builder_digest}" "faas-base-layout-v3" \
  "guest-init-sha256=${guest_init_sha}" > "${builder_path}.digest"

echo "native builder acceptance: build current guest-init"
cd "${repo_root}"
export HOME="${cache_root}/home"
export GOCACHE="${cache_root}/go-build"
export GOMODCACHE="${cache_root}/go-mod"
export GOPATH="${cache_root}"
CGO_ENABLED=0 "${FAAS_ACCEPTANCE_GO}" build -trimpath -tags linux -o "${guest_init}" ./guest/init
# The host's installed operator CLI may predate the live schema. Build the
# matching source revision so drain/activate always use the lifecycle column
# shipped by this acceptance bundle.
CGO_ENABLED=0 "${FAAS_ACCEPTANCE_GO}" build -trimpath -o "${gregalectl_path}" ./cmd/gregalectl

# Remove the node from placement and wait for live workloads to leave before
# the final process-level check. This closes the race where CI used to stop
# production daemons while the scheduler still considered the node active.
FAAS_ACCEPTANCE_GREGALECTL="${gregalectl_path}" \
  bash "${repo_root}/scripts/ci/native-builder-node-maintenance.sh" enter "${maintenance_state}"
if firecracker_running; then
  die "a Firecracker workload remains after the node reached drain-safe state"
fi
for service in "${services[@]}"; do
  if systemctl is-active --quiet "${service}"; then
    printf '%s\n' "${service}" >> "${active_services}"
  fi
done
systemctl stop "${services[@]}"

PATH="$(dirname "${FAAS_ACCEPTANCE_GO}"):$(dirname "${FAAS_ACCEPTANCE_CRANE}"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
export FAAS_TEST_KERNEL="${kernel}"
export FAAS_TEST_BASE_ROOTFS="${runtime_base}"
export FAAS_BUILDER_BASE_PATH="${builder_path}"
export FAAS_GUEST_INIT="${guest_init}"
export FAAS_TEST_FC_VERSION="${fc_version}"
export FAAS_METAL_ACCEPTANCE_ROOT="${stage_root}"
export FAAS_METAL_NODE_ACCEPTANCE=1
export FAAS_METAL_BUILD_TIMEOUT_SECONDS="${FAAS_METAL_BUILD_TIMEOUT_SECONDS:-900}"
export METAL_BUILDER_TIMEOUT="${METAL_BUILDER_TIMEOUT:-60m}"

echo "native builder acceptance: run Firecracker builder suite"
make GO="${FAAS_ACCEPTANCE_GO}" test-metal-builder
