#!/bin/bash
set -euo pipefail
base=/mnt/e2e-scratch/gregale-run-pipeline-progress-20261117
cd "$base/source"
export PATH=/var/cache/faas-native-e2e/go-mod/golang.org/toolchain@v0.0.1-go1.26.11.linux-amd64/bin:$PATH
mkdir -p "$base/tmp"
export TMPDIR="$base/tmp"
export GOPATH=/var/cache/faas-native-e2e GOMODCACHE=/var/cache/faas-native-e2e/go-mod GOCACHE=/var/cache/faas-native-e2e/go-build GOMEMLIMIT=9GiB
export GOFLAGS='-p=1 -trimpath -gcflags=all=-dwarf=false "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/sched=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false"'

trap 'echo "exit=$?" > "$base/evidence/lint-exit-status"' EXIT
for n in $(seq 1 1800); do
 state=$(systemctl show gregale-pipeline-progress-20261117.service -p ActiveState --value)
 if [ "$state" = failed ]; then exit 75; fi
 if [ "$state" = inactive ]; then break; fi
 sleep 5
done
[ "$state" = inactive ]
[ "$(cat "$base/evidence/exit-status")" = "exit=0" ]
export GOLANGCI_LINT_CACHE=/mnt/e2e-scratch/gregale-run-pipeline-completion-20261116/lint-cache
mkdir -p "$base/tmp"
export TMPDIR="$base/tmp"
for package in ./pkg/apid/runpipeline ./pkg/state ./pkg/api ./pkg/apid ./pkg/apid/capgate ./cmd/gregale ./pkg/apid/apidserver; do
 name=${package##*/}
 echo "lint-$name" > "$base/evidence/progress"
 go tool golangci-lint run --concurrency=1 --timeout=20m "$package" > "$base/evidence/lint-$name.log" 2>&1
done
echo all-pass > "$base/evidence/progress"
