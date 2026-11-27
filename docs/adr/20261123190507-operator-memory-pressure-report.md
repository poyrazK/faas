# ADR-20261123190507: Operator memory pressure report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261123163340 (operator build queue report).

## Context

Under measured admission (ADR-909) a node admits work until its measured
memory reaches its admission ceiling (47,600 MB on the reference node).
Bookings, Σ(ram_mb + 8) of its live instances, may reach 250% of that
ceiling. When measured memory goes over the ceiling, schedd acts in order:

1. It compresses frozen VMs.
2. It parks frozen VMs (`frozen_park`, reason `memory_pressure`).
3. It evicts running instances until the node is back under 90% of the
   ceiling.

Separately, a workload that outgrows its plan RAM is OOM-killed inside its
VM (ADR-121, `instances.workload_oom_failed`).

Operators can see bookings against the ceiling on the Capacity page
(`/v1/admin/obs/capacity`). They cannot see:

- the measured memory that actually drives admission and eviction;
- how close each node is to the eviction band;
- which apps were pushed out, why, and how often.

Running-instance evictions were indistinguishable from ordinary stops.
`Engine.Evict` recorded them as a plain `state_transition` with no reason;
only Prometheus counters told them apart.

## Decision

### schedd records why it evicted

`Engine.EvictUnderPressure(ctx, id, reason)` evicts a running instance with
a `memory_eviction` transition event. The event's `reason` names the
trigger:

- `measured_memory`: measured memory went over the ceiling;
- `ram_pressure`: reservations went over the ceiling.

`Engine.Evict`, which rollouts also use to stop replicas, keeps
`state_transition`. Frozen instances keep their `frozen_park` row: under
pressure with reason `memory_pressure`, and through `Engine.Evict` (a
replica that froze before the rollout locked it) with reason `superseded`,
which earlier was also recorded as `memory_pressure`. The kind and the
reason are constants in `pkg/state` that sched and the report query share.

The event is still one row per transition, so customer audit counts do not
change. Nothing filters on `state_transition` to detect evictions.

### One operator session read

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/memory-pressure?window=24h` | `ObsMemoryPressureReport` |

`window` is 1m to 168h and defaults to 24h. The route reads:

- **Nodes:** the existing `OperatorCapacity` projection, for each node's
  bookings, ceiling and live counts.
- **Measured memory:** `node_memory_reports`, the latest measured memory,
  vmmd-used memory and frozen count per node, through the existing
  `NodeMemoryReportStore`.
- **Events:** a new optional store query, `state.MemoryPressureReader`
  (sqlc, no migration). `OperatorMemoryPressureEvents` returns the window's
  `memory_eviction` events, `frozen_park` events with reason
  `memory_pressure`, and `instances.workload_oom_failed` events, newest
  first, up to `ObsMemoryPressureEventsMax` (20,000).
  - Each event is joined to its instance (node, RAM) and to its app,
    account and plan. Every join is optional: terminal instances age out.
  - `events_kind_at_idx` serves each kind's range.

`pkg/apid/memorypressure` (ADR-982) grades each node into one of five
states:

- `over`: memory is above the ceiling;
- `near`: memory is at or above `MeasuredEvictionTargetPercent`
  (90%) of the ceiling, or bookings are at the overcommit cap;
- `ok`;
- `stale`: the last measurement is older than
  `ObsMemoryMeasurementStaleSeconds` (30 s);
- `unmeasured`: the node has no report.

The memory graded is vmmd's used memory when the node reports it, because
that is what schedd's measured-memory eviction compares with the ceiling;
otherwise admission's measured charge. Fleet totals of measured memory,
used memory and frozen VMs count only nodes with a fresh report.

It also reports:

- fleet totals;
- event counts per node, per kind and reason, and per app (the 25 most
  affected, with the largest OOM peak against plan RAM);
- the newest 100 events.

## Consequences

- **Console:** gets a Memory pressure page under On call. The Capacity page
  stays the place for booked headroom and placement.
- **Event history:** evictions before this change are not distinguishable;
  the report's eviction counts start with the release that ships it.
- **Read cost:** no migration. The event read is bounded, uses an existing
  index, and only operators run it, on demand.
- **Visibility:** the route is operator-only and excluded from the public
  OpenAPI spec, like the other `/v1/admin/obs/*` routes.
