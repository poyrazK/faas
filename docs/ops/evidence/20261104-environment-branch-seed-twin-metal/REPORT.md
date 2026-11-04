# Rehearsal twin seeded from a simulated environment branch on metal, 2026-11-04

This report covers ADR-20261031193635 slice 6b on `gregale-internal-test-1` (GCP
n2, 2 vCPU, KVM, cgroups v2). It used the native harness
(`/mnt/e2e-scratch/e2e-run.sh`): one build of `feat/environment-branches` at
`fac734d79b`, the shared acceptance lock, and leakcheck before and after.

## `TestEnvironmentBranchSeedTwinMetal` — PASS (873 s)

apid ran with `FAAS_ENVIRONMENT_BRANCH_CONTAINMENT=1`,
`FAAS_REHEARSAL_NETWORK_CONTAINMENT=1` and `FAAS_REHEARSAL_ACTION_SIMULATION=1`.

1. A source-deployed Node app goes live in production with `APP_COLOR=production`.
2. `POST .../branches` with `egress: simulated` returns 201. The branch
   deployment goes live, and the branch scope gets `APP_COLOR=twin-of-sim-1`.
3. `PUT /v1/dev/sessions/twin-web` creates the run's developer session.
   `POST /v1/dev/sessions/twin-web/seed` returns 202.
4. The twin's deployment goes live after one ordinary boot. It has no `builds`
   row, and its `rootfs_key` equals the branch deployment's. The twin then serves
   `color=twin-of-sim-1`: the branch's build, with the branch's values.

The run's containment and simulation follow the twin's own app, as for any
rehearsal member. The rehearsal native acceptance
(`scripts/ci/run-native-rehearsal-acceptance.sh`) covers that machinery, and
this slice does not change it.

## Regression: `TestEnvironmentBranchContainedEgressMetal` — PASS (1065 s)

## Note on the first attempt

The first attempt failed in both tests before reaching slice 6b code. The host
had load average 4 on 2 vCPU and 38 sessions. The parent's builder VM took
731 s, and the seed test's parent missed the 15-minute deploy ceiling by
seconds. The contained test's production control request got 503
`traffic_policy_unavailable`. A re-run of the same binaries passed both tests.
