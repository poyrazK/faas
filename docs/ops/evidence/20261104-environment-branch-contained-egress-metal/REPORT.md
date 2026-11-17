# Contained environment branch egress on metal, 2026-11-04

ADR-20261031193635 slice 6a. Both runs used `gregale-internal-test-1` (GCP n2,
2 vCPU, KVM, cgroups v2) with the native harness (`/mnt/e2e-scratch/e2e-run.sh`,
shared acceptance lock, leakcheck before and after).

## `TestEnvironmentBranchContainedEgressMetal` — PASS

This run used branch `feat/environment-branches` at `0bfa68b351`. The test took
580 s, and leakcheck was clean before and after. apid ran with
`FAAS_ENVIRONMENT_BRANCH_CONTAINMENT=1`.

1. A source-deployed Node app serves `/probe`. The probe opens TCP to
   `1.1.1.1:443` and resolves a name through the platform resolver.
2. Control: production's guest answers `public=connected dns=ok`.
3. `POST .../branches` with `egress: contained` returns 201, and the branch
   answers `egress: contained`.
4. The branch's first request cold-boots, and the same build answers
   `public=blocked dns=ok`. Ingress works, the bridge resolver works, and public
   TCP is dropped. schedd accepted the runtime, so vmmd acknowledged the
   contained profile.

The first run, at `f09c2519b9`, failed in the harness before any containment
check: the production control request had no API key, and a project app
requires one. In the same run, `TestEnvironmentBranchIdentityReloadMetal`
passed (550 s). That pass re-proves slice 5 after unexporting the Lease restore
identity, which the native launch journal had refused as `null`.

## Packet test `pkg/netns` (metal tag, root)

| Test | Result |
|---|---|
| `TestMetalEnvironmentBranchContainedProfile` | PASS (15 s). Public TCP and a non-branch bridge port are dropped. The service proxy and the resolver stay reachable. The branch database opens only once its pair enters `branch_db`. The profile holds after an ordinary reset and a re-render. |
| `TestMetalTenantEgressPolicyEnforced` | PASS |
| `TestMetalRehearsalNetworkGuardEnforced` | FAIL at its IPv6 control probe (`2001:db8:20::1 is unreachable`), before any guard is installed. On this host the IPv6 topology is unavailable. The branch profile emits no rules for a non-branch config. |
