# ADR-20261023171905: Unbilled quiet pause — bill the seconds a VM runs, not the seconds it waits

Date: 2026-10-23
Status: Accepted (owner, 2026-10-23). Built behind switches that are off by
default. The billing switch stays off fleet-wide until the financial-model
re-check in "Economics" and the quiet pause canaries (ADR-1151 steps 1-2)
are done.
Amends (when accepted): spec §4.7 (what a billed second is), ADR-1151
decision 1 ("billing is unchanged"), ADR-20261021215353 (early freeze stops
being the billing mechanism; its rejected alternative is taken up here)
Related: [ADR-1151](1151-pause-tiers.md) (pause tiers),
[ADR-20261021215353](20261021215353-early-freeze.md) (early freeze),
[ADR-906](906-frozen-instances.md) (frozen instances, economics simulator),
[ADR-790](790-meterd-closed-minute-catch-up.md) (closed-minute catch-up),
[ADR-060](060-meterd-gbh-floor.md) (GB-h floor),
[ADR-1430](1430-plan-entitlements-from-october-2026-cogs-report.md) (plans, USD)

## Context

ADR-1151 built two pause tiers on the same Firecracker pause:

| | quiet pause | frozen |
|---|---|---|
| decided by | vmmd, after `QuietPauseAfter` (2 s) without activity | schedd's reaper, at the idle timeout (or, with early freeze, after 2-300 s quiet) |
| row state | stays `running` | `running` → `frozen` → `running` |
| database writes | none | a state compare-and-set each way, a billing-interval row each way, the instance triggers |
| resume | 1-3 ms in vmmd, on the request path, no round trip | thaw through schedd: p50 53 ms, p90 89 ms on production-us rc.290 with the fast path |
| vCPU | not counted as active; booked, capped at 300% of the budget | none booked |
| billed | **yes** | no |

Early freeze (ADR-20261021215353) exists only to stop billing soon after the
last request. Policy F in ADR-1151's simulation, "freeze after 2 s, frozen
time unbilled", cut billed GB-h per function app-month from 29.4 to 9.3 at
the same density, 716 apps per EX44. Competing serverless platforms bill
memory only while a request is in flight.

Early freeze gets there by turning every request after a quiet period into a
thaw, plus a freeze a few seconds later:

- **The control plane is on every request.** Each thaw is a schedd round trip
  and a guarded update on the control-plane Postgres; each freeze is another
  update. Measured on PostgreSQL 16 (2026-10-23), the `instances` triggers
  alone take about 1-1.4 ms per freeze or thaw on a warm backend and 20×
  that on a fresh one. 1,000 apps at one request a minute is ~33 state
  changes a second on today's 2-vCPU control-plane database, plus a billing
  interval opened and closed per request.
- **The first request after a pause is slower.** A thaw is 53 ms at p50; the
  quiet resume it replaces is 1-3 ms.
- **The delay is coarse.** The reaper ticks every 10 s (`pkg/sched/loop.go`),
  so "after 2 s" freezes 2-12 s after the last request, and each quiet
  period is billed for that long.

Quiet pause already stops the VM 2 s after the last request, inside vmmd,
with no database write. The only thing it does not stop is billing. The
early-freeze ADR rejected unbilling it because metering would need
per-interval pause accounting, and an unbilled VM would keep its vCPU
booking. This ADR supplies that accounting. The vCPU booking was already made
safe by ADR-1151 section 3: admission counts only active vCPU, and booked
vCPU for running and quiet-paused instances is capped at 300%.

## Decision

### 1. A billed second is a running second the VM is not quiet-paused

§4.7 keeps its rule: plan RAM + 8 MB per running second, never sampled RSS.
A second in which vmmd holds the instance quiet-paused is not a running
second, exactly as a frozen second is not. The instance still holds its RAM
reservation and stays routable; the platform carries that RAM unbilled, as it
already does for frozen instances.

- Scope is whatever vmmd quiet-pauses today: function apps, and apps that
  declare `lifecycle.pause: between_requests`. Workers, services, jobs, app
  tasks, mirrors and private-network apps are never quiet-paused, so nothing
  changes for them.
- An app with a `--min` floor keeps paying for its floor. ADR-060's floor
  fill runs after the subtraction, so seconds a floor instance spends paused
  are charged as floor capacity, as the floor promises.
- Anything that keeps a VM from pausing keeps it billed: a request in flight,
  an open connection, a `waitUntil` tail, an exec session.

### 2. vmmd is the source; schedd keeps a pause ledger

- **vmmd** (`pkg/fcvm/quiet_pause.go`) records each incarnation's most recent
  completed pause, from the moment the VM stopped to the moment it ran again,
  and the total of its completed pauses on the monotonic clock. `InstanceStats`
  and the capacity stream's `InstanceTelemetry` carry them as
  `quiet_last_pause_started_at`, `quiet_last_pause_ended_at` and
  `quiet_paused_ns_total`, beside the existing `quiet_paused` and
  `quiet_paused_since`. The fields are additive and optional, so N-1 peers
  ignore them (docs/release-compatibility.md).
