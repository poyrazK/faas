#!/usr/bin/env bash
# Main-only CI entrypoint on the designated idle acceptance host. Host-owned
# fixture credentials are never carried in SSH arguments or public artifacts.
set -euo pipefail
umask 077
die() { echo "binding native: $*" >&2; exit 1; }
preflight_only=0
case "$#:${1:-}" in 0:) ;; 1:--preflight) preflight_only=1 ;; *) die 'usage: run-binding-native.sh [--preflight]' ;; esac
[[ "$(id -u)" == 0 && "$(uname -s)" == Linux && "$(uname -m)" == x86_64 && -r /dev/kvm ]] ||
  die 'root, Linux amd64 and KVM are required'
[[ -f /etc/faas/builder-acceptance-host ]] || die 'host is not designated for isolated acceptance'
for tool in git python3 make systemctl systemd-detect-virt pgrep; do command -v "$tool" >/dev/null || die "missing $tool"; done
for kind in --vm --container; do
  detected_status=0
  detected="$(systemd-detect-virt "$kind")" || detected_status=$?
  [[ "$detected" == none && "$detected_status" == 1 ]] || die 'native host class could not be established; virtual diagnostics cannot qualify'
done
for service in faas-apid faas-schedd faas-vmmd faas-builderd faas-imaged faas-gatewayd-internal faas-gatewayd-public; do
  if systemctl is-active --quiet "$service"; then die "refusing active $service"; fi
done
process_status=0
pgrep -x 'firecracker(-v[0-9].*)?' >/dev/null || process_status=$?
[[ "$process_status" == 1 ]] || die 'Firecracker workloads are active or process inventory is unavailable'
config=/etc/faas/bindings-native.env
[[ -f "$config" && ! -L "$config" && "$(stat -c '%a %u' "$config")" == '600 0' ]] ||
  die 'configure root-owned mode-0600 /etc/faas/bindings-native.env with disposable TLS SQL and approved runtime fixtures'
set -a
# shellcheck disable=SC1090
source "$config"
set +a
: "${DATABASE_URL:?Configure a disposable TLS/SCRAM PostgreSQL catalog in bindings-native.env}"
: "${GREGALE_POSTGRES_NATIVE_CA_PATH:?Configure the disposable SQL CA with the fixture IP SAN}"
: "${FAAS_TEST_KERNEL:?Configure the approved kernel}"
: "${FAAS_TEST_MINIMAL_BASE_PATH:?Configure the approved minimal deploy base}"
: "${FAAS_BUILDER_BASE_PATH:?Configure the approved local builder base}"
: "${FAAS_E2E_SOURCE_SHA:?Exact source SHA is required}"
: "${FAAS_E2E_TRANSFER_ROOT:?Owned transfer root is required}"
: "${FAAS_E2E_GO:?Pinned Go binary is required}"
[[ "$FAAS_E2E_SOURCE_SHA" =~ ^[0-9a-f]{40}$ && -x "$FAAS_E2E_GO" ]] || die 'invalid source identity or Go binary'
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
[[ -d .git && ! -L .git && "$(git rev-parse HEAD)" == "$FAAS_E2E_SOURCE_SHA" && -z "$(git status --porcelain)" ]] || die 'exact clean regular Git checkout required'
[[ -z "${FAAS_SKIP_PG_TESTS:-}" ]] || die 'PostgreSQL tests cannot be skipped'
for fixture in "$GREGALE_POSTGRES_NATIVE_CA_PATH" "$FAAS_TEST_KERNEL" "$FAAS_BUILDER_BASE_PATH" "$FAAS_TEST_MINIMAL_BASE_PATH" "$FAAS_TEST_MINIMAL_BASE_PATH.digest"; do
  [[ -f "$fixture" && -r "$fixture" ]] || die 'approved native fixture is missing or unreadable'
done
if [[ "$preflight_only" == 1 ]]; then
  echo 'binding native: preflight passed; no builds, daemons or guests started'
  exit 0
fi
export PATH="$(dirname "$FAAS_E2E_GO"):$PATH"
export GOTOOLCHAIN=local GOMEMLIMIT=6GiB GOGC=10 GOMAXPROCS=2
export GOPATH=/var/cache/faas-binding-native
export GOCACHE="$GOPATH/go-build" GOMODCACHE="$GOPATH/go-mod"
mkdir -p "$GOCACHE" "$GOMODCACHE"
"$FAAS_E2E_GO" run ./scripts/ci/nativetestinventory --check
export FAAS_GUEST_INIT="$FAAS_E2E_TRANSFER_ROOT/binding-guest-init"
[[ ! -e "$FAAS_GUEST_INIT" && ! -L "$FAAS_GUEST_INIT" ]] || die 'guest-init output already exists'
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$FAAS_E2E_GO" build -trimpath -buildvcs=true -tags linux -o "$FAAS_GUEST_INIT" ./guest/init
exec python3 scripts/ci/binding-native-lane.py --source "$FAAS_E2E_SOURCE_SHA" --out "$FAAS_E2E_TRANSFER_ROOT/binding-evidence" --go "$FAAS_E2E_GO"
