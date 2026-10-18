# Guarded two-host function migration harness

Date: 2026-10-17. Parent revision: `531ff85827d6dbdf452a91fbd530e567b62ec66c`.
Scope: first successful-migration slice of
[ADR-1352](../../../adr/1664-function-migration-destination.md).

The harness and portable rejection tests are implemented. No native migration
was executed, no native acceptance is claimed and production remains off.

## Implemented scope

`make test-function-migration-native` targets two prepared physical native
x86_64 KVM hosts. It requires clean committed source, an explicitly scoped
disposable catalog with verified TLS, root-owned host markers, distinct physical
host/boot identities and exact fresh qualified daemon/guest/runner hashes.
The suite uses real VM RPCs, the production scheduler spec builder and migration
coordinator, measured producer/consumption receipts, independent PostgreSQL
backend identities and SSH process/network-namespace witnesses.

For each of Node 22 and 24, it holds checkpoint and recovery ACKs around eight
SQL sessions in four interpreters, checks one pool generation increase, observes
closed execution and retained source ownership before commit, verifies the
committed destination and original rows, and confirms source and final app
retirement. Synthetic portable documents never count as native evidence. Missing
or skipped cases, stale scope and incomplete cleanup prevent qualification.

See the [operator runbook](../../function-migration-native-acceptance.md) for the
prepared fixture, exact build flags and evidence contract. The suite covers
successful moves only. Its reports always leave the failure matrix and
production readiness unqualified.

## Portable verification

- Race tests passed for evidence rejection, strict configuration, authenticated
  phase barriers and independent SQL session observation. The real Node adapter
  and TLS/SCRAM PostgreSQL test passed, including four loaded interpreters, two
  pools per interpreter, repeated drainage/recovery and interrupted worker work.
  All selected tests executed; none skipped.
- Worker witnesses accept cold-boot generation zero and wait for both concurrent
  pool callbacks to finish before sampling. The real Node test checks generation
  values across two recovery cycles; the test peer invokes no extra callback.
- Five portable Python guard tests passed, covering unsafe fixture scope, exact
  verified-TLS catalog selection, failed/skipped/missing cases, stale report
  scope and unprivileged coordinator rejection.
- Scoped qualification-tag lint passed with zero issues across the new native
  suite, evidence/control helpers and changed runner tests. Workflow actionlint,
  documentation links/live-docs tests, ADR uniqueness, sealed-environment scope,
  runbook SQL, shell quoting and text encoding checks passed.
- The complete race-enabled native e2e test executable compiled with
  `metal,function_background_native`; the new suite was present. Its strict
  selection guard rejected an unrelated suite. Compilation/listing supply no
  KVM or migration evidence.

[Portable results](portable-results.json), [native prerequisite attempts](native-checks.json)
and [verification hashes](verification.json) record the exact observations.

## Native limits and cleanup

The managed workspace runs as UID 1000, has no `/dev/kvm`, no native fixture
marker and no prepared two-host configuration. The strict native target and
direct suite guard refused execution. The required scoped `make test-metal`
attempt failed on the missing staged kernel. `make leakcheck` failed because
the container cannot inspect its listed native loop devices; this is not a clean
native leak verdict. These outcomes must not be relabelled as passing acceptance.

The disposable SQL containers, their volumes, generated fixture credentials and
certificates were removed. The temporary native executable was removed. No
service configuration or production capability was activated. Work remains
local; no push, PR, merge or deployment was performed for this increment.

Actual Node 22/24 cross-host success, lost ACKs, failed probes, cancellation and
expiry/commit races, capacity pressure, host/daemon death and restart,
private-environment authority and native leak acceptance remain required before
release. Unknown physical ownership continues to require quarantine/recovery.
