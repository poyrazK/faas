#!/usr/bin/env bash
set -euo pipefail
: "${DATABASE_URL:?Set DATABASE_URL to a disposable local PostgreSQL 16 cluster with CREATEDB}"
repair_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
repair_tmp=$(mktemp -d "${TMPDIR:-/tmp}/gregale-repair-gate.XXXXXX")
trap 'rm -rf "$repair_tmp"' EXIT
cd "$repair_root"
command -v promtool >/dev/null || { echo 'promtool is required for the strict repair health alert gate' >&2; exit 1; }
"${GO:-go}" run scripts/gen-data-repair-readiness.go --check
"${PYTHON:-python3}" scripts/ci/test_data_repair_native.py
node --test pkg/apid/datarepair/verification_web_test.mjs pkg/apid/datarepair/history_web_test.mjs pkg/apid/datarepair/doctor_web_test.mjs
"${GO:-go}" build -o "$repair_tmp/gregale" ./cmd/gregale
CGO_ENABLED=0 "${GO:-go}" test -c -o "$repair_tmp/guest-test" ./pkg/datarepair/guest
export GREGALE_DATA_REPAIR_GUEST_HELPER="$repair_tmp/guest-test"
export GREGALE_DATA_REPAIR_CLI="$repair_tmp/gregale"
export GREGALE_DATA_REPAIR_SCHEMA="$repair_tmp/schema.sql"
"$GREGALE_DATA_REPAIR_CLI" data-repair schema > "$GREGALE_DATA_REPAIR_SCHEMA"
GREGALE_DATA_REPAIR_DOCKER_TEST=1 "${GO:-go}" test -race -count=1 ./pkg/datarepair ./pkg/apid/datarepair ./pkg/apid/repairhealth ./pkg/sched/repairhealth ./pkg/dashboard
"${GO:-go}" test -p=1 -race -gcflags=github.com/onebox-faas/faas/pkg/sched='-c=1 -dwarf=false' -count=1 -run 'Test(DataRepair|RepairRehearsal|NativeProducer)' ./pkg/fcvm ./pkg/sched ./pkg/vmmdgrpc ./guest/init
GOMEMLIMIT="${GOMEMLIMIT:-6GiB}" "${GO:-go}" test -race -gcflags=github.com/onebox-faas/faas/pkg/state='-c=1 -dwarf=false' -count=1 -run '^Test(ManagedDataRepairState|DataRepairHealth)' ./pkg/state
GREGALE_DATA_REPAIR_DOCKER_TEST=1 make test-apid GO="${GO:-go}" RUN='^Test(ManagedDataRepair(Named|Validation|Replacement)?(Scheduled)?HTTPJourney|ManagedDataRepairHistoryRequiresMFA|OperatorDataRepairHealth.*|DashboardAdmin_TLSCutoverBannerPersistsAfterRollback)$' RACE=1
"${GO:-go}" test -count=1 -run '^Test(DataRepairHealthRules|AlertMetricsAreEmitted)$' ./pkg/promqlrules
bash scripts/ci/check_prometheus_alert_metadata.sh "$repair_root"
"${GO:-go}" test -run 'Test(DataRepair|HelpDocsLinks|MarkdownReferenceFresh|RequiredFlagsWithoutValueAreMarkedBool)' ./cmd/gregale
(cd sdk/commit-tests/go && "${GO:-go}" test -count=1 -run '^TestDataRepair' .)
(cd sdk/node && npm run build && node --test test/data-repair-postgres.test.mjs)
PYTHONPATH="$repair_root/sdk/python" "${PYTHON:-python3}" -m unittest discover -s sdk/python/tests -p test_data_repair_postgres.py -v
"${PYTHON:-python3}" scripts/test-data-repair-journey.py
