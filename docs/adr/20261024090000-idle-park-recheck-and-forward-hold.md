# ADR-20261024090000: Idle parks re-check activity, and a park holds new forwards

Date: 2026-10-24
Status: Accepted
Amends: spec §6 (park), [ADR-1151](1151-pause-tiers.md) (the freeze gate is reused for park)
Related: [ADR-1133](1133-undelivered-service-calls-reroute.md) (undelivered forwards re-route), [ADR-643](643-park-to-admit-refused-wakes.md) (park for capacity)

## Context

The idle reaper takes one snapshot of instance activity per tick, then parks
each idle instance in turn. A park can take up to two minutes, so later parks
in a tick act on an idle decision that is tens of seconds or minutes old.
`Engine.Park` re-read the row under the app lock but checked only its state.
The default path then entered SNAPSHOTTING, and vmmd paused and killed the
guest without looking at its forwards. A request that reached the instance
after the tick, often the first request after an idle period, got a 502
instead of being held for a wake, and its half-processed state went into the
snapshot. Park for capacity (`pressure_park.go`) already re-reads the row;
the idle and scale-in paths did not.

A refusal from vmmd cannot keep the instance running once schedd has
written SNAPSHOTTING: the state machine (spec §6.1) has no SNAPSHOTTING →
RUNNING edge. Marking the row STOPPED while the guest still runs would leave
vmmd's live-VM reaper to kill it anyway.

## Decision

1. **schedd re-checks before SNAPSHOTTING.** The reaper parks through
   `Engine.ParkIdle`. It passes the activity it chose on (last request,
   request count) and a reader for fresh guest activity: vmmd's in-flight
   forwards and newest request from the stats poller, plus the conntrack flow
   count. Under the app lock, with the row still RUNNING, the instance stays
   RUNNING when any of these holds:
   - a waitUntil task is pending;
   - vmmd reports a forward in flight;
   - an open flow exists;
   - for idle parks only, the row or vmmd shows a newer request or a higher
     request count.

   Scale-in (`ReapAggressive`) parks instances that still take traffic, so
   for those only work in flight blocks the park. The engine returns
   `ErrParkActivity`, and the reaper skips the instance until its next tick.
   Other `Park` callers (quota, operator, warm pool, capacity) keep their
   behaviour.
2. **vmmd holds new forwards for the whole terminal park.** It publishes a
   gate that forwards wait on in `AwaitFrozen`, the same wait a freeze uses.
   It releases the gate after teardown, so a held forward finds no instance
   and fails as undelivered, which the gateway re-routes to a fresh wake.
3. **vmmd drains forwards already at the guest before the pause.** It waits
   up to `api.ParkForwardDrainTimeoutSeconds` (5 s, the same as the waitUntil
   drain) and does not count forwards held on the gate. A forward still
   running at the deadline is cut by the pause, as before. A forced park
   therefore still succeeds.

## Consequences

- With the recheck the window narrows to the gap between it and the gate:
  a request routed by a gateway that has not yet seen SNAPSHOTTING. Such a
  request is held and re-routed instead of failing.
- An idle park that finds new activity costs one row read and one poller map
  read. The instance is reconsidered on the next tick.
- A park with a forward at the guest can take up to 5 s longer. That time
  comes out of the snapshot budget (at least 15 s plus 60 s per GB).
- Held forwards wait up to the length of the park (p90 about 22 s) before
  they are re-routed. Without the gate they would have failed.

## Rejected alternatives

- **vmmd refuses a busy park and schedd puts the row back to RUNNING.** This
  needs a SNAPSHOTTING → RUNNING edge in the spec §6.1 state machine, and
  every reader of that state would have to account for it.
- **Freeze before every park.** This would reuse the frozen tier's barrier,
  but vmmd refuses to park a frozen app VM, and freeze eligibility excludes
  private-network members and pending waitUntil work. The park path would
  depend on a tier that is off by default.
