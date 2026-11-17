# Environment branches on metal after merging main, 2026-11-11

This report covers ADR-20261031193635 on `gregale-internal-test-1` (GCP n2, KVM, cgroups v2),
with the native harness: `feat/environment-branches` at `0ac3b7e539`, which merges
main up to `b4d03d9645`.

## The branch's own tests: all pass

| Test | Result |
|---|---|
| `TestEnvironmentBranchContainedEgressMetal` | PASS (299 s) |
| `TestEnvironmentBranchContainedObjectsMetal` | PASS (234 s) |
| `TestEnvironmentBranchIdentityReloadMetal` | PASS (237 s) |
| `TestEnvironmentBranchSeedTwinMetal` | PASS (234 s) |

Leakcheck: one empty build cgroup was there before the run and still there after
(left by an interrupted earlier run), so the run added nothing. It held no processes and
was removed. Every later group was clean before and after.

## Host fix needed first

`/var/run/faas/stream` was missing: `/var/run` is a tmpfs and the host had been rebooted.
Without it, `vmmd-stream-bridge` exits 3 (`listen` on its socket fails), and every proxied
request returned 503, production apps included. It was recreated with
`install -d -m 0700 -o root -g root /var/run/faas/stream`. A tmpfiles.d entry or harness
setup should own this directory.

## Wider VM-lifecycle tests: the failures are not in branch code

The snapshot, fork, frozen tier, managed data, private TCP and wake groups were run on the
same build. Every failure falls into one of these patterns:

| Pattern | Tests | Seen on main? |
|---|---|---|
| An image deploy stays `pending` until its deadline; imaged never logs it | CrashSnapshot, CrashSnapshotSDK, Fork, LiveFork, FrozenTier, FrozenTierApp, PrivateTCPWakeAhead, DeployWake, WakeTimeline, WorkerFrozenThaw | Yes: `TestCustomerHTTPOperationResultMetal` at main `387d9a276a` stays `pending` for 12 min |
| A source deploy's built layer is 513–514 MB, over the Hobby plan's 512 MB cap | AfterRestore, BeforeCheckpoint, SourceDeployWake | Yes: `TestAfterRestoreMetal` at main `387d9a276a`, 513 MB |
| Private TCP dial times out | PrivateServiceTCP | Not checked |
| Creating an edge returns 422 | ServiceCallInsideLeaseAfterParkWakes | Not checked |

An earlier full run at `3b1c86cf68` stopped at the harness's 60-minute test timeout after
four `CustomerHTTPOperation*` deployments sat at `pending` for 12 minutes each. The
same test fails the same way on main.

## Branch changes made during the merges

- vmmd proto: main took `AppSpec` 37–42 and `AllowResolvedEgressRequest` 5–6, so
  `identity_reload` = 43 and `environment_branch_contained` = 44. The resolved-egress
  `name` = 7 is the same field main added for sandbox sessions, and it now always carries
  the answered name.
- Branch database pairs (`branch_db`) are written in their own atomic transaction after
  main's `refreshTimedEgress`. Contained branch instances are never adopted, so they need
  no rebuild path.
- Capture boots book vCPUs and CPU with main's helpers (`appVCPUs`,
  `appCPUBookingMillicores`), because `Limits.VCPU` was removed.
