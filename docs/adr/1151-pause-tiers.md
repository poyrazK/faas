# ADR-1151: Pause tiers — quiet pause between requests, frozen after idle

- Status: Proposed. Rollout starts with function apps only (owner
  decision, 2026-10-13); services follow once functions have run well in
  production. Steps 1 and 2 are built, each behind switches that are off by
  default: vmmd `quiet_pause`, schedd `quiet_pause_admission` and schedd
  `frozen_tier` (2026-10-13). Step 3 is built for request-mode apps behind
  schedd `frozen_tier_apps`, also off by default (2026-10-15). The canaries
  are pending. Amended 2026-10-27: vmmd `quiet_pause` and schedd
  `frozen_tier_apps` are on by default (see "Amendment 2026-10-27").
- Date: 2026-10-12
- Builds on: ADR-906 (frozen instances: prototype, measurements,
  economics), ADR-909 (measured admission), ADR-074 (warm pool), ADR-159
  (snapshot reuse at park)
- Amends (when accepted): spec §6.1 state machine (a `frozen` state),
  §6.2 invariant 2 (frozen charge; vCPU admission by active vCPU), §12
  metrics, ADR-906 decision 2 (how a frozen instance is charged)

## Context

ADR-906 measured a Firecracker pause: 0.4 ms to pause or resume, no host
CPU while paused, first byte 5–14 ms after resuming a restored VM, against
a 340 ms p50 snapshot wake in production. Its economics simulation replays
3,000 HTTP apps from the Azure Functions 2019 trace and found that vCPU
admission, not RAM, limits every policy. A node admits Σ guest vCPU up to
host threads × `CPUOvercommit` (160 on an EX44), and Free, Hobby and Pro
VMs expose 2 vCPUs each, so an EX44 holds at most ~80 running VMs whatever
their RAM. An idle running VM keeps its vCPUs booked for the whole idle
timeout (30–600 s) while using about 0.4% of a core.

The same simulator (`scripts/experiments/frozen-tier-economics`), extended
with a pause that keeps the instance billed and holds RAM but no vCPU, gives
(apps per EX44 at the p99.9 instant; mean latency of a request that finds
no running VM):

| policy | billed GB-h per app-month | revenue per app-month | apps per EX44 | binding limit | wakes per app-week | latency of those wakes |
|---|---|---|---|---|---|---|
| A. today: idle timeout, then park | 29.4 | €3.94 | 251 | vCPU | 1,096 restores | 340 ms |
| B. freeze at the idle timeout, park after 1 h (ADR-906 as proposed) | 29.4 | €3.94 | 251 | vCPU | 1,096, 99.1% thaws | 10.9 ms |
| C. idle running VMs hold no vCPU, no pause | 29.4 | €3.94 | 1,052 | RAM | ~1,100 restores | 340 ms |
| D. quiet pause after 2 s (billed), park at the idle timeout | 29.4 | €3.94 | 716 | vCPU | 6,901, 84% resumes | 55.7 ms |
| **E. quiet pause after 2 s (billed), freeze at the idle timeout** | **29.4** | **€3.94** | **716** | **vCPU** | **6,901, 84% resumes, 16% thaws** | **3.4 ms** |
| F. freeze after 2 s, frozen time unbilled | 9.3 | €3.83 | 716 | vCPU | 6,901, 99.8% thaws | 8.5 ms |

C shows the vCPU rule is the lever, but it only drops the booking: an idle
VM that gets busy again is never re-admitted, so the node's CPU guarantee
becomes statistical and wakes after the idle timeout still restore. E keeps
today's billing (§4.7 untouched, no revenue change), fits 2.85× the apps,
and turns almost every wake into a resume or a thaw. At 716 apps per EX44,
E's p99.9 instant has 166 active vCPU (budget 160), 424 booked vCPU for
running and quiet-paused VMs (2.65× the budget), 210 resident VMs and about
305 frozen ones (`occupancy.py` in the same directory).

The trace is all functions, but the table above uses the plan idle
timeouts. Function apps idle out after 30 s on every plan (ADR-974), which
leaves less idle time to reclaim. With `FUNCTIONS=1`:

