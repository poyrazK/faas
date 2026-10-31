# ADR 20261029151111 · Admission by measured CPU

- **Status:** proposed. Built behind the schedd switch
  `measured_cpu_admission`, on by default since the amendment of
  2026-10-31.
- **Date:** 2026-10-29
- **Amends:** ADR-1151 §3 (the booking cap once measured CPU admission is
  on), ADR 20261027220445 decision 3 (the bound on the discount the durable
  guard accepts)
- **Related:** ADR-909 (admission by measured memory), ADR-193 (durable
  node reservation), ADR-1430 (plan table), issue #5879

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

schedd admits CPU by bookings alone. `NodeLedger.Admit` runs two CPU gates:

- the **guest vCPU gate**: every awake instance books its plan's guest vCPUs
  (2 on Free, Hobby and Pro; 4 on Scale) against `vcpu_budget`, the tenant
  CPUs × `api.CPUOvercommit` (8);
- the **physical CPU gate**: every awake instance books its cgroup quota
  (1000 mc by default) against the tenant CPUs × 8 × 1000 mc.

A booking is held for the whole time an instance is RUNNING, including its
idle timeout. On a 16-thread node the guest vCPU gate admits 60 awake
instances (15 × 8 / 2), long before RAM fills. RAM has been admitted by
measured use since ADR-909; CPU has no equivalent.

