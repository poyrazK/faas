#!/usr/bin/env bash
# Build a pushed branch's daemons and e2e.test on gregale-internal-test-1's
# scratch disk, then run the metal tests matching <run-regex> under the shared
# acceptance lock with leakcheck before and after (e2e-test.sh, e2e-inner.sh).
# Usage: sudo -b setsid nohup bash e2e-run.sh <run-dir> <branch> <run-regex> >/dev/null 2>&1 </dev/null
set -uo pipefail
W="$1"
BRANCH="$2"
RUN="$3"
mkdir -p "$W/bin" "$W/tmp" "$W/vmmd-resources"
exec >>"$W/run.log" 2>&1
echo "start $(date -u +%FT%TZ) run=$RUN"
TC=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.10.linux-amd64/bin
idle() { ! pgrep -f '(^| )go test |/pkg/tool/linux_amd64/compile ' >/dev/null; }
(
  export PATH="$TC:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  export GOTOOLCHAIN=local GOMODCACHE=/mnt/e2e-scratch/sr-gomod GOCACHE="$W/go-build" GOTMPDIR="$W/tmp"
  export GOGC=25 GOMEMLIMIT=4GiB GOMAXPROCS=2 GOFLAGS=-modcacherw
  [ -d "$W/src" ] || git clone -q --depth 1 --branch "$BRANCH" https://github.com/poyrazK/faas "$W/src" || { echo CLONE_FAILED; exit 1; }
  cd "$W/src" || exit 1
  echo "revision $(git rev-parse HEAD)"
  for _ in $(seq 1 360); do idle && break; sleep 20; done
  go mod download || { echo MOD_DOWNLOAD_FAILED; exit 1; }
  ok=1
  for d in apid schedd vmmd imaged gatewayd-internal gatewayd-public meterd builderd \
    vmmd-stream-bridge vmmd-raw-bridge vmmd-tcp-bridge vmmd-udp-bridge; do
    CGO_ENABLED=0 nice go build -p 1 -trimpath -tags metal -ldflags "-s -w" -o "$W/bin/$d" "./cmd/$d" || { echo "BUILD_FAILED $d"; ok=0; }
  done
  CGO_ENABLED=0 nice go build -p 1 -trimpath -ldflags "-s -w" -o "$W/bin/vmmd-jail-helper" ./cmd/vmmd-jail-helper || { echo "BUILD_FAILED vmmd-jail-helper"; ok=0; }
  CGO_ENABLED=0 nice go build -p 1 -trimpath -buildvcs=false -o "$W/init" ./guest/init || { echo "BUILD_FAILED init"; ok=0; }
  (cd pkg/e2etest/testdata/helloserver && CGO_ENABLED=0 nice go build -trimpath -ldflags "-s -w" -o "$W/hello-server" .) || { echo "BUILD_FAILED hello-server"; ok=0; }
  (cd pkg/e2etest/testdata/tracingserver && CGO_ENABLED=0 nice go build -trimpath -ldflags "-s -w" -o "$W/tracing-server" .) || { echo "BUILD_FAILED tracing-server"; ok=0; }
  (cd sdk/go/preview/tracing/testdata/server && CGO_ENABLED=0 nice go build -trimpath -ldflags "-s -w" -o "$W/go-tracing-helper" .) || { echo "BUILD_FAILED go-tracing-helper"; ok=0; }
  CGO_ENABLED=0 nice go test -c -vet=off -p 1 -trimpath -tags metal -ldflags "-s -w" -o "$W/e2e.test" ./cmd/e2e || { echo "BUILD_FAILED e2e.test"; ok=0; }
  [ "$ok" = 1 ] || { echo BUILD_FAILED; exit 1; }
  echo "BUILD_OK $(date -u +%FT%TZ)"
)
rc=$?
rm -rf "$W/go-build"
[ "$rc" = 0 ] || { echo "STOPPED_BEFORE_TEST rc=$rc"; exit 1; }
cp "$W"/src/deploy/scripts/leakcheck* "$W/"
cp /mnt/e2e-scratch/e2e-test.sh /mnt/e2e-scratch/e2e-inner.sh "$W/"
E2E_RUN="$RUN" bash "$W/e2e-test.sh" "$W"
echo "RUN_SCRIPT_EXIT=$? $(date -u +%FT%TZ)"