| function apps (30 s idle timeout) | apps per EX44 | billing | wakes that restore | mean wake latency |
|---|---|---|---|---|
| today | 285 | 23.6 GB-h, €3.92 per app-month | all | 340 ms |
| quiet pause after 2 s | 716 | unchanged | 18% | 62.3 ms |
| quiet pause, then frozen at the idle timeout | 716 | unchanged | 0.1% | 3.5 ms |

Quiet pause alone carries the whole capacity gain; frozen adds the latency
win for requests that arrive after the 30 s idle timeout.

## Decision (proposed)

Two tiers built on the same Firecracker pause, with different owners.

### 1. Quiet pause: inside RUNNING, owned by vmmd, billed

- vmmd pauses a running instance after `QuietPauseAfter` (2 s) without
  activity: no forward RPC in flight (HTTP, raw, TCP and UDP streams, so an
  open WebSocket or TCP connection counts), no `waitUntil` tail task and no
  exec session. vmmd already tracks forwarded requests (`pkg/fcvm/activity`)
  and tail tasks (the Manager's tail counters), and all four forward RPCs
  pass through `Server.beginActivity`.
- `beginActivity` resumes a paused instance before the bridge dials the
  guest, once per instance however many requests arrive together. ADR-906
  measured +1–3 ms p50 for a request that finds the VM paused.
- No memory reclaim. Liveness, readiness and framework-ready loops skip a
  paused instance instead of counting failures. vmmd's own calls into the
  guest (probes, metric scrapes, vsock queries) never resume it.
- The row stays `running`. There is no database write and no schedd round
  trip, and billing is unchanged: a quiet-paused instance is a running
  instance that is not using its vCPUs.
  (Amended 2026-10-23 by ADR-20261023171905: with meterd's
  `quiet_pause_unbilled = "on"`, a quiet-paused second is no longer billed.
  schedd records the pauses in a ledger and meterd subtracts the ones it can
  prove; the row still stays `running` and nothing changes on the request
  path.)
- vmmd reports `quiet_paused` and `quiet_paused_since` in `InstanceStats`;
  schedd uses them for admission (section 3).
- schedd decides eligibility at wake and sends it in the app spec
  (`quiet_pause_after_ms`, 0 = off). The first release covers function
  apps only (`app_type = function`), the execution model AWS Lambda uses,
  where the environment is frozen between invocations. It needs no new
  customer setting. Quiet pause is never on for workers, apps attached to a
  private network (guest-to-guest packets bypass vmmd, so a paused peer
  would drop them), mirror instances, jobs, app tasks or builders.
- What a customer sees, from ADR-906's pause experiments: in-process timers
  and background threads stop 2 s after the last request; an outbound
  connection survives, but a peer that expects heartbeats more often than
  the pause lasts may close it; after resume the guest clock is a constant
  ~75 ms behind.
- Services come later, once functions have run well in production. They
  get a declared lifecycle written by apid: `request` (paused between
  requests, the function behaviour), `idle` (runs through the idle timeout,
  then frozen; the default for services) or `always` (never paused or
  frozen). The platform cannot see in-process timers or heartbeat
  protocols from outside, so the app declares them.
  (Amended 2026-10-15, step 3 as built: the setting is `lifecycle.pause` in
  `gregale.yaml` and `pause` on the app API, stored in the app manifest that
  apid owns, with the values `between_requests` (`request`), `when_idle`
  (`idle`) and `never` (`always`); under a `lifecycle:` block, `pause:
  always` would read as the opposite of what it does. Functions default to
  `between_requests` and request-mode apps to `when_idle`. Worker, service
  and job execution modes never idle out and accept only `never`. Apps
  freeze only when schedd's `frozen_tier_apps` switch is on as well as
  `frozen_tier`, so the operator turns the tier on for functions and apps
  separately; an app that declares `between_requests` is quiet-paused
  wherever vmmd's `quiet_pause` is on, because it opted in. A thaw prefers
  the frozen instance a session-affinity cookie names: the gateway passes
  that instance to schedd as a wake hint, and schedd thaws it first when it
  is one of the deployment's frozen candidates.)

### 2. Frozen: a new instance state, owned by schedd, not billed

- At the idle timeout the reaper freezes an eligible instance instead of
  parking it, when the deployment already has a usable snapshot (ADR-159):
  RUNNING → FROZEN. Without one the reaper parks as today, which writes the
  snapshot, and the instance is frozen at a later idle timeout. (Amended
  2026-10-13: the draft also froze after a keep-paused `PauseAndSnapshot`,
  SNAPSHOTTING → FROZEN. vmmd refuses keep-alive snapshots under native
  process recovery, and the first idle period after a deploy is the only
  one without a snapshot, so the extra RPC bought little.)
- State machine: `frozen` joins the `instances.state` CHECK. Edges: RUNNING
  → FROZEN; FROZEN → RUNNING (thaw); FROZEN → PARKED (TTL, memory pressure,
  superseded deployment, changed config fence, failed thaw, node or vmmd
  restart); FROZEN → STOPPED, FAILED, EVICTING_ACCOUNT_DELETING.
  `CountsForConcurrency` is false and `CountsForRAM` is true. `State.Billable`
  is `CountsForRAM` less FROZEN. In SQL and Go state lists, FROZEN goes
  wherever WARM goes (resident listings, layer-artifact and secret-runtime
  rosters, publication proofs), except billing, the `ram_mb + 8` node-usage
  sums and the CPU sets.
- Billing: `frozen` stays out of the billable set in the
  `instance_billing_intervals` trigger (ADR-906 decision 4).
- Admission: a frozen reservation (`KindFrozen`) holds the instance's
  measured memory, at least `MeasuredChargeFloorMB` (64 MB), and no vCPU,
  CPU millicores or concurrency. It is outside ADR-909's reservation
  overcommit cap. A thaw re-admits the full reservation (plan RAM + 8 MB,
  vCPU, CPU, concurrency) the way a wake is admitted; `PromoteWarm` only
  adds concurrency because a warm VM keeps its full booking. If it does not fit,
  schedd destroys the frozen VM (FROZEN → PARKED) and the request takes an
  ordinary wake, on another node if needed.
- Thaw path: gateway → schedd `Wake` → `promoteFrozenInstanceLocked`, the
  warm-pool promotion generalised: config-fence check, ledger re-admission,
  vmmd `Thaw` (resume and restart probes), `PublishOwnedInstanceRuntime`
  (FROZEN → RUNNING compare-and-set), route publication. No resume hook
  (ADR-906 decision 5), except for an app that declares `after_restore`.
  (Amended 2026-10-15: such an app reconnects its clients after sleeping, so
  the thaw runs the resume hook and its callback. `ThawInstance` carries
  `run_after_restore`; a vmmd that does not confirm it in
  `after_restore_ran` fails the thaw, and the request restores from the
  snapshot, whose restore runs the hook.)
- Service calls (amended 2026-10-15): the internal HTTP and TCP service
  proxies treat vmmd's `instance_frozen` refusal like the public gateway:
  the replica leaves the proxy's endpoint cache without a circuit-breaker
  failure, the target is woken (which thaws it), and the call is retried
  once on the thawed replica. vmmd refuses before it dials the guest, so
  the retry sits outside the caller's retry policy; a request body that
  cannot be replayed is not retried.
- Bounds: `FrozenTTL` (10 min), then park. Under memory pressure (ADR-909
  eviction) frozen instances go first, least recently used, before any
  running one. `FrozenMaxPerNode` caps Firecracker processes and jail uids.
  A host restart loses frozen VMs; recovery marks their rows parked, since
  snapshots stay the source of truth (ADR-005). Memory reclaim and zswap
  apply only under pressure, as ADR-906's density results recommend.
- Eligibility: whatever an idle park would take today, except instances
  with ephemeral secrets (the warm pool discards those because a resumed VM
  would outlive its credentials), mirror instances and ended or superseded
  deployments.

### 3. Admission counts active vCPU

- The vCPU gate counts active vCPU: Σ guest vCPU of waking, cold-booting
  and running instances that are not quiet-paused, from the newest stats,
  plus every reservation admitted since. This mirrors ADR-909's
  `MeasuredMemory` with an `ActiveVCPU(nodeID) (vcpu, asOf, ok)` source;
  without fresh stats the gate falls back to today's bookings. The physical
  CPU-millicore gate follows the same rule.
- Booked vCPU for running and quiet-paused instances is capped at
  `QuietPauseMaxVCPUOvercommitPercent` (300%) of the budget. Frozen
  instances book none.
- A quiet resume never waits for admission. When many paused instances
  resume together, active vCPU can exceed the budget until they go quiet
  again. The cost is CPU contention, which raises latency and does not fail
  requests; the 300% cap bounds it, and CPU pressure (PSI) alerts on it.
- Rejected for now: a vmmd-held token per active vCPU. It would make a
  resume wait for capacity, moving admission into vmmd and putting a queue
  on the request path.

### 4. Ownership stays as it is

vmmd is still the only component touching Firecracker, and quiet pause is
decided inside vmmd from its own counters. schedd is still the only writer
of `instances`: it owns `frozen`, the TTL, eviction, thaw admission and
eligibility. gatewayd-internal keeps its hot path; a frozen instance is
unroutable like a parked one and wakes through schedd. meterd is unchanged,
because billing follows instance states through the trigger. apid will
write the services' lifecycle setting when services are added.

## Limits (`pkg/api/limits.go`)

| name | value |
|---|---|
| `QuietPauseAfter` | 2 s |
| `QuietPauseMaxVCPUOvercommitPercent` | 300 |
| `FrozenTTL` | 10 min |
| `FrozenMaxPerNode` | 1,000 |
| frozen charge floor | `MeasuredChargeFloorMB` (64 MB, existing) |

## Metrics (§12 additions)

`vmmd_quiet_pause_total{outcome}`, `vmmd_quiet_resume_seconds`,
`vmmd_quiet_paused_instances`, `vmmd_freeze_seconds`, `vmmd_thaw_seconds`,
`node_frozen_instances`, `node_frozen_resident_bytes`,
`schedd_frozen_instances`, `schedd_frozen_total{outcome}`,
`schedd_frozen_parks_total{reason}` and `schedd_frozen_thaw_total{outcome}`.
(Amended 2026-10-13: schedd metrics are prefixed `schedd_`, as every schedd
series is. A thaw answers the gateway as a restore, as a warm-pool resume
does; `gateway_wake_latency_seconds` stays unlabeled because ADR-092 pins it
as a §12 contract, and thaw latency is `vmmd_thaw_seconds`.) The canary
watches them on `deploy/grafana/frozen-tier.json`; the `frozen_tier` alert
family covers failing and slow thaws, freeze errors, memory-pressure parks,
the per-node cap and quiet resume errors (`docs/runbooks/FaasFrozenTier.md`).

## Rollout

One PR each. PRs that touch the VM lifecycle run `test-metal` and
`leakcheck` on the n2 node.

Step 1, quiet pause for function apps:

1. This ADR.
2. vmmd quiet pause behind `quiet_pause` in `vmmd.toml` (off by
   default): pause controller, resume in `beginActivity`, probe skipping,
   stats fields. Metal test: 1,000 pause/resume cycles under forwarded load,
   an open stream is never paused, a paused VM snapshots and destroys
   cleanly, no leaks.
3. schedd eligibility for function apps in the app spec.
4. schedd active-vCPU admission and the 300% cap, with a property test for
   the vCPU side of invariant 2.
5. Canary on one production node with the flag on, comparing resume
   latency, errors and CPU pressure against the other nodes; then on for
   every node.
   (Amended 2026-10-26: before the canary, run the node with vmmd's
   `quiet_pause_observe` instead. Observe mode runs the same pause pass,
   pauses nothing, and records the pauses quiet pause would make:
   `vmmd_quiet_pause_observed_total`, `vmmd_quiet_pause_observed_pause_seconds`,
   `vmmd_quiet_pause_observed_mb_seconds_total{plan}` and
   `vmmd_quiet_pause_observed_instances`. They measure the canary's
   expected pauses and resumes, and the billable MB-seconds unbilled quiet
   pause (ADR-20261023171905) would not bill, on real traffic instead of the
   simulated trace. Probes, admission and the pause ledger see an observed
   instance as running.)

Step 2, frozen for function apps. Built behind schedd's `frozen_tier`
switch, off by default (owner decision, 2026-10-13: build before the step 1
canary; turn it on only after step 1 has run well in production):

6. `frozen` state: migration (CHECK, indexes; the billing trigger already
   leaves it out), `machine.go` edges, `State.Billable`, store conformance
   tests.
7. vmmd `FreezeInstance`/`ThawInstance` RPCs from the ADR-906 prototype,
   with a frozen flag vmmd enforces: forwards to a frozen VM fail fast,
   quiet pause and snapshots skip it, reclaim only on request.
8. schedd: freeze instead of park at the idle timeout, TTL, eviction order,
   thaw promotion, recovery after a node or vmmd restart, metrics.
9. Docs and billing tests (frozen unbilled, quiet-paused billed); canary as
   in step 1.

Step 3, services: the declared lifecycle (`request`, `idle`, `always`)
through apid, the CLI and docs, then the same canary. (Built 2026-10-15 as
`lifecycle.pause` with schedd's `frozen_tier_apps` switch; see the
amendment under "Decision".)

## Consequences

- Rolling back. Turning `frozen_tier` off stops new freezes; frozen
  instances still thaw for requests and park after `FrozenTTL`. Roll schedd
  back to a release without the `frozen` state only after
  `schedd_frozen_instances` reads 0: an older schedd neither thaws nor parks
  a frozen row. The migration's down step parks any frozen rows left.
- Hosts with the `zswap` role (ADR-906 decision 6) compress frozen VMs under
  memory pressure instead of parking them; a compressed VM thaws more
  slowly, as its pages fault back in, but still skips a snapshot restore.
- Turning the tier on needs vmmd with `FreezeInstance` first. An older vmmd
  answers Unimplemented and the instance parks as before.
- In the simulation, a request that finds no running VM waits 3.4 ms on
  average instead of 340 ms, vCPU-bound nodes hold 2.85× the apps, and
  billing and revenue do not change.
- Function apps lose in-process timers between requests after 2 s, as on
  AWS Lambda. The function docs state it.
- A burst of resumes can oversubscribe CPU for a while. The guarantee is
  soft, bounded by the 300% cap and watched through PSI.
- More resident Firecracker processes: about 515 per EX44 at 716 apps
  (~2 MB each), within `FrozenMaxPerNode` and the 10,000 jail uids.
- Snapshot, migration and destroy must all handle a paused VM, and
  migration resumes a quiet-paused VM first.

## Open questions

1. Frozen TTL: decided 10 min (2026-10-13). The simulation gives the same
   capacity as 1 h and 11.0 against 10.9 ms mean thaw-or-restore latency,
   while holding fewer resident VMs.
2. Is a soft CPU guarantee for quiet resumes acceptable, or should resumes
   wait for a vCPU token? The step 1 canary measures how far active vCPU
   exceeds the budget in practice.

## Amendment 2026-10-27: quiet pause and frozen apps on by default

Production measurements on rc.297, 2026-10-27, with the switches on in
compute-1's (fsn-2) `vmmd.toml` and `schedd.toml` (frozen tier itself on
since 2026-10-22 through a schedd drop-in on both compute nodes):

- `quiet_pause`: `h10-fn-go` (a function, so `between_requests`) at one
  request per 5 s paused 27 times in 120 s, resume mean 1.1 ms and p90
  2.4 ms, no resume errors; client p50 196 ms against a 250 ms baseline.
  Apps default to `pause: when_idle` and were not paused. Quiet pause was
  then turned on on compute-2 (fsn-3) as well.
- `frozen_tier_apps`: request-mode apps thawed in 6-12 ms on the vmmd side;
  platform wake mean 108 ms for thaws against 539 ms for snapshot restores
  (vmmd restore 225-241 ms). Across a vmmd restart a freeze fails and the
  instance parks instead, as designed.

vmmd `quiet_pause` and schedd `frozen_tier_apps` now default to on
(`cmd/vmmd/config.go`, `cmd/schedd/config.go`). `frozen_tier_apps` still does
nothing unless `frozen_tier` is on, and `frozen_tier` stays off by default.
`quiet_pause_admission`, `frozen_tier_workers` and early freeze
(ADR-20261021215353) stay off. Because quiet pause wins over
`quiet_pause_observe`, observing a node now needs `quiet_pause = false`.

Billing is unchanged: a quiet-paused instance stays RUNNING and billed.
schedd `quiet_pause_ledger` and meterd `quiet_pause_unbilled`
(ADR-20261023171905) stay off by default; turning them on changes what a
billed second is and remains a separate decision after the financial-model
re-check that ADR names.

Why the binary and not the node: release rollouts run `gregalectl manifest
render` on every compute node (`deploy/ansible/node_join.yml`), which rewrites
`/etc/faas/vmmd.toml` and `/etc/faas/schedd.toml` from the manifest. A
switch set by hand in those files is lost at the next rollout and is never
present on a new node; a default in the binary reaches both the same way.
