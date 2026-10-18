# Function migration recovery after scheduler process loss

Status: implementation for qualification; native execution pending; production off.

This increment adds [ADR-1353](../../../adr/1665-function-migration-scheduler-restart.md)
startup recovery and extends the guarded native migration fixture. No production
capability, deployment, automatic invocation retry or VM reattachment is enabled.

## Recovery behavior

Before ledger seeding and admission, schedd discovers its destination node's
durable ready and committed handoffs through a keyset-paginated sqlc query.
Source cleanup may remove earlier entries while a later page is being read;
the cursor advances past failed entries. The startup pass uses the existing
commit-recovery time budget, which can defer later work. Failed or unfinished work retains durable ownership
and capacity for the existing expiry reconciler.

A ready handoff uses the source's persisted configuration receipt and the exact
recorded destination wake, network identity and measured receipt. The existing
ownership transaction rechecks current inputs, source lease and ownership,
artifact/policy authority and target incarnation. A commit error resolves against
that exact durable decision. A committed handoff repeats only activation and the
original lease-bound source acknowledgement; it never rewrites ownership or
allocates another wake. No restore or application lifecycle callback is requested
by startup recovery. Issued, expired or stale uncommitted attempts retain the
existing fenced rollback path.

## Native fixture scope

Both Node 22 and Node 24 must execute the original four baseline/reply cases and
two new coordinator process-loss cases, for twelve runtime/scenario pairs. The
fixture SIGKILLs only its own migration coordinator child after readiness or
after the real PostgreSQL ownership commit, before activation. A replacement
child receives only its role and destination node. It must discover the attempt
through a new PostgreSQL pool and RPC clients, run production startup recovery,
then seed a fresh ledger.

Version 3 evidence requires actual signal termination, distinct observed process
generations, the same qualified executable hash, the expected durable crash
boundary and retained cleanup authority. The destination must retain its native
process/receipt and all eight TLS SQL sessions, with unchanged lifecycle callback
counts and original SQL rows. Source retirement, final app retirement and both
physical host leak checks remain mandatory. Private child logs are retained for
failure diagnosis. Older baseline/reply-only reports cannot qualify this scope.

These cases kill a fixture process running the production coordinator and
startup methods. Prepared systemd schedd/vmmd services remain running. They do
not qualify a real service restart, host reboot, vmmd recovery, HA or PostgreSQL
failover. See the [operator runbook](../../function-migration-native-acceptance.md).

## Verification

- Real PostgreSQL race checks passed for ready/committed startup recovery, a
  controlled error after the real startup ownership commit, changed
  configuration and changed target incarnation. The latter two cases retain
  source ownership/cleanup authority and perform no activation or retirement
  RPC. The VM RPC peer in these composition tests is simulated.
- Durable discovery, expired readiness, committed cleanup after lease expiry,
  cursor/node isolation, owned result copies and pending RAM capacity through a
  fresh PostgreSQL pool passed. These tests do not establish native pressure
  acceptance.
- Normal-build migration/runtime-configuration race checks passed. Two existing
  tests explicitly require `NativeQualificationBuild`; both subsequently passed
  under `metal,function_background_native`. The tagged portable suite passed
  with no skips. These simulated host tests supply no native execution evidence.
- Linux subprocess SIGKILL/replacement protocol, rejection of incomplete or
  replayed reports, persistent callback counts and real Node TLS/SCRAM PostgreSQL
  lifecycle checks passed. Scheduler qualification-dispatch checks also passed.
- Five Python fixture guard tests, workflow actionlint, sqlc regeneration,
  docs-links and its four live-docs tests, ADR uniqueness, sealed-environment
  scope, runbook SQL, shell quoting and text encoding checks passed.
- Scoped qualification-tagged lint reports zero issues. After fixing helper
  cleanup diagnostics, its complete portable race suite passed without skips and
  the complete native e2e executable was freshly recompiled.
- The complete race-enabled native e2e executable compiled. Exact suite listing,
  prerequisite refusal and rejection of an incorrect test selection were checked.
  No native migration body executed beyond its prerequisite refusal.

See [portable results](portable-results.json) and
[native prerequisite attempts](native-checks.json). Commands, final source/log
hashes and cleanup facts are recorded in [verification](verification.json).

## Limits and cleanup

This workspace has UID 1000, no `/dev/kvm`, no designated native host marker and
no prepared two-host configuration. The strict native entry point rejected those
prerequisites. The required scoped `make test-metal` attempt failed on the absent
staged kernel; `make leakcheck` could not inspect the container's listed native
loop devices. Neither attempt supplies passing native acceptance.

The task's disposable SQL containers/volumes, credentials, certificates and
temporary native executable were removed. Work remains local; no push, PR,
merge, deployment or automation change was performed.

Execution of all twelve cases on the designated native hosts remains pending.
Actual systemd service restart, host/vmmd death, real transport loss, probe
failures, cancellation/expiry races, native capacity pressure, private-environment
authority and native leak acceptance remain unqualified. Complete failure-matrix
qualification and production readiness remain false.
