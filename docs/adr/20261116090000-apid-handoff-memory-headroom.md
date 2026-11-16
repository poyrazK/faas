# ADR-20261116090000: apid hand-off headroom: MemoryMax 1 GiB

- **Status:** proposed
- **Date:** 2026-11-16
- **Amends:** ADR 20261018132058 (apid memory for two-generation reloads), decision 1
- **Related:** ADR-1736 (zero-downtime daemon restarts), ADR-068 (apid socket activation)

## Context

ADR 20261018132058 set apid's `MemoryMax` to 512 MB and kept the hand-off guard
at 45 % of it, 230 MB, "above every steady-state run measured" in October.

Two of the last three production releases failed the control-plane rollout
gate because apid restarted instead of handing off:

| Release | deployctl | Why | API gate |
|---|---|---|---|
| rc.344 (2026-11-15) | restart | `hand-off refused: cgroup uses 244314112 of 536870912 bytes; a second generation needs it below 45%` | 3 consecutive 3 s timeouts, failed |
| rc.345 | restart | `99-faas-dev-patch.conf` changed (#7361 turned `FAAS_DEV_PATCH_DELIVERY` on by default) | 3 consecutive 3 s timeouts, failed |
| rc.346 | hand-off | | 45/45 samples ok |

rc.345's restart was a one-time environment change, which a hand-off cannot
apply. rc.344's was not: apid's cgroup sat at 244 MB after the 60 s memory wait,
so the guard now falls inside apid's working range and a hand-off depends on
where apid happens to be when the deploy arrives. Each restart cost about 9 s
in which the API socket accepted connections but answered none; the rollout
gate tolerates two consecutive timeouts.

## Decision

1. `api.APIDMemoryMaxMB` rises from 512 to 1024. The hand-off guard stays at
   45 %, now 460 MB: room for a predecessor at nearly twice the rc.344 reading
   plus a successor spiking like the largest observed run (257 MB).
2. `GOMEMLIMIT` stays 256 MiB per process (`api.APIDGoMemLimitMB`), so the
   collector still holds each generation near its old steady state; the
   larger cap is headroom for two generations, not a larger target.
3. The §13 ledger entry for apid (384 MB) is unchanged: it describes expected
   use, which `GOMEMLIMIT` bounds. Only the worst case grows.

## Consequences

- A deploy hands apid off unless the cgroup is above 460 MB after the 60 s
  wait, instead of above 230 MB.
- Worst case during a reload: two generations above their soft limits can now
  reach 1 GiB before the kernel kills one (`OOMPolicy=continue` keeps the
  other). On a single-box install that worst case takes 640 MB more from the
  control-plane slice's 2,176 MB slack, below the guaranteed builder VM's
  2,056 MB, for as long as the spike lasts. Production is split-box: builders
  run on compute nodes and the control plane's 3 GiB slice peaked at 913 MB.
- The unit file changes, so the first deploy of this release restarts apid
  (deployctl hands off only a unit unchanged since it started). Later deploys
  hand off.
- An environment drop-in change still restarts apid; making a restart answer
  sooner than ~9 s is separate work.
