# ADR-643 · A wake refused for fleet capacity parks an idle instance

- **Status:** proposed
- **Date:** 2026-10-07
- **Amends:** spec §4.3 and §5 (eviction), ADR-193 (per-node admission).
- **Decision:**
  - **Trigger.** A gateway wake (`Engine.Wake` or `Engine.AdmitInstance`
    with trigger `gateway`) that schedd refuses with `capacity_unavailable`
    parks one idle instance of another app, then retries the wake once.
    Floors, scale-up, cron and other background wakes never park another
    app's instance.
  - **Candidates.** Every reaper tick publishes candidates from its own
    snapshot. The rules match idle reaping:
    - running instances of apps this schedd owns;
    - idle for at least 30 s (`PressureParkIdle`) and older than
      `MinInstanceAge`;
    - no in-flight requests, open connections or log tails;
    - never the last instances under an app's floor;
    - never workers, services, jobs, mirrors or `reserved` eviction priority.

    Order: least recently used first, Scale plan last. A refused wake
    prefers a candidate at least as large as its own RAM.
  - **Safety.**
    - The candidate list is used for at most 30 s.
    - A candidate is claimed once.
    - The durable row is re-read before parking, so a request that
      landed since the tick keeps the instance.
    - One refused wake tries at most three candidates and parks at most
      one instance.
  - **Mechanism.** Parking is `Engine.Park`: the victim is snapshotted and
    its next wake restores (ADR-005). It is counted as
    `schedd_eviction_fired_total{reason="wake_pressure"}`.
- **Why:** H5-32, from production-us hunt #5 (rc.245).
  - **Symptom:** 15 parked apps were woken at once on the two-node fleet
    (2 × 32 vCPU at 4 vCPU per instance, 16 slots in total). 9 were refused
    `503`, and every other parked app stayed refused until the reaper's idle
    timeout parked something: up to 600 s on Scale.
  - **Eviction never fires here:** RAM-pressure eviction compares a schedd's
    whole-ledger RAM with 80% of the single-box 47,600 MB ceiling. The
    fleet's per-node ceilings are 8,593 MB, so the threshold is never
    reached.
  - **CPU binds first:** the per-node vCPU and host-CPU budgets fill before
    RAM for small apps, and no pressure path considers them.
  - Making eviction per-node at 80% RAM was rejected below. Parking on
    demand frees exactly the slot a waiting request needs and keeps idle
    instances warm until then.
- **Consequences:**
  - A parked app's wake under full capacity costs one park (about
    150–500 ms) before its own restore, instead of a `503`.
  - The parked app's next request pays a restore (median ~300 ms).
  - Candidates are per-owner: a schedd parks only instances of the apps it
    owns. On a fleet whose ownership is skewed, a wake owned by the smaller
    shard may still be refused. Cross-owner parking would need a peer RPC
    and is not part of this decision.
  - Under a burst, each refused wake parks at most one instance, so
    parking is bounded by the number of refused wakes.
- **Rejected alternatives:**
  - **Per-node RAM-pressure eviction at 80% of the node ceiling.** It keeps
    a fixed fifth of every node empty even when nobody is waiting, does not
    see CPU pressure, and `Evict` destroys the VM, so the evicted app's next
    wake cold-boots.
  - **Shorter idle timeouts under pressure.** These still refuse the
    requests that arrive before the next reaper tick.
- **Verification:**
  - `TestGatewayWakeParksAnIdleInstanceWhenTheNodeIsFull` fills a node's
    vCPU budget around an idle instance and checks two things: a background
    wake is still refused, and a gateway wake parks the instance and places.
  - `TestSelectPressureParkCandidates*` pins the exemptions, floors and
    ordering.
  - Production: repeat the 15-app burst after rollout. The `wake_pressure`
    counter rises and refused wakes fall.

## Amendment — zero-traffic siblings (2026-10-08, hunt #6, H5-59)

An admission refused at the app's own concurrency cap (any trigger) may park
one idle instance of the same app whose live deployment receives 0% weighted
traffic, then retries once. A rollout moves the previous deployment of a
traffic split to 0%, but its warm instance kept a slot until its idle timeout
(600 s on Scale). On production-us that instance and the serving one filled
`max_concurrency` plus the ADR-199 rollout grant, every smoke wake of the new
candidate was refused with 429, and the deploy failed as "verification
unavailable". The requested deployment, deployments with traffic, deployments
with their own floor, non-live deployments (rollout candidates), non-normal
modes, and instances younger than `MinInstanceAge` or active within
`PressureParkIdle` are never taken. Parking snapshots the instance, so an
exact revision or preview request still restores it. The counter is
`schedd_eviction_fired_total{reason="zero_traffic"}`.
