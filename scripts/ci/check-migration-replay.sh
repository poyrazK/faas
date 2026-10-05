#!/usr/bin/env bash
# adr: 592. Exercise ordinary replay and reviewed frozen-SQL recovery.
set -euo pipefail

scripts/ci/with-pg16-unix.sh bash -euo pipefail <<'REPLAY_TESTS'
go test -race -count=1 -v -run TestNewMigrationsAreReplaySafe ./migrations/...
go test -race -count=1 -v -run TestApplicationStandardLedgerRecovery ./pkg/db
REPLAY_TESTS
