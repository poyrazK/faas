#!/usr/bin/env bash
# check_edge_store.sh [DAEMON...] — fail when an edge daemon reaches the
# whole store type (docs/adr/20261021092138-edge-store-blast-radius.md).
#
# Every daemon links reflection, so the Go linker keeps the method names of
# every type reachable from an interface value. A daemon that reaches
# *state.PgStore or state.Store that way carries all of their methods, and
# adding any store method changes its code and redeploys its lane. The edge
# daemons run on state.EdgeStore instead. This links each daemon with the
# release build configuration and -ldflags=-dumpdep and fails, naming the
# referrers, when either type is reachable. Default: gatewayd-public.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
module="$(awk '$1 == "module" { print $2; exit }' go.mod)"
tags="$(sed -n 's/^DAEMON_TAGS := //p' Makefile)"
read -r -a flags <<<"$(sed -n 's/^GO_BUILD_FLAGS := //p' Makefile)"
[[ -n "$module" && -n "$tags" ]] || { echo "check_edge_store: cannot read go.mod or Makefile" >&2; exit 2; }
(($#)) || set -- gatewayd-public

types="type:\\*?${module//./\\.}/pkg/state\\.(PgStore|Store)"
status=0
for daemon in "$@"; do
  [[ "$daemon" =~ ^[a-z0-9-]+$ && -d "cmd/$daemon" ]] || { echo "check_edge_store: no daemon $daemon" >&2; exit 2; }
  dep="$(mktemp)"
  if ! GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO:-go}" build "${flags[@]}" -tags "$tags" -ldflags=-dumpdep \
      -o /dev/null "./cmd/$daemon" 2>"$dep"; then
    grep -v ' -> ' "$dep" >&2 || true
    rm -f -- "$dep"
    echo "check_edge_store: building $daemon failed" >&2
    exit 2
  fi
  referrers="$(grep -E " -> ${types}( |\$)" "$dep" | grep -vE "^${types}( |<)" | awk '{ print $1 }' | sort -u || true)"
  rm -f -- "$dep"
  if [[ -n "$referrers" ]]; then
    echo "$daemon reaches *state.PgStore or state.Store, so every store change rebuilds it; take state.EdgeStore or a narrow interface instead. Referred from:" >&2
    while IFS= read -r referrer; do echo "  $referrer" >&2; done <<<"$referrers"
    status=1
  else
    echo "check_edge_store: $daemon does not reach *state.PgStore or state.Store"
  fi
done
exit "$status"
