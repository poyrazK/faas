# Native environment promotion acceptance

The `project-environment-promotion-only` lane exercises staging-to-production
promotion and recovery under ADR-20261019120023/1025/1026/1027/1028/1029/1031/1033/1035. It is an explicit, bounded acceptance lane, compiled
with `metal,promotionacceptance`. It does not enable production admission or
replace the remaining scenarios in [the promotion qualification runbook](promotion-health-native-acceptance.md).
Adding, compiling or locally checking this lane does not record a native pass.

For the optional frontend/API graph capability, select
`qualify_service_graph=true` and retain the separate
[service-graph qualification](promotion-service-graph-native-acceptance.md).
Its seventh job supplements the unchanged six-part base report.

## Scenarios

All runtime scenarios use real APID, builders, imaged, schedd, vmmd, gateways,
isolated PostgreSQL and Firecracker guests. A bootstrap graph and a real paired
release establish a pinned production baseline. Separate native source builds
establish the staging graph, configuration and live worker startup proof.
Production and staging credentials differ; guest observations contain only a
comparison result. The processor is classified as a worker by repository scanning.

| Scenario | Required observations |
| --- | --- |
| Successful promotion | Candidate artifact/configuration and production credentials arrive before cutover, with zero production jobs. Stop APID, kill the candidate VM and restart schedd; the durable hold recovers a new isolated worker. Production keeps serving the baseline. Restart APID and replay the same admission key; deadlines and identity persist. Observe two real preflight rounds, then the selected graph/configuration and new-job version pins. Restart owners after cutover and require fresh minute-spaced healthy job/probe windows before success. |
| Automatic rollback | Repeat the same admission, isolation, loss/recovery and cutover path. Regress only the candidate HTTP critical endpoint. Two real regression rounds restore the captured graph, document, variables and worker admission; a fresh job and HTTP request execute the baseline. Completed jobs retain their outcomes. |
| Retry and retained pool, in both scenarios | Crash a real first delivery before promotion, with a bounded delayed retry. Its first-admission graph/deployment pin must remain pending through cutover. Terminate the retained worker after cutover and recover its pool. The eventual second attempt must complete after cutover with the original artifact/configuration and production credentials. A run in which the retry already happened before cutover fails this scenario. |
| Operator controls | Three independent admissions reuse real staging builds. Abort an in-flight pre-cutover probe and require candidate VM/namespace retirement after restart. Regress candidate HTTP health after cutover, abort, recover a killed retained worker VM, and restore only after fresh baseline rounds. Interrupt probe transport for the full ten-minute control deadline; require a blocked receipt and unchanged candidate selection/retention, repair transport, and recover with a new request. Five real delayed baseline retries supply fresh health. Preserve earlier receipts, candidate job pins and artifact identities. |
| Concurrent reconciliation | Keep 21 accepted promotions active across three isolated Scale accounts/projects and seven destinations each. Direct OCI baselines and native source-built staging artifacts use ordinary APID admission. Delay one project's gateway probes while other projects progress. Kill APID with live leases, wait for expiry/reclaim, and reject late checkpoint changes. Observe fleet/account lease bounds, give every job a turn, complete an operator restoration alongside ordinary work, and restart again without duplicate graphs or candidate deployments. |
| Admission saturation | Five accounts independently reach 24/eight active and 128/32 waiting limits. Synchronized requests contend for the last fleet slot. Restart with 128 waiters and no candidate allocations; cancel waiting, reject changed intent, interrupt validated dispatch before preparation and replay without duplicates. Exercise a full ten-minute blocked restoration, then recover above a replenished 24-operation fleet while ordinary work progresses. Stop APID for the full fifteen-minute queue expiry and preserve destination selection. |
| Database-backed promotions | Four independent single-HTTP fixtures execute real destination expansion once. Lose an admission response; restart APID with queued, running and completed tasks; retain task identity and deadlines after disabling admission. Require read-only baseline/candidate health, rollback without reversing schema, nonzero-exit refusal, and uncertain-result recovery only after scheduler-owned runtime teardown. Preserve the original uncertain task receipt and reject competing recovery/migration requests. |
| Publication crash recovery | Kill the owned APID immediately before/after both cutover and restoration commits. Prove atomic graph/configuration/receipt visibility, durable checkpoints, unchanged job pins and exactly one committed cutover/restoration. A second admission loses restoration acknowledgement while a newer public graph/configuration is installed; recovery must preserve that intent. |
| Fixture metadata | Check both workload identities, scan-derived worker classification and eligibility for paired worker release admission without KVM. This is a structural check, not runtime evidence. |

