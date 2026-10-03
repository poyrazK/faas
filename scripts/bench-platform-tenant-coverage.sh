#!/bin/sh
set -eu

: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL database}"

task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
log_file=$(mktemp "${TMPDIR:-/tmp}/gregale-tenant-coverage.XXXXXX")
trap 'rm -f "$log_file"' EXIT HUP INT TERM

if ! (
	cd "$task_root"
	FAAS_RUN_PLATFORM_TENANT_COVERAGE_SCALE=1 "${GO:-go}" test -p 1 ./pkg/state \
		-run '^TestPgPlatformTenantStatementCoverageScale$' -count=1 -v
) >"$log_file" 2>&1; then
	tail -n 100 "$log_file" >&2
	exit 1
fi

grep -E 'coverage scale:|coverage timings:|coverage backend:|^--- PASS:|^PASS$|^ok[[:space:]]' "$log_file"
