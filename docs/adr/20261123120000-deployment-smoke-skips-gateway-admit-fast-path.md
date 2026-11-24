# ADR-20261123120000 · A deployment smoke always reaches schedd

- **Status:** accepted.
- **Date:** 2026-11-23
- **Amends:** the production-us hunt #6 zero-traffic sibling park (`pkg/sched/pressure_park.go`).

## Context

`deployment_verification_unavailable` kept failing redeploys after the
2026-11-21 fix that lets schedd park an idle 0%-traffic sibling for a refused
deployment smoke. A prod repro on 2026-11-23 failed 1 in 4 back-to-back
redeploys of an idle `max_concurrency=1` MCP app, with no schedd log at all.

The cause is in the gateway. `PGBackend.admitSynchronous` refuses locally,
before any RPC, when its cached targets across all of the app's deployments
reach the requested maximum (`max_concurrency + RolloutConcurrencyGrant` = 2).
After two quick deploys the picker held the previous release (0% traffic,
still warm) and the live release, so every smoke of the third release was
answered at capacity by the gateway itself. schedd never saw it and never
parked the sibling. The smoke kept retrying until the old instance idled out
(froze or parked); if that took longer than the 5-minute verification window
the deploy failed.

Evidence (deployment 2873dcce): 60 refusals in 5 min, `02647478` (0%) and
`18fb8837` (live) both running, no schedd line. The next deploy (6797f9d6) was
refused 100 times from 10:55:55 and admitted at 10:57:39.26, 2.3 s after
`02647478` froze at 10:57:36.95 and left the picker.

## Decision

Admissions with `trigger = deployment.smoke` skip the gateway fast path and
always call schedd's `AdmitInstance`, which can free a slot from a 0%-traffic
sibling. Ordinary traffic keeps the cheap local refusal.

## Consequences

- One extra schedd RPC per refused smoke attempt; smokes are rare.
- schedd also logs every refused smoke (`sched: deployment smoke refused`,
  throttled), so any remaining refusal names its holders.
