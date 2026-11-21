#!/bin/bash
set -euo pipefail
base=/mnt/e2e-scratch/gregale-run-pipeline-progress-20261117
cd "$base/source"
export PATH=/var/cache/faas-native-e2e/go-mod/golang.org/toolchain@v0.0.1-go1.26.11.linux-amd64/bin:$PATH
mkdir -p "$base/tmp"
export TMPDIR="$base/tmp"
export GOPATH=/var/cache/faas-native-e2e GOMODCACHE=/var/cache/faas-native-e2e/go-mod GOCACHE=/var/cache/faas-native-e2e/go-build GOMEMLIMIT=4GiB
export GOFLAGS='-p=1 -trimpath -gcflags=all=-dwarf=false "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/apid/apidserver_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/state_test=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/pkg/sched=-c=1 -dwarf=false" "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-c=1 -dwarf=false"'
trap 'echo "exit=$?" > "$base/evidence/exit-status"' EXIT
echo domain-testing > "$base/evidence/progress"
go test -vet=off ./pkg/apid/runpipeline ./pkg/apid/agents -count=1 > "$base/evidence/domain.log" 2>&1
echo routing-testing > "$base/evidence/progress"
go test ./pkg/apid ./pkg/apid/capgate -run '^Test(IsApidPath|PipelineProgressAndRerunsRemainPreview)$' -count=1 > "$base/evidence/routing.log" 2>&1
echo state-build > "$base/evidence/progress"
go test -vet=off -c -o "$base/evidence/state.test" ./pkg/state > "$base/evidence/state-build.log" 2>&1
"$base/evidence/state.test" -test.run '^TestMem(PipelineProgressAndRerun|PipelineCompletionAndAgentObligation|ExecutionPipelineFences|RunCustomerControls|VersionedRunTasks|ExecutionFinishedDelivery)$' -test.v > "$base/evidence/state-mem.log" 2>&1
echo state-mem-pass > "$base/evidence/progress"
echo postgres-testing > "$base/evidence/progress"
pgwork=/mnt/e2e-scratch/gregale-pipeline-progress-pgtest-20261117
install -d -o postgres -g postgres -m 0700 "$pgwork"
install -o postgres -g postgres -m 0700 "$base/evidence/state.test" "$pgwork/state.test"
set +e
sudo -u postgres bash -c 'set -eu;cd /mnt/e2e-scratch/gregale-pipeline-progress-pgtest-20261117;echo $$ > test.pid;export DATABASE_URL="postgres:///postgres?host=/var/run/postgresql&user=postgres" FAAS_PGTEST_TEMPLATE_DATABASE=1;exec ./state.test -test.run "^TestPg(PipelineProgressAndRerun|PipelineCompletionAndAgentObligation|ExecutionPipelineFences|RunCustomerControls|VersionedRunTasks|ExecutionFinishedDelivery)$" -test.v' > "$base/evidence/state-pg.log" 2>&1
pg_status=$?
set -e
pg_pid=$(cat "$pgwork/test.pid")
sudo -u postgres psql -X -v ON_ERROR_STOP=1 -v pipeline_pid="$pg_pid" -d postgres > "$base/evidence/pg-cleanup.log" <<'SQL'
SELECT format('DROP DATABASE %I',datname) FROM pg_database
WHERE datdba=(SELECT oid FROM pg_roles WHERE rolname='postgres')
AND datname ~ ('^faas_test_(template|clone)_' || :'pipeline_pid' || '_[0-9a-f]+$')
\gexec
SQL
if [ "$pg_status" -ne 0 ]; then exit "$pg_status";fi
echo state-pg-pass > "$base/evidence/progress"
echo api-testing > "$base/evidence/progress"
go test -vet=off ./pkg/apid/apidserver -run '^(TestPipelineLiveProgressAndExplicitRerun|TestPipelineAgentCompletionAcrossRestartAndRollback|TestNamedPipelineRetryRestartBudgetAndCancellation|TestManagedExecutionWorkflow.*|TestVersionedTaskPublicationInvocationAndRollback)$' -count=1 > "$base/evidence/api.log" 2>&1
echo api-pass > "$base/evidence/progress"
echo cli-testing > "$base/evidence/progress"
go test -vet=off ./cmd/gregale -run '^Test(TaskPipelineManifestRequiresManagedMode|PipelineWatchRejectsInvalidCursorBeforeAuthentication)$' -count=1 > "$base/evidence/cli.log" 2>&1
echo contracts-testing > "$base/evidence/progress"
go test ./scripts/ci/speccompliance ./scripts/ci/teststorecheck > "$base/evidence/contracts.log" 2>&1
go run ./cmd/sdk-spec > "$base/evidence/sdk-openapi.yaml"
(cd sdk/go && go test ./preview -run '^Test(ExecutionPipelineV2AndAttemptLineage|PipelineProgressCursorAndRerunContract)$' -count=1) > "$base/evidence/go-sdk.log" 2>&1
echo functional-pass > "$base/evidence/progress"