Measured on production on 2026-10-29 (read-only, issue #5879): an awake
instance used 0.02 to 0.05 cores, boot and restore included, against a
booking of 0.25 of a thread after the 8× overcommit. Production traffic is
mostly tests, and nested virtualisation on n2 inflates CPU per VM, so the
numbers are an upper bound for idle instances, not a load model.

The goal is the most customers per node. With the frozen tier on, a
16-thread / 128 GB node holds 195 apps under today's admission, and RAM
would allow 1,454 (simulation below).

## Decision

1. **Measured charge.** With `measured_cpu_admission` on, the CPU gate
   charges an instance its measured host CPU instead of its CPU booking.
   - Source: the instance stats poller (`pkg/sched/instancestats`), which
     already reads every instance's CPU from vmmd every 200 ms. Peer nodes
     are read through the owner fleet reader, as quiet pauses are.
   - Measurement: the larger of the latest reading and the p95 of the
     instance's per-bucket peaks over the last
     `MeasuredChargeWindowSeconds` (600 s), one bucket per
     `MeasuredChargeSampleSeconds` (5 s). A bucket keeps its peak, so a
     request burst between two samples still counts. These are ADR-909's
     window constants.
   - An instance is measured only once its history spans
     `MeasuredCPUMinHistorySeconds` (10 s). Until then it is charged its
     booking, so a boot or a restore is never charged as idle.
   - Charge: the measurement × `api.CPUOvercommit`, the rate the booking
     model already assumes (a 1000 mc booking stands for 125 real
     millicores), so measured charges fill the budget exactly when they fill
     the node's cores. The charge is never less than
     `MeasuredCPUMinChargePercent` (10%) of the booking, which keeps room for
     an idle instance's next request, and never more than the booking, so
     measurement only relaxes the gate.
   - A booking admitted after the sample is charged in full, and a node
     whose sample is older than `MeasuredAdmissionSampleMaxAgeMillis`
     (2 s) is charged its bookings, as under ADR-909.

2. **Guest vCPU stops being a resource for a measured instance.** The vCPU
   gate does not count a measured instance's guest vCPUs. An idle vCPU
   thread costs the host nothing, `cpu.max` already bounds the host CPU of
   each instance, and that host CPU is what the CPU gate counts. Unmeasured
   instances still count their guest vCPUs.

3. **Booking cap.** A resume or a burst never waits for admission, so every
   booking, discounted or not, stays within
   `MeasuredCPUMaxOvercommitPercent` (700%) of each budget while measured CPU
   admission is on. The cap replaces ADR-1151's 300%
   (`QuietPauseMaxVCPUOvercommitPercent`), which still applies when only
   quiet pause admission is on. On the guest vCPU budget it is the sanity
   cap on guest vCPU threads per node: 840 on a 16-thread node, or 420
   two-vCPU instances awake. 700% is where the simulation stops gaining:
   from there on the measured CPU gate binds first.

4. **Composition with quiet pause.** The discount is the whole booking of an
   instance the latest sample saw quiet-paused, plus, for each other
   measured instance, its booking less its charge. An instance that is both
   counted once, as paused. Either switch works without the other.

5. **The durable guard keeps the scheduler's discount.** The scheduler
   passes the combined discount in the fields ADR 20261027220445 added for
   quiet pauses (`NodeCompute.QuietVCPU`, `QuietCPUMillicores`; the names
   predate the second source). The guard now bounds the discount by
   `MeasuredCPUMaxOvercommitPercent` instead of 300%: migration
   `20261029151111347` changes `admission_reserve_node` to clamp at six
   budgets instead of two, and the statement path and MemStore use the same
   bound (`state.quietDiscountBound`). The argument of ADR 20261027220445
   that the guard never refuses what placement admits holds with the cap at
   700%. A scheduler with only quiet pause admission still holds itself to
   300%; the guard's bound is the trust boundary, so it is the larger cap.

6. **Metrics.**
   - `schedd_node_cpu_measured_millicores{node}`: the sum of a node's
     measured instances' measurements.
   - `schedd_instance_cpu_measured_millicores{node}`: a histogram of each
     measured instance's measurement per tick. The floor and the window are
     tuned on it. It is labelled by node, not by instance, to keep
     cardinality bounded by the fleet.
   - Per (app, node) CPU already exists as `schedd_instance_cpu_pct` and
     `schedd_instance_cpu_seconds_total`.

7. **Limits.** The new constants (`MeasuredCPUMinChargePercent`,
   `MeasuredCPUMinHistorySeconds`, `MeasuredCPUMaxOvercommitPercent`) live in
   `pkg/api/limits.go`.

## What does not change

- Invariant 2 and RAM admission. Measured CPU never touches the RAM gates.
- Billing: plan RAM + 8 MB per running second (§4.7).
- The request path. Requests to a running instance never reach schedd, a
  wake's admission is still one statement (ADR-1741), and the discount is a
  parameter of that statement, not a read.
- Each instance's `cpu.max`.

## Risk: many idle instances get busy together

With the cap at 700%, bookings can reach seven times the budget. If every
measured-idle instance on a node gets busy at once, host CPU exceeds the
node's cores until the busy ones are charged again. A busy instance's charge
rises to its booking on the next 200 ms sample, and the p95 window keeps it
there for up to 10 minutes, so admission stops adding to a node within one
sample. The cost of the overshoot is CPU contention, which raises latency
and does not fail requests: the same trade ADR-1151 made for quiet resumes,
at a higher cap.

Not in this change, and planned next:

- a CPU pressure backstop: vmmd reports the tenant slice's `cpu.pressure`,
  and placement deprioritises and admission refuses a node above a
  threshold;
- a frozen cap by measured RAM instead of the fixed `FrozenMaxPerNode`.

## Simulation

The ADR-1151 simulator replays the Azure Functions 2019 trace on a
16-thread / 128 GB node with the frozen tier on, plan idle timeouts and
ADR-1430 plan RAM. A node fits N apps when the 99.9th percentile of every
resource, across sampled instants after a day of warm-up, is within its
limit; the result is the median of five draws.

Measured CPU is modelled conservatively: an instance is charged its whole
booking when the p95 of its 5 s buckets over the last 10 minutes is a bucket
in which a request ran, and while it is unmeasured; otherwise it is charged
160 mc (20 real millicores, the high end of the production idle sample). A
quiet-paused instance is charged nothing.

| Admission | Cap | Apps per node | Binds first |
|---|---|---|---|
| Bookings (today) | — | 195 | guest vCPU |
| Quiet pause (ADR-1151) | 300% | 561 | guest vCPU |
| Measured CPU | any | 416 | measured CPU |
| Measured CPU + quiet pause | 300% | 606 | booking cap |
| Measured CPU + quiet pause | 500% | 1,005 | booking cap |
| Measured CPU + quiet pause | **700%** | **1,122** | measured CPU |
| Measured CPU + quiet pause | none | 1,122 | measured CPU |
| No CPU gate | — | 1,454 | RAM |

At 700% the 99.9th percentile of booked guest vCPU is 560% of the budget,
under the cap, and measured CPU is 92% of the budget.

## Rollout

- Data first: the migration only changes a bound inside
  `admission_reserve_node`. A previous-release scheduler's discount never
  passes 300%, so its admissions are checked exactly as before.
- A schedd of this release that runs before the migration finds the old
  300% bound. Its admissions past 300% are refused by the guard and
  re-placed on another node, as a RAM refusal is; nothing is over-admitted.
- Turn the switch on one node, watch the histogram, the node gauge and
  `FaasCpuStarvation`, then the fleet. The floor and the cap are tuned from
  the histogram.

## Rejected alternatives

- **Raise `vcpu_budget` only.** It keeps the booking model and moves the
  limit: the simulation gets 395 apps at twice the budget, still bound by
  guest vCPU, and every node needs its own number.
- **A per-admission cap passed to the guard.** The guard's bound exists to
  hold a scheduler that misreports its discount. A bound the scheduler
  chooses would not.
- **Per-instance metric labels.** Unbounded cardinality across wakes; the
  per-node histogram answers the tuning question.
- **Charge by the latest sample alone.** A burst between two samples would
  not count, and an instance between requests would look idle.

## Validation

- `TestMeasuredCPUAdmission` (sched): idle, below the floor, as booked, busy
  and half-booked instances fill an 8 vCPU / 4,000 mc node to the expected
  count; below the floor only the 700% cap binds.
- `TestMeasuredCPUAdmissionNeedsTheSample`: no sample, a sample older than
  the bookings and a stale sample all charge bookings; RAM is unaffected.
- `TestMeasuredCPUWithQuietPause`: a paused, measured instance keeps its
  whole discount.
- `TestMeasuredCPUAdmissionRandomTraffic`: 20,000 random admissions,
  releases, CPU changes and samples. At every admission the CPU computed
  independently from the sample fits the budget, unmeasured vCPU fits the
  vCPU budget, and bookings fit the cap.
- `checkNodeComputeReservation` (state), on MemStore and on PostgreSQL
  through the function and the statement paths: an oversized discount fills
  a node to exactly 700% of the budget.
- `TestCPUHistoryChargeSample`, `TestMeasuredCPUSnapshot` (instancestats):
  the bucket peak and p95, the minimum history, stale samples, the node
  gauge and the histogram.
- `cmd/schedd`: the switch is off by default and parses from TOML.

## Amendment 2026-10-31: on by default

The owner turned `quiet_pause_admission`, `measured_cpu_admission` and
`cpu_pressure_backstop` on in production on 2026-10-31, 09:13-09:16 UTC, by
hand in `/etc/faas/schedd.toml` on fsn-3, fsn-2 and the control plane
(rc.311), one node at a time. Every schedd restarted cleanly, logged each
switch enabled and raised no new warnings. The nodes were about 3% busy with
no CPU pressure, and no node was held. vmmd's `quiet_pause` has been on by
default since 2026-10-27.

`measured_cpu_admission` now defaults to on (`cmd/schedd/config.go`);
`measured_cpu_admission = false` in `schedd.toml` turns it off. The rollout
above went fleet-wide at once rather than one node first, at the owner's
request and at today's low load. Tuning the floor and the cap from
`schedd_instance_cpu_measured_millicores` continues.

Release rollouts run `gregalectl manifest render` on every node, which
rewrites `/etc/faas/schedd.toml` from the manifest, so the hand edit would be
lost at the next rollout and would never reach a new node. The production
manifest cannot carry a new key until every node's `gregalectl` and `imaged`
parse it, as ADR-1151's 2026-10-27 amendment found. A default in the binary
reaches every node with the release itself.
