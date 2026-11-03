#!/bin/sh
# Deploy create against a production-sized fleet (ADR-20261027163626 rollout
# step 3): 10,000 apps and 1,000 route-owning accounts, under the fleet-wide
# and the scoped traffic binding guard. Prints latency and phase lines.
set -eu

: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL database}"

task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
log_file=$(mktemp "${TMPDIR:-/tmp}/gregale-traffic-guard.XXXXXX")
trap 'rm -f "$log_file"' EXIT HUP INT TERM

if ! (
	cd "$task_root"
	FAAS_RUN_TRAFFIC_GUARD_SCALE=1 "${GO:-go}" test -p 1 ./pkg/state \
		-run '^TestPgTrafficGuardFleetScale$' -count=1 -v -timeout 60m
) >"$log_file" 2>&1; then
	tail -n 100 "$log_file" >&2
	exit 1
fi

grep -E 'traffic guard (scale|phases):|^--- PASS:|^PASS$|^ok[[:space:]]' "$log_file"