Candidate artifact/rootfs identities must equal the staging artifacts. Each prepared promotion gets one candidate deployment per workload, including
after idempotent admission replay and owner restarts. Tests do not inject runtime proofs, job results,
promotion publications or checkpoint timestamps. Production probe intervals and
deadlines remain unchanged. Fixture cleanup deletes apps through APID, waits for
instance retirement and asserts no native resource leaks. The outer guarded
runner also cleans failed runs, checks leaks and restores quiesced host services.

VM faults use a PID file descriptor for exactly one Firecracker process, verified
against the fixture worker's instance cgroup and jail UID before signalling. An
inner workload crash may restart inside its existing VM; it does not substitute
for the VM-loss recovery scenario. Ownership guard checks run in fixture metadata.

## Execute and retain evidence

Dispatch `.github/workflows/e2e-native.yml` from the qualified `main` revision,
selecting lane `project-environment-promotion-only`. Six jobs run serially:
`promotion-core` (metadata, success and automatic rollback), `promotion-controls`
(operator controls), `promotion-load` (concurrent reconciliation),
`promotion-admission` (capacity and waiting), `promotion-migrations`
(database-backed success, rollback, nonzero exit and uncertain-result recovery),
and `promotion-publication` (commit-boundary process death and newer intent).
Core, controls, load and publication each have a 210-minute test budget,
215-minute systemd envelope and 240-minute CI job. Admission and migrations have 330/335/360
minutes to cover fifteen and twelve real source builds respectively, with unshortened recovery/wait
deadlines. Each partition has independent cleanup and a blocking verdict. The core
cases each use six source builds; controls reuse six builds across three admissions;
load uses nine builds across three projects; publication reuses six builds across
two admissions; admission uses fifteen across five projects. Its bounded
32,760 MB fixture fits ordinary RAM/concurrency quotas.
Ordinary build budgets and runtime health intervals remain unchanged.
It is selected separately from the default full suite because of those budgets
and its explicit promotion admission requirements.

For an operator-staged exact source archive, use the same guarded runner on the
designated native x86_64 KVM host:

```bash
FAAS_E2E_PHASE=project-environment-promotion-only \
FAAS_E2E_PROMOTION_GROUP=promotion-controls \
FAAS_E2E_SOURCE_SHA="$qualified_revision" \
FAAS_E2E_GO=/path/to/pinned/go/bin/go \
bash scripts/ci/run-native-e2e.sh
```

Run each of `promotion-core`, `promotion-controls`, `promotion-load`, `promotion-admission`, `promotion-migrations` and `promotion-publication` against
the same exact revision and retain each log separately. The runner refuses an
unbounded combined invocation.

Follow [native CI](e2e-native-ci.md) for archive identity, root, designated-host
marker, host locking, PostgreSQL and signed builder/guest/kernel assets. Nested
virtualization is unsupported. The harness alone enables the promotion flags.
The guarded promotion runner requires `CREATEDB` and uses private test database
clones. Its qualification-only rollout access cannot use a production catalog
or produce a production-ready result; see [controlled rollout](promotion-rollout.md).

Each job uploads its `native-e2e.log` and scoped `promotion-qualification.json`.
The final job requires all six artifacts and uploads a complete
`promotion-qualification-<SHA>` report for the tested commit. The report contains
the source SHA, log SHA-256 and bounded scenario observations. A per-job check uses `--group promotion-controls` (or its
actual partition). For the complete report, concatenate the six logs in core,
controls, load, admission, migrations, publication order, then run:

```bash
python3 scripts/ci/promotion-qualification.py \
  --log native-e2e.log --sha "$qualified_revision" \
  --output promotion-qualification.json
```

Qualification requires all ten runtime PASS records, their attributed observations,
all selected tests passing, a successful commit-bound runner completion and a
clean final leakcheck for each of the six jobs. Missing/duplicate observations,
skipped children, failures, reused healthy windows, incomplete recovery evidence or contradictory runner
results fail qualification. The report always sets `admission_enabled: false`.
It is CI evidence, not a signed deployment authorization.