- **schedd's stats poller** (200 ms, `pkg/sched/instancestats`) hands each new
  node report to the pause ledger writer (`PauseLedger`):
  - a completed pause is recorded exactly;
  - an open pause is recorded once, so a minute spent entirely paused is
    visible before the pause ends;
  - a pause that ended without its completed report reaching the ledger (it
    was replaced before a report, the VM was destroyed while paused, or vmmd
    restarted) is closed at the last report that saw it paused, so the time
    after that is billed;
  - after a schedd restart, the node's open pauses are read back and closed
    the same way unless the next report shows them still open.
- The writer sends each node's report in one statement, at most every
  `api.QuietPauseLedgerFlushSeconds` (2 s), to
  `instance_quiet_pauses(instance_id, node_id, started_at, ended_at)`, keyed
  by `(instance_id, started_at)`. A pause only ever gains a later end, so a
  repeated or stale report never reopens or shortens one. The same statement
  advances `quiet_pause_watermarks(node_id, covered_until)` to the newest
  report it covers. A failed write keeps the queue and the old watermark.
- schedd writes the ledger because it already receives these stats and owns
  instance lifecycle. vmmd, the only root daemon, gains no write path into
  billing data. Each schedd records the node whose vmmd streams to it.
- **The ledger is not on the request path.** A pause or resume is not a
  database write; a node's pauses cost one batched statement every couple of
  seconds, however many requests it serves.

### 3. meterd subtracts paused seconds and fails toward billing

meterd already rolls closed minutes from `instance_billing_intervals`
(`InstanceBillingSeconds`, ADR-790). For each instance-minute it now
computes:

```
billed_seconds = resident_seconds − proven_paused_seconds   (never below 0)
```

The subtraction happens where `seconds` is computed in
`pkg/meter/sampler.go`, before financial-compute funding and the ADR-060
floor fill.

**Failure rule: meterd subtracts only what the ledger proves.**

- A closed pause is a fact and counts exactly. An open pause counts only up
  to its node's watermark. A node whose writes fell behind therefore proves
  less, and the rest is billed as running time.
  `meterd_quiet_pause_unproven_minutes_total` counts the instance-minutes
  where that happened. Watermark lag is normally a few seconds, against
  meterd's 60-120 s roll delay.
- Paused seconds are floored per instance-minute, and resident seconds stay
  rounded up, so rounding never bills less than the running time.
- A ledger that cannot be read fails the minute, like a failed residency
  read, so ADR-790's catch-up rolls it later from the same ledger.
- Pauses reported after their minute was rolled are ignored. `usage_minutes`
  keeps the first positive value per (instance, minute), so a late report
  never re-bills or refunds.

The customer's bill is therefore never higher than it is today, and is lower
by exactly the paused seconds meterd could prove.

Customer-facing paused seconds (a `paused_seconds` column on `usage_minutes`
or a usage view over the ledger) belong to rollout step 3. Until then, the
shadow mode's metric is the comparison.

### 4. The frozen tier keeps its job, minus billing

- Freezing at the idle timeout stays. It releases the vCPU booking entirely
  and makes the VM eligible for memory reclaim and zswap (ADR-906 decision 6),
  which quiet pause never does.
- Early freeze stops being needed for billing, so it should stay off. A
  quiet-paused VM already bills nothing, and it resumes in 1-3 ms instead of
  thawing through the control plane. The switch remains for operators who
  want earlier memory reclaim. It now needs a reason other than billing,
  recorded when it is turned on.

### 5. What does not change

- Invariant 2 (§6.2): quiet-paused instances keep their full `ram_mb + 8`
  reservation, as today.
- Admission: ADR-1151 section 3 unchanged. Quiet-paused vCPU is not active,
  and booked vCPU stays capped at 300%.
- Ownership: vmmd decides pauses, schedd writes lifecycle and the ledger,
  meterd owns usage truth. No new direct calls between components.

## Economics

ADR-1151's simulation (Azure Functions 2019 trace, 3,000 HTTP apps,
`scripts/experiments/frozen-tier-economics`) already contains both halves of
this policy:

| policy | billed GB-h per app-month | revenue per app-month | apps per EX44 | wakes |
|---|---|---|---|---|
| E. quiet pause after 2 s (billed), freeze at the idle timeout (shipped) | 29.4 | €3.94 | 716 | 84% quiet resumes, 16% thaws, mean 3.4 ms |
| F. freeze after 2 s, frozen time unbilled (early freeze) | 9.3 | €3.83 | 716 | 99.8% thaws, mean 8.5 ms |
| **G. quiet pause after 2 s (unbilled), freeze at the idle timeout (this ADR)** | **≈ 9.3** | **≈ €3.83** | **716** | **as E** |

G bills the same seconds as F, because both stop billing 2 s after the last
request. It keeps E's wakes, because nothing on the request path changes.
Revenue barely moves because most reclaimed hours fall inside included
allowances.

