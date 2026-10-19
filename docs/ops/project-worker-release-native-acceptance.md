# Native project worker release acceptance

ADR-20261019120008 admission remains disabled by default. This suite supplies the native
qualification path for `gregale deploy --project --atomic --verify --workers`,
including paired runtime variables. No native pass is recorded by adding or
compiling these tests.

## Required scenarios

`TestProjectWorkerReleaseMetal` runs three scenarios through real APID, builderd,
imaged, schedd, vmmd, public/internal gateways, PostgreSQL and Firecracker guests.
The fixture prepares a static Go executable; each deployment's Dockerfile COPY
build still runs in a native builder microVM. The generated OCI images contain
separate HTTP and worker commands and an artifact generation file. A guest state
endpoint reports generation, delivered runtime configuration and per-job counts.
Only initial customer intent is seeded directly; readiness proofs, instance
transitions, source deployment acceptance and cutover/rollback use daemon paths.

| Scenario | Required evidence |
| --- | --- |
| Isolation, restart, cutover and rollback | Stop APID immediately after durable candidate acceptance. Candidates finish native startup while the prior graph serves HTTP and queue work. Candidate worker reports the proposed variables and zero jobs. Restart schedd and verify the same held VM remains isolated. Restart APID; the original batch cuts over without extra deployments. An old-generation job held until SIGTERM completes on its first attempt. New jobs reach the candidate. Real failed critical-probe samples restore the retained graph and variables. Completed queue rows remain completed with one attempt; fresh jobs reach the restored worker. |
| Unlaunchable worker | A source build with a missing worker entrypoint fails the release without publishing its graph. HTTP and queue delivery remain on the retained generation and configuration. |
| Ignored stop signal | Tell the old guest to ignore SIGTERM, then publish a healthy candidate. The old worker reaches STOPPED and loses its native network namespace within the five-second guest grace plus fifteen seconds for confirmed transport/resource teardown. Two real minute-spaced healthy probe rounds finalize the release; new jobs reach the successor. |

Native app cleanup deletes the fixture apps through APID, waits for retirement
and checks host resources. The outer runner also reaps failed-run guests, runs
`leakcheck`, and restores every production service it quiesced. A skip, missing
test, failed step, failed cleanup or failed leakcheck cannot qualify the lane.
These scenarios assert first-attempt delivery; they do not promise exactly-once
external effects or reversal of completed jobs.

## Run on the designated host

Use the existing `e2e-native.yml` workflow with lane
`project-worker-release-only`. The lane has a blocking verdict, a 75-minute test
budget and an 80-minute systemd envelope. The whole native suite also selects the
scenario in its deploy phase; fixture-shape checks run in the fixtures phase.
The existing parallel push-queue test is now included in the deploy partition.

For an operator-staged, exact source archive on the designated host, the same
runner can be invoked with:

```bash
FAAS_E2E_PHASE=project-worker-release-only \
FAAS_E2E_SOURCE_SHA="$qualified_revision" \
FAAS_E2E_GO=/path/to/pinned/go/bin/go \
bash scripts/ci/run-native-e2e.sh
```

Follow [native end-to-end CI](e2e-native-ci.md) for the archive marker, host-owned
PostgreSQL credentials, signed base assets, guest-init, Firecracker and kernel
staging. The runner requires root, native x86_64 KVM and the designated-host
marker. The test rejects nested or virtualized hosts and uses a private migrated
PostgreSQL database. Run only through the guarded owner so host locking, service
restoration and cleanup remain active. No production admission setting is changed
by this workflow; the flag is enabled only inside the private test harness.

Collect the combined log, the three `ADR-20261019120008 native` observation lines, the
parent and child PASS lines, and the final leakcheck result for the exact tested
source commit. Only after that evidence passes should an operator enable
`FAAS_ATOMIC_WORKER_RELEASES_ENABLED=1` through the normal deployment process.

## Local checks and remaining qualification

Without KVM, compile the metal suite, run the fixture-shape check and the guest
workload race tests, and run the native phase/verdict contracts. These validate
compilation, source-only admission, deterministic archives, executable modes,
cooperative and ignored-stop fixture behavior, required-test coverage and the
blocking workflow wiring. They do not validate native queue or VM behavior.

The managed development container has no `/dev/kvm` and cannot inspect native
loop devices. Native execution and a successful final leakcheck remain pending.
The admission gate stays disabled while those requirements are unmet.
