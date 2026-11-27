#!/bin/bash
set -euo pipefail
base=/mnt/e2e-scratch/gregale-run-pipeline-completion-20261116
cd "$base/source"
export PATH=/var/cache/faas-native-e2e/go-mod/golang.org/toolchain@v0.0.1-go1.26.11.linux-amd64/bin:$PATH
mkdir -p "$base/tmp"
export TMPDIR="$base/tmp"
export GOPATH=/var/cache/faas-native-e2e GOMODCACHE=/var/cache/faas-native-e2e/go-mod GOCACHE=/var/cache/faas-native-e2e/go-build GOMEMLIMIT=4GiB
export GOFLAGS='-p=1 -trimpath -gcflags=all=-dwarf=false "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/sched=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false"'
trap 'echo "exit=$?" > "$base/evidence/exit-status"' EXIT
echo contracts-testing > "$base/evidence/progress"
go test ./scripts/ci/speccompliance ./scripts/ci/teststorecheck > "$base/evidence/contracts.log" 2>&1
go run ./cmd/sdk-spec > "$base/evidence/sdk-openapi.yaml"
go run ./cmd/sdk-coverage > "$base/evidence/sdk-coverage.log"
(cd sdk/go && go test ./preview -run '^TestExecutionPipelineV2AndAttemptLineage$' -count=1) > "$base/evidence/go-sdk.log" 2>&1
for package in ./pkg/apid/runpipeline ./pkg/apid/agents ./pkg/state ./pkg/api ./pkg/apid/apidserver;do
 name=${package##*/}
 echo "lint-$name" > "$base/evidence/progress"
 go tool golangci-lint run --concurrency=1 --timeout=20m "$package" > "$base/evidence/lint-$name.log" 2>&1
done
echo all-pass > "$base/evidence/progress"
