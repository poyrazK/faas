#!/usr/bin/env bash
# Re-run TestLogsLiveAndArchivedMetal after a test-only change: reuse the
# daemon binaries in <run-dir>, move its source to <commit>, rebuild only
# e2e.test, then run under the acceptance lock via logs-e2e-run.sh's tail.
# Usage: sudo -b setsid nohup bash logs-e2e-rerun.sh <run-dir> <commit> >/dev/null 2>&1 </dev/null
set -uo pipefail
W="$1"
C="$2"
exec >>"$W/rerun.log" 2>&1
echo "rerun start $(date -u +%FT%TZ)"
TC=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin
(
  export PATH="$TC:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  export GOTOOLCHAIN=local GOMODCACHE=/mnt/e2e-scratch/sr-gomod GOCACHE="$W/go-build" GOTMPDIR="$W/tmp"
  export GOGC=25 GOMEMLIMIT=4GiB GOMAXPROCS=2 GOFLAGS=-modcacherw
  cd "$W/src" || exit 1
  git fetch -q --depth 1 origin "$C" && git checkout -q FETCH_HEAD || { echo FETCH_FAILED; exit 1; }
  echo "revision $(git rev-parse HEAD)"
  CGO_ENABLED=0 nice go test -c -vet=off -p 1 -trimpath -tags metal -ldflags "-s -w" -o "$W/e2e.test" ./cmd/e2e || { echo BUILD_FAILED; exit 1; }
  echo "BUILD_OK $(date -u +%FT%TZ)"
)
rc=$?
rm -rf "$W/go-build"
[ "$rc" = 0 ] || { echo "STOPPED_BEFORE_TEST rc=$rc"; exit 1; }
bash "$W/logs-e2e-test.sh" "$W"
echo "RERUN_EXIT=$? $(date -u +%FT%TZ)"
