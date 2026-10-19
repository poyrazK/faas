# Native migration contract qualification

ADR-20261019120017 admission remains disabled by default. The new
`project-migration-contract-only` lane is a qualification harness, not evidence
that this capability has passed on native hardware.

The lane requires three independent scenarios to PASS:

- `TestProjectMigrationContractSuccessMetal`: execute expansion in a real task
  guest, complete 5/25/100 rollout and ten-minute stabilization, wait for retained
  graphs and revision pins to expire, then explicitly admit cleanup. Stop schedd
  during acceptance and replay the request after restarting APID. The same task
  must execute once with the candidate artifact/configuration and exit zero.
- `TestProjectMigrationContractFailureMetal`: the cleanup guest exits 23. The
  task receipt must preserve the failure and may not be retried. Retired code
  remains fenced despite the failure.
- `TestProjectMigrationContractInterruptedMetal`: kill schedd while cleanup is
  running, allow the actual lease to expire, then restart its owner. The missing
  exit receipt must surface as `uncertain`; execution is never replayed.

Every scenario rejects early cleanup during real retention, rejects another
cleanup request after admission, rejects republishing the old graph, and sends
an exact old revision request through the native gateway. Old guests must stay
parked or terminal. Existing fixture teardown deletes apps through APID, waits
for runtime retirement, and requires zero leaked resources. The guarded runner
also reaps failed-run guests and requires its final leakcheck to succeed.

The private harness enables contract and canary admission. Production services
are not changed. The fixture uses a 30-minute public revision pin policy and
30-minute graph retention, both declared before source deployment. It never
updates proof rows, deadlines, expiration timestamps or task leases to advance
acceptance. Real worker deliveries supply the health samples. No source build
may be resubmitted during recovery.

Run the `e2e-native.yml` workflow with lane `project-migration-contract-only`, or
use the guarded runner with an operator-staged exact source archive:

```bash
FAAS_E2E_PHASE=project-migration-contract-only \
FAAS_E2E_SOURCE_SHA="$qualified_revision" \
FAAS_E2E_GO=/path/to/pinned/go/bin/go \
bash scripts/ci/run-native-e2e.sh
```

Follow [native CI](e2e-native-ci.md) for the dedicated native Linux amd64 host,
root/KVM requirements, PostgreSQL setup, archive identity and signed base assets.
The dedicated lane adds the `contractacceptance` build tag; the regular full
platform matrix retains its existing scope and time budget.
The test budget is 210 minutes inside a 215-minute systemd envelope. A missing,
skipped or failed scenario rejects the lane. Keep the exact source SHA, all
`contract native:` observations, each PASS line and successful final leakcheck.
An operator must review those artifacts before enabling admission through normal
deployment.

Portable preflight checks:

```bash
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
go test -tags=metal ./cmd/e2e -run '^TestProjectWorkerReleaseFixtureMeta$'
```

These validate fixture ownership/source policy, full test selection, and verdict
rejection of skipped/failed/missing outcomes. They do not qualify guest execution.
The PostgreSQL integration test
`TestMigrationContractPGRejectsWakeWaitingOnAdmission` separately exercises a
wake waiting on the admission transaction's lock; the native gateway check does
not reproduce that database scheduling interleaving.

This suite uses guest command exit/output as its execution evidence. It does not
prove application SQL compatibility, destructive SQL semantics, or migration-only
managed database credential delivery. The [managed PostgreSQL contract lane](project-migration-sql-native-acceptance.md)
now supplies that additional fixture; its native qualification remains pending. The managed
workspace cannot run native KVM acceptance, so qualification remains pending.

## Workspace preflight results (2026-10-14)

Lane selection, missing/skip/failure verdict checks, fixture admission, suite
compilation, guest fixture regressions, and the standalone contract CI budget
checks passed. Native execution was refused because the workspace is not root;
leakcheck could not inspect `/dev/loop0` through `/dev/loop7`. Neither is a native
qualification pass.

The full CI matrix now runs source/runtime phase groups as two serialized jobs,
with a SHA-bound combined verdict. The budget guard verifies each job independently,
including cleanup overhead; the former six-hour full-matrix overflow is resolved.
