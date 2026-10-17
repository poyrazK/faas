# Strict compute native acceptance

This candidate is local and unpublished. Use dedicated native x86_64 Linux KVM.

## Automated qualification

Run the existing complete metal host gate with the financial lane enabled.
The runner requires the designated host marker, the reviewed source archive's
`.faas-metal-source-sha`, the matching pinned Go toolchain, root, Firecracker,
jailer, a staged kernel, a tenant bridge and a disposable PostgreSQL cluster.
It locks the host, stages matching guest/helper binaries, stops its recorded
services, runs the entire `pkg/fcvm` metal suite and namespace batch, checks
leaks and restores the recorded services. It does not deploy the candidate.

```sh
set -euo pipefail
export FAAS_FINANCIAL_COMPUTE_ACCEPTANCE=1
export FAAS_METAL_SOURCE_SHA='<reviewed 40-character source SHA>'
export FAAS_METAL_GO='<absolute path to matching Go binary>'
export DATABASE_URL='<disposable acceptance PostgreSQL DSN>'
bash scripts/ci/run-native-metal-smoke.sh
```

The additional financial tests cover:

- Real PostgreSQL reservations raced by two independent native owner processes,
  with enough money for one VM; the loser must create no native journal or slot.
- Account-wide allowance contention between different apps on independent native
  owner processes. The loser must consume no shared allowance, journal or slot;
  physical settlement releases the winner’s reserved MB-seconds.
- Local funded expiry after the owner's real PostgreSQL pool is closed and its
  wake context is cancelled. No scheduler, meter or gateway runs in the fixture.
  Physical absence must precede the funded deadline while money and allocator
  capacity remain held until successful settlement.
- SIGKILL during persisted boot authorization, a live guest and physical
  teardown before settlement, followed by a new native owner incarnation.
- Native pidfd kill failure and database settlement failure. Recovery retains
  the exact slot even when the previous daemon already confirmed physical
  cleanup. A slot occupied by a replacement fails closed before cleanup effects.
- Settlement/recovery replay, denial replay after a policy edit, billed compute
  against pinned funded ceilings, stale residency clipping and floor waiver.
- PostgreSQL recovery from prior-month books/leases, manual versus next-period
  holds and policy edits before current-period initialization. These seed
  historical ledger state; they do not change the database or host clock.

Required financial tests are discovered from source. Missing tests, failed
tests and skipped tests (including skipped subtests under a passing parent)
fail qualification. `FAAS_SKIP_PG_TESTS` is rejected. The financial lane also
runs the store conformance suite with the race detector. The rest of the full
metal suite retains its existing specialized-fixture skip reporting.

Save `financial-compute-evidence.txt`, `financial-compute-postgres.log`,
`fcvm-metal.log`, `fcvm-metal-batch.log` and the wrapper's full output. They are
written below `FAAS_METAL_TRANSFER_ROOT`, or `/var/tmp` when unset. The evidence
records the reviewed source SHA, host, kernel, toolchain, commands and exit
statuses, including final leakcheck and service restoration. The existing
transfer-directory retention is five minutes; copy evidence into the release
review promptly. Failed recovery retains journal/image trees for repair. Short
private jail paths are under `/srv/fc/cost-*` to respect Unix socket limits;
their paths are printed on cleanup failure. Do not recursively remove retained
trees until their native resources have been retired and verified absent.

Portable verification before a native run:

```sh
go test -race -count=1 -run '^TestFinancialCompute' ./pkg/state/financialtest ./pkg/fcvm
bash scripts/ci/financial-compute-acceptance_test.sh
bash scripts/ci/run-native-metal-smoke_test.sh
```

## Remaining release checks

Two logical owners on one host exercise the shared authority and real native
retirement. They do not qualify a physical two-host network partition, the full
APID/scheduler/VMMD/meter process graph, all workload modes or performance.
Complete the following on isolated release fixtures before enabling strict
budgets in production. Apply additive migrations to those fixtures and run
matching scheduler, gateway, VMMD and meter binaries with native process
recovery and current financial owners on every active node.

1. Activate app-scoped strict gross compute `suspend_workloads`, zero drain,
   empty alert thresholds. Refuse busy apps, incomplete evidence and old owners.
2. Race cold starts across two nodes and deployments. Check all policy limits,
   fenced physical attempts and retained queued work. Other apps must continue.
3. Exercise cold/restore, paused warm pools, frozen thaw, workers, commands,
   retries and deployment changes. Unsupported qualification restore/job paths
   must refuse strict app work before physical effects.
4. Disconnect scheduler/meter/database after granting funding. Check local expiry
   and cancellation of boot in flight. Inject failed kill and failed settlement;
   retain funding/capacity, retry exact physical cleanup and settle exactly once.
5. Kill/restart vmmd during boot, live execution and teardown. Check native
   quarantine and exact-generation recovery. Stale RUNNING rows cannot bill
   past funding; Gregale absorbs unfunded tail consumption.
6. Replay acquire/renew/settle and stale incarnation messages. Test manual and
   next-period holds across UTC month reset. Disable/raise a policy and attempt
   a limit below existing funding. Preserve independent payment/security holds.
   Clean up a denied or unstarted wake without a VM journal, restart the node
   incarnation, then replay its persisted wake ID or command lease token. It
   must remain fenced before native allocation. Check the teardown pause at
   the UTC boundary before initializing the next month's funding.
   Edit a held policy before that initialization: release its own hold while
   preserving prior funding and the next acquire's accounting/retirement checks.
7. Reconcile billed compute with funded ceilings, sidecars, rounding, prices,
   corrections and synthetic floor waiver. Verify API/CLI/SDK/console balances
   and distinguish commitments from invoice spending.
8. Measure warm and cold admission latency/throughput against platform targets.

Run the full native VM lifecycle suite and leakcheck afterwards. Record host
identity, commit hashes, commands, results and resource inventory in the release
review. The current managed cloud container has no native acceptance path.

## Account compute release checks

Use an account compute-only strict `net_usage` / `suspend_workloads` policy with
zero drain and no alert thresholds. Record the monthly compute contract first;
activation requires quiet covered workloads, complete retained sampling and no
independent job definitions or queued/claimed job tasks. Keep subscription,
storage, egress, builds and tax outside the claimed guarantee.

In the actual two-host and full-daemon acceptance runs, contend different apps,
workers, deployments and command tasks for one shared allowance and paid cap.
Combine an app gross cap with the account net cap; failure of either must retain
only its own hold and consume no candidate funding in either book. Create a new
app after the account hold and verify traffic, wake, floor and command admission
remain blocked. Verify policy edits, disable/re-enable, replacement and plan
upgrades/downgrades cannot reset committed money or reissue included quantity.

Retain a previous-month physical attempt through a node crash. A new UTC month
must not authorize another app until exact old-fence physical retirement settles
both books. Verify `manual` holds persist and `next_period` holds release in the
new month; an edit releasing one policy cannot release another policy or a
payment/security hold. Job creation (ordinary, scheduled, image-run and
GitOps-created) must fail while the account strict policy is enabled.

The portable financial suite includes account contention, joint policy checks,
retained allowance, replay, unsupported producers and PostgreSQL month recovery.
These checks and the compiled native account contention fixture do not close the
physical two-host, full-daemon, workload-matrix or leak acceptance gates above.
