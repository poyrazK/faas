# ADR: Route priority in the cold-wake queue

Date: 2026-10-18
Status: Accepted
Related: [ADR-978 route priority for the warm-capacity queue](978-route-priority-under-load.md), spec §4.1 (wake-blocking)

## Context

ADR-978 orders the warm-capacity queue by route priority, but a parked app
first queues requests behind its wake (`pkg/gateway/gate.go`). That queue is
small (4 waiters on Free, 16 on Hobby, 64 on Pro, 128 on Scale) and admits
first-come, first-served: a crawler sweep that reaches a parked Hobby app
fills it, and the next `POST /checkout` gets 503 while the bots wait. When
the wake finishes, every waiter is released at once and the instance's first
slots go to whichever goroutines win the race, so a critical request is as
likely to wait in the warm queue as a bulk one.

## Decision

The wake gate uses the same classes as ADR-978 (critical, normal, bulk; saved
rules, else route-health selectors; crawlers and preview bots bulk).

- **Admission.** When an app's wake queue is full, the newcomer is classified
  and, if it outranks a waiter, takes the place of the newest waiter of the
  lowest lower class. That waiter receives the existing queue-full 503 with
  Retry-After. The request that started the wake never moves. Settling a full
  queue is the only time the wake path may read the rules synchronously,
  through ADR-978's 30 s cache.
- **Release.** When a wake succeeds, critical waiters are released first,
  normal ones `RoutePriorityWakeReleaseStagger` (25 ms) later and bulk ones
  after another stagger; absent classes add no delay. A failed wake releases
  everyone at once with the error.
- **No blocking reads.** When a second request joins a wake, the gateway
  starts loading the app's rules in the background
  (`RoutePriorityPrefetchTimeout`). The release classifies only from the
  cache; if the rules are not loaded yet, every waiter is released together,
  exactly as before.
- **Metrics.** `gateway_route_priority_wake_total{app,class,outcome}` counts
  displacing, displaced, rejected and released_later.

## Consequences

- A burst of bulk traffic can no longer turn away critical requests from a
  parked app's wake queue, and critical requests reach the new instance first.
- Lower classes wait at most 50 ms longer after a wake that also held a
  higher class; a wake with one class or no cached rules is unchanged.
- Release ordering is best effort: a released critical request still has to
  reach slot acquisition within the stagger. Beyond the first slots, ADR-978's
  warm queue orders the rest.

## Not decided here

Per-class wake wait budgets, showing wake-queue outcomes in the route feature
evidence (ADR-1152), and ordering waiters across gateways.