## Local checks and remaining qualification

Without KVM, run the fixture metadata, compile the tagged tests, test the guest
workload, and run the phase/wrapper/aggregate and evidence-validator contracts:

```bash
go test -tags metal,promotionacceptance ./cmd/e2e -run '^TestProjectEnvironmentPromotionFixtureMeta$'
go test -race ./pkg/e2etest/testdata/projectworker
python3 scripts/ci/promotion-qualification_test.py
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
bash scripts/ci/native-e2e-combined-verdict_test.sh
```

Native runtime qualification and final host leakcheck are pending until executed
on the designated host. This lane does not qualify service binding graphs, multi-service migrations, migration resolve/drift/RPC/deadline variants,
concurrent operator intent races, operator-control-specific publication interruptions, every
baseline failure/low-traffic variant or every retention/startup failure. Those
remain in their respective acceptance runbooks. Review the exact native evidence
and all applicable qualification gates before enabling admission through the
ordinary production deployment process.

## Capacity admission qualification

The `promotion-admission` partition implements ADR-20261019120032 and must execute on the
designated host. The original 21-operation load cohort alone cannot qualify the
queue. Portable parser/fixture checks and tagged compilation do not record a pass.

Admission evidence must include all 128 distinct waiters, original queue times,
fresh dispatch health deadlines, both definite 429 rejections, cancellation and
intent-drift results, and the dispatch interruption before candidate creation.
Recovery must show 24 ordinary operations before admission, 25 total/nine account
operations after the explicit urgent bypass, fresh baseline-only windows, ordinary
progress and unchanged retention. Queue expiry uses the full production fifteen
minutes with APID stopped. No health/lease/publication row mutations may substitute
for these observations.

Run `FAAS_E2E_PROMOTION_GROUP=promotion-admission` through the guarded runner to
collect this partition alone, or dispatch the existing promotion lane for all six.
The aggregate rejects a missing admission artifact even when the older three
partitions pass. Reports always leave admission disabled. Queued worker/migration
graphs and additional migration recovery variants remain separate qualification scopes.

## Database migration qualification

Run `FAAS_E2E_PROMOTION_GROUP=promotion-migrations` through the guarded runner.
It reserves the isolated SQL proxy interface and removes it during cleanup. The
PostgreSQL cluster must support TLS; missing credentials, fixtures, or a native
prerequisite cannot yield a qualified report. Each case uses three source builds
and separate source/destination databases. Native execution is pending.

Evidence includes the promotion/task/SQL run identities, one task/attempt/SQL
invocation, the unchanged source invocation count, original admission/start
times, actual exit or preserved uncertainty, selected graph, schema retention
and fresh minute-spaced health windows. Recovery adds node/runtime/lease IDs,
actual lease expiry, inspection hash, operator request, teardown and resolution
timestamps, SQL session disappearance and restart/replay results. No secret or
SQL driver diagnostic belongs in evidence.

All six partitions are required for the combined report. See
[ADR-20261019120033](../adr/20261019120033-native-promotion-migration-qualification.md) and the
[migration runbook](promotion-migration-native-acceptance.md) for the implemented
scope and remaining native requirements before enabling migration admission.

## Publication crash qualification

Run `FAAS_E2E_PROMOTION_GROUP=promotion-publication` through the same guarded
runner. The lane pauses only the real owned APID at a selected transaction
boundary, verifies its socket peer PID and sends SIGKILL. Before-commit crashes
must discard the proposed graph/configuration/checkpoint together. After-commit
crashes must retain them despite the missing acknowledgement. Ordinary lease
expiry, real minute-spaced probes and daemon recovery complete the operation.
A second admission installs newer graph/configuration intent through APID before
losing restoration acknowledgement and requires that intent to survive restart.

Retain both admissions, all five attributed process deaths, persisted publication
counts, artifact/job pins and cleanup evidence. The sixth scoped report and full
aggregate are required for the new runtime approval inventory. After executing
and reviewing the native run, retain the publication report bytes and use their
SHA-256 for the `publication-crash-boundaries` receipt in
[controlled rollout](promotion-rollout.md). Review any additional applicable
operator-control publication variants before signing; the partition does not
implicitly qualify those variants or migration execution. See ADR-20261019120035 for scope.
