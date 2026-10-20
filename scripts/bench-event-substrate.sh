#!/bin/sh
# Measures the event substrate (publish, fanout, drain, webhook delivery) on
# disposable PostgreSQL: spec Appendix D V12, docs/ops/event-substrate-benchmark.md.
set -eu

: "${DATABASE_URL:?Set DATABASE_URL to a disposable PostgreSQL database}"

task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
log_file=$(mktemp "${TMPDIR:-/tmp}/gregale-event-substrate.XXXXXX")
trap 'rm -f "$log_file"' EXIT HUP INT TERM

if ! (
	cd "$task_root"
	FAAS_RUN_EVENT_SUBSTRATE_BENCH=1 FAAS_PGTEST_TEMPLATE_DATABASE="${FAAS_PGTEST_TEMPLATE_DATABASE:-1}" "${GO:-go}" test -p 1 -count=1 -v -timeout 90m \
		-run '^(TestEventSubstrateBench|TestWebhookDeliveryBench)$' ./pkg/sched ./pkg/webhook
) >"$log_file" 2>&1; then
	tail -n 100 "$log_file" >&2
	exit 1
fi

grep -E 'event-substrate:|^--- PASS:|^PASS$|^ok[[:space:]]' "$log_file"
