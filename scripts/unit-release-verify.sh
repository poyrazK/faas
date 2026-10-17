#!/usr/bin/env bash
# Per-unit health gate for a per-unit control-plane release
# (docs/adr/1650-per-unit-control-plane-releases.md). Runs on the control
# plane after `deployctl deploy --only` and fails unless every listed service
# is active, runs a binary from the new release, and answers its readiness
# URL.
#
#   unit-release-verify.sh RELEASE_ROOT SPEC...
#
# SPEC is SERVICE or SERVICE=READYZ_URL. A leading `?` marks an optional
# unit (apid-clone-worker, s3-gatewayd, bridged): deployctl does not manage
# it, so it is restarted here, and only when the operator enabled it and it
# still runs an older binary. An optional unit that is not active is skipped.
#
# UNIT_VERIFY_TIMEOUT (seconds, default 60) bounds each service's wait.
# UNIT_VERIFY_PROC (default /proc) is a test seam.
set -euo pipefail

if (( $# < 2 )); then
  echo "usage: $0 RELEASE_ROOT SPEC..." >&2
  exit 2
fi
release_root="${1%/}"
shift
timeout="${UNIT_VERIFY_TIMEOUT:-60}"
proc="${UNIT_VERIFY_PROC:-/proc}"
[[ "$timeout" =~ ^[0-9]+$ && -d "$release_root/bin" ]] || {
  echo "unit-release-verify: bad timeout or release root $release_root" >&2
  exit 2
}

# running_from SERVICE: the unit's main process runs a binary from the release.
running_from() {
  local pid exe
  pid="$(systemctl show "faas-$1.service" -p MainPID --value 2>/dev/null || true)"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
  exe="$(readlink "$proc/$pid/exe" 2>/dev/null || true)"
  [[ "$exe" == "$release_root/bin/"* ]]
}

ready() {
  local service="$1" url="$2"
  systemctl is-active --quiet "faas-$service.service" || return 1
  running_from "$service" || return 1
  [[ -z "$url" ]] || curl --fail --silent --show-error --max-time 2 --output /dev/null "$url"
}

status=0
for spec in "$@"; do
  optional=false
  if [[ "$spec" == \?* ]]; then
    optional=true
    spec="${spec#\?}"
  fi
  service="${spec%%=*}"
  url=""
  [[ "$spec" != *=* ]] || url="${spec#*=}"
  [[ "$service" =~ ^[a-z0-9-]+$ ]] || {
    echo "unit-release-verify: bad service name in $spec" >&2
    exit 2
  }
  if [[ "$optional" == true ]]; then
    if ! systemctl is-active --quiet "faas-$service.service"; then
      echo "faas-$service.service is not active; optional unit left as is"
      continue
    fi
    if ! running_from "$service"; then
      echo "restarting optional faas-$service.service onto the new release"
      systemctl restart "faas-$service.service"
    fi
  fi
  deadline=$(( SECONDS + timeout ))
  until ready "$service" "$url"; do
    if (( SECONDS >= deadline )); then
      echo "::error::faas-$service.service is not healthy on $release_root after ${timeout}s" >&2
      systemctl status "faas-$service.service" --no-pager >&2 || true
      journalctl -u "faas-$service.service" -n 50 --no-pager >&2 || true
      status=1
      continue 2
    fi
    sleep 1
  done
  echo "faas-$service.service healthy on $(basename "$release_root")${url:+ ($url)}"
done
exit "$status"