The table predates ADR-1430's USD plans. **Before acceptance:**

1. Re-run `freeze_sim.py` with a G policy and ADR-1430's plan table, overage
   of $0.01 per GB-h and the 30 s function idle timeout.
2. Reconcile the result with `ex44_faas_financial_model.xlsx`. The code and
   docs must not contradict it.

## Rollout

1. **Ledger, shadow only.** vmmd reports the pause record; schedd writes the
   ledger behind `quiet_pause_ledger` (`FAAS_QUIET_PAUSE_LEDGER`, off by
   default); meterd runs with `[meter] quiet_pause_unbilled = "shadow"`
   (`FAAS_QUIET_PAUSE_UNBILLED`), which reads the ledger and counts what it
   would subtract in `meterd_quiet_pause_unbilled_seconds_total{mode="shadow"}`
   but bills as today. For a week, compare it with `vmmd_quiet_paused_instances`
   and `vmmd_quiet_pause_total`.
2. **Billing switch.** `quiet_pause_unbilled = "on"`: internal accounts first,
   then everyone after the financial-model sign-off. Turning it back to `off`
   restores today's billing from the next rolled minute; already-rolled
   minutes stay as rolled.
3. **Customer surface.** Pricing page, usage views with paused seconds,
   `gregale usage`, the changelog: "billed while running requests, plus 2 s".

The prerequisite is quiet pause itself in production (vmmd `quiet_pause`,
schedd `quiet_pause_admission`), whose canaries ADR-1151 still lists as
pending. Ledger retention is `api.QuietPauseLedgerRetentionDays` (45), run by
meterd's retention loop.

## Metrics (§12 additions)

- `meterd_quiet_pause_unbilled_seconds_total{mode,plan}`: proven paused seconds,
  subtracted (`on`) or only counted (`shadow`).
- `meterd_quiet_pause_unproven_minutes_total{mode}`: instance-minutes with an
  open pause past its node's watermark; the uncovered part was billed. Alert
  `FaasQuietPauseUnprovenMinutes`.
- `schedd_quiet_pause_ledger_writes_total{outcome}`: `ok`, `error`,
  `load_error`. Alert `FaasQuietPauseLedgerWriteErrors`.
- `schedd_quiet_pause_ledger_watermark_seconds{node}`: Unix time of the newest
  recorded report. It stops moving while a node reports no VM.
- `schedd_quiet_pause_measured_seconds_total{node}` (amended 2026-10-26):
  the quiet-paused time vmmd measured, from the reports the ledger is built
  from: each incarnation's completed pauses (`quiet_paused_ns_total`) plus
  its open pause time. The alerts above only catch customers paying for
  pause time. `FaasQuietPauseUnbilledExceedsMeasured` catches the other
  direction: over 6 h, meterd's unbilled seconds (subtracted or shadow)
  exceed the measured seconds by more than 5% plus 10 minutes, meaning the
  ledger proves pauses that did not happen.

Runbook: docs/runbooks/FaasQuietPauseBilling.md.

## Consequences

- A `between_requests` workload is billed for the seconds it runs plus 2 s
  per quiet period. Its first request after a pause resumes in 1-3 ms, with no
  control-plane round trip.
- The per-request database work early freeze adds disappears: no state change,
  no billing interval and no trigger work per request. Instead, each node
  makes one batched ledger write every couple of seconds.
- The platform holds more RAM unbilled, as with early freeze. Memory pressure
  still parks frozen instances first (ADR-1151); quiet-paused instances are
  RUNNING and follow ADR-909's ordinary eviction order.
- Billing gains a second input, and metering gains a failure mode. The
  failure rule (bill when unsure) bounds both: no customer pays more than
  today.
- The ledger needs retention: drop intervals once their month is invoiced
  plus the dispute window, in meterd's retention loop.

## Alternatives considered

- **Early freeze for billing (status quo).** It works, but it puts schedd and
  the control-plane Postgres on every request, and it slows the first request
  after a pause from 1-3 ms to ~53 ms.
- **A live counter in meterd, like `cpu_usec`.** It is simpler, but live
  counters land in the newest minute, and ADR-790's catch-up rolls minutes
  without them. Billing would then depend on meterd's uptime, which ADR-790
  removed for residency.
- **vmmd writes the ledger.** It is one hop shorter, but it gives the root
  daemon a write path into billing data and more compute-node connections to
  the control-plane database. schedd already has the data every 200 ms.
- **Reconstruct pauses from the paused-time counter alone.** It is exact in
  total but places pauses only approximately when one poll gap holds several.
  Reporting the last completed pause is exact, and a missed one is billed.
  The counter stays as a cross-check.
- **Sample the `quiet_paused` flag each second.** It is inexact at interval
  edges and loses everything between samples during a stall.
- **Bill a minute in full whenever its node's watermark lags.** It is simpler
  to state but throws away closed pauses, which are exact regardless of the
  watermark.
