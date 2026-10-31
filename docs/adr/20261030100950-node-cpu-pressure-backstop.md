# ADR 20261030100950 · CPU pressure backstop

- **Status:** proposed. Built behind the schedd switch
  `cpu_pressure_backstop`, on by default since the amendment of
  2026-10-31.
- **Date:** 2026-10-30
- **Amends:** ADR 20261029151111 (measured CPU admission: its "planned
  next" pressure backstop), ADR-1151 §3 ("CPU pressure (PSI) alerts on it")
- **Related:** ADR-20261022184531 (node wake breaker, the column this one
  copies), ADR-20261029085904 (capacity stream silence), issue #5879 item 4

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Quiet pause admission (ADR-1151) and measured CPU admission (ADR
20261029151111) admit by what instances use, not what they booked, and let a
node's bookings reach 300% and 700% of its CPU budgets. A resume or a burst
never waits for admission, so when many quiet or idle instances get busy
together, host CPU can exceed the node's cores until the next sample charges
them. Measured CPU admission corrects itself within one 200 ms sample for
the instances it measures, but nothing sees contention it does not measure:
quiet-paused instances resuming unseen, CPU used outside the tenant slice
(vmmd, restores, the bridge units), or steal on a virtualised host.

Linux reports that contention directly: a cgroup's `cpu.pressure` is the
share of time its runnable tasks waited for a CPU. Nothing in the platform
reads it today.

Pressure alone is the wrong trigger. A task held back by its own `cpu.max`
quota can count as stalled in its cgroup and every parent, so one app
running at its 1-core quota on an idle node can make the tenant slice look
pressured. A node is out of CPU only when its tasks wait **and** its CPUs
are nearly all busy.

## Decision

1. **vmmd reports two readings.** `StatsResponse` gains
   `tenant_cpu_pressure_some_avg10` (the `some avg10` value of
   `faas-tenant.slice/cpu.pressure`, percent) and `host_cpu_busy_percent`
   (the host's busy share of all CPUs over the last
   `NodeCPUBusyWindowSeconds`, 10 s, from `/proc/stat`). The capacity stream
   carries both in `CapacityReport` fields 13 and 14, outside the capacity
   signature like the other telemetry. Both are absent on a host without
   the files and on an older vmmd.

2. **The node's own schedd decides.** A node is under CPU pressure when
   `some avg10` ≥ `NodeCPUPressureStallPercent` (20) and host busy ≥
   `NodeCPUPressureBusyPercent` (90). The schedd that receives the node's
   readings (its capacity stream, or vmmd `Stats` on a single box) checks
   every watchdog tick (1 s). While the node is under pressure it holds
   `compute_nodes.cpu_pressure_until` at least `NodeCPUPressureHoldSeconds`
   (30 s) ahead, rewriting it once less than half the hold remains, so a
   pressured node costs one write and one `compute_node_changed` every 15 s.
   Nothing clears the column: it expires. A node whose readings stop is
   never held back. Only a schedd with `cpu_pressure_backstop` on writes it.

3. **Every schedd honours the column**, through the node registry that
   `compute_node_changed` already refreshes:
   - **Placement** leaves a pressured node out of its first pass, like a
     degraded node or an open wake breaker. It still uses the node when
     nothing else fits.
   - **Admission** withholds the CPU discount on a pressured node: quiet
     pauses and measurements no longer offset bookings, so the vCPU and CPU
     gates count every booking, as they do with both switches off. A node
     booked past its budget admits nothing new until the pressure passes; a
     node within its budget still admits. A refusal keeps its `node_vcpu` or
     `node_cpu` code and says the node is under CPU pressure.
   - The durable node reservation receives no discount for the node, so it
     never admits what the ledger refused.

4. **Why a new column, not readiness or the wake breaker.** The heartbeat
   rewrites `readiness` on every beat, so a pressure mark there would be
   overwritten, and a successful wake closes the wake breaker. A separate
   column that only expires has one writer per node and no other meaning.
   Readiness, its gauge and the `FaasComputeNodeDegraded` alert do not
   change.

5. **Metrics.** `schedd_node_cpu_pressure_some_avg10{node}` and
   `schedd_node_cpu_busy_percent{node}` (the readings, published whether or
   not the switch is on, to tune the thresholds), and
   `schedd_node_cpu_pressure_holds_total{node}` (episodes this schedd
   started).

6. **Limits** (`pkg/api/limits.go`): `NodeCPUPressureStallPercent`,
   `NodeCPUPressureBusyPercent`, `NodeCPUPressureHoldSeconds`,
   `NodeCPUBusyWindowSeconds`.

## What does not change

- Invariant 2, RAM admission and measured memory.
- The request path: the readings ride the existing capacity stream and
  `Stats` call; the column is read from the in-memory node registry.
- A quiet resume still never waits for admission. The backstop stops adding
  load to a contended node; it does not shed load already there.

## Rollout

- Data first: the migration adds one nullable column with no default, so no
  row is written. A previous-release schedd neither writes nor reads it.
- vmmd before schedd, as the deploy order already is. A schedd that sees no
  readings never marks a node.
- Watch the two gauges on dense nodes with the switch off, confirm the
  thresholds would have fired only under real contention, then turn on
  `cpu_pressure_backstop` together with `measured_cpu_admission`.

## Rejected alternatives

- **Pressure alone.** Quota throttling of one busy app would trigger it on
  an idle node.
- **Host busy alone.** A node can run 95% busy with nobody waiting; that is
  good utilisation, not contention.
- **Refuse every admission on a pressured node.** On a single box that
  fails wakes. Withholding the discount stops the overcommit, which is what
  creates the risk, and keeps a node within its bookings serving.
- **Evaluate in every schedd from its own telemetry.** A schedd polls only
  peers that host its own instances, so it would not see pressure on a peer
  where it places a first instance. The column makes one decision
  fleet-wide.

## Validation

- vmmd: parsing `cpu.pressure` and `/proc/stat`, the busy window, and
  `Stats` and the capacity report carrying both readings.
- state: the column on MemStore and PostgreSQL; lifecycle writes keep it.
- sched: a node is marked only when both thresholds hold, the mark is
  refreshed at half the hold and not before, readings that stop are never
  marked, placement prefers an unpressured node and falls back to a
  pressured one, and admission withholds the discount on a pressured node
  and names the pressure in its refusal.
- schedd: the switch is off by default and parses from TOML.

## Amendment 2026-10-31: on by default

The owner turned `quiet_pause_admission`, `measured_cpu_admission` and
`cpu_pressure_backstop` on in production on 2026-10-31, 09:13-09:16 UTC, by
hand in `/etc/faas/schedd.toml` on fsn-3, fsn-2 and the control plane
(rc.311), one node at a time. Every schedd restarted cleanly, logged each
switch enabled and raised no new warnings. The nodes were about 3% busy with
no CPU pressure, and no node was held. vmmd's `quiet_pause` has been on by
default since 2026-10-27.

`cpu_pressure_backstop` now defaults to on (`cmd/schedd/config.go`), with
measured CPU admission, whose overcommit it bounds;
`cpu_pressure_backstop = false` in `schedd.toml` turns it off. The
thresholds stay the initial values, to be tuned from
`schedd_node_cpu_pressure_some_avg10` and `schedd_node_cpu_busy_percent`
once nodes run dense.

Release rollouts run `gregalectl manifest render` on every node, which
rewrites `/etc/faas/schedd.toml` from the manifest, so the hand edit would be
lost at the next rollout and would never reach a new node. The production
manifest cannot carry a new key until every node's `gregalectl` and `imaged`
parse it, as ADR-1151's 2026-10-27 amendment found. A default in the binary
reaches every node with the release itself.
