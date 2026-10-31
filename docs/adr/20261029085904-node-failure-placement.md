# ADR 20261029085904 · Placement around failed and failing nodes

- **Status:** proposed
- **Date:** 2026-10-29
- **Amends:** ADR-20261022184531 (node readiness, wake failover)
- **Related:** ADR-028 (heartbeat), ADR-137 (recovery arbiter), the
  dead-node reconciler, the vmmd capacity stream (ADR-025 axis 5)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A scheduler takes a node out of rotation when three heartbeat probes, 30 s
apart, fail. That is 60–90 s. The recovery arbiter then parks the node's app
instances within about a second. The threshold is deliberately slow,
because declaring a node dead triggers that recovery, and a vmmd that
crash-restarts must not set it off. ADR-20261022184531 added one readiness
signal (vmmd cannot adopt a base generation) and re-placed wakes refused for
that reason. Four gaps remained:

1. **A node that answers its probes but fails every boot** (disk, jailer,
   KVM, cgroups, jail ids) stayed ready. Placement kept choosing it, often as
   the app owner's preferred node, and every wake failed until an operator
   drained it.
2. **A wake whose request never reached vmmd** (connection refused, name
   resolution, a refused dial) failed outright. Failover deliberately skips
   transport errors, because a VM might exist, but here none can.
3. **The wake fast path returned a running instance on a node already out
   of rotation**, in the moment before recovery parked it, or when recovery
   could not take the node over.
4. **vmmd reports capacity every second** on its stream, yet a stopped vmmd
   stayed in placement for the whole heartbeat window.

## Decision

1. **Per-node boot breaker.** Each scheduler counts node-caused boot
   failures per node:
   - vmmd not reached, an `internal` problem, or a raw `Internal`, `Unknown`,
     `Unavailable` or `DataLoss` status;
   - not app problems (startup timeout, scan findings, capacity), and not
     the caller's own cancellation.

   vmmd's catch-all `internal` can still be an app's own fault (a bad
   layer, a corrupt snapshot), so the threshold is three failures in a row
   from three different apps (`api.NodeBootBreakerFailures`,
   `api.NodeBootBreakerDistinctApps`). Placement then treats the node as
   degraded for `api.NodeBootBreakerOpenSeconds` (60): it uses the node only
   when nothing else fits. A successful boot closes the breaker. The caller's
   cancellation and deadline arrive as raw `Canceled` and `DeadlineExceeded`
   statuses (`liftErr` passes statuses without ErrorInfo through
   unchanged), so they do not count. The breaker is process-local, so each
   scheduler learns from its own wakes and none overwrites another's
   readiness.
2. **Re-place what never reached vmmd.** A router dial failure, or a gRPC
   `Unavailable` raised before any transport existed (`Error while
   dialing`, `authentication handshake failed`, `name resolver error:`), is
   a wake failover refusal with reason `vmmd_unreachable`. The wake is
   re-placed within its deadline, as for the base-generation refusals.
   `transport is closing` and `error reading from server` share the
   `connection error:` prefix but can follow a request that reached vmmd,
   so they are excluded.
3. **Fast-path backstop.** The wake fast path, including the owner-cache
   path, skips a running instance whose node is out of rotation: missing
   from the active-node registry, and unavailable, retired or inactive in
   its row. This is a registry lookup, with a row read only for a node the
   registry lacks. For an app already at max_concurrency, the stranded
   instance still holds its ledger slot until recovery parks it, so that
   wake is refused for that moment rather than routed to a VM that is
   gone.
4. **Capacity-stream silence.** Each tick, the scheduler that receives a
   node's capacity stream pings the node's vmmd once the stream has been
   silent for `api.CapacityStreamLostSeconds` (10), unless a planned
   restart notice is active.
   - A vmmd that answers is only reconnecting its stream (its reconnect
     backoff can outlast the threshold). It is left alone and pinged again
     after another threshold.
   - A vmmd that does not answer gets its node's readiness marked
     `unavailable` ("vmmd stopped reporting capacity").

   Placement, and owner choice for new apps, avoid an unavailable-readiness
   node as they do a degraded one. When reports resume, the scheduler
   restores the readiness its mark replaced, only if the node still carries
   the mark, so a heartbeat that proved the node ready meanwhile stands. A
   failed node read is retried on the next tick, not skipped. The heartbeat's
   lifecycle decision, and with it recovery, is unchanged: VMs may keep
   serving while vmmd restarts, so their instances are not touched. This
   scheduler becomes the second writer of `compute_nodes.readiness`
   (`ComputeNodeReadinessStore` says so).

Unchanged: the heartbeat threshold, the recovery arbiter, the dead-node
reconciler, and wake failover for errors that may have left a VM.

## Consequences

- A node that cannot boot leaves placement after three failed wakes from
  two apps, instead of after an operator notices.
- A dead node's wakes are re-placed as soon as gRPC fails fast. A node
  behind a black hole costs one connect timeout before its channel fails
  fast.
- A stopped vmmd leaves placement in about 10 s instead of 60–90 s.

No migration and no new wire field. The new failover reason is a new label
value on `schedd_wake_failover_total`.

## Validation

`pkg/sched/node_failure_test.go`:

- a wake that cannot reach vmmd is re-placed;
- a call whose transport closed is not re-placed;
- the breaker steers placement away after node-caused failures from three
  apps, and does not trip on app failures;
- the fast path skips an instance on a node out of rotation;
- a silent stream whose vmmd answers is left alone;
- a silent stream whose vmmd does not answer marks the node unavailable and
  placement goes elsewhere;
- resumed reports restore the previous readiness.
