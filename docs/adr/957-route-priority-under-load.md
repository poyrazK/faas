# ADR-957: Route priority for the warm-capacity queue

- Status: Accepted
- Date: 2026-10-10
- Related: ADR-454, ADR-844
- Amends: none

## Context

When every routable instance of an app is busy, the gateway parks requests in
a per-app warm-capacity queue (`pkg/gateway/vm_concurrency.go`): strictly
FIFO, bounded per gateway by the app's queue depth and across gateways by a
fleet permit budget. A burst of exports or a crawler sweep can therefore fill
the queue and turn away checkout and login requests with 503, even though the
app has said which routes matter most (its route-health selectors).

## Decision

Requests in the warm-capacity queue are ordered by route priority class:
critical, normal, bulk.

- **Classification.** Saved per-app rules (`app_route_priorities`, at most 20,
  method + route template or edge-rule glob, first match wins) decide the
  class. With no saved rules, the app's route-health selectors are critical.
  An empty saved list means no priorities. Crawlers and link-preview bots that
  match no rule are bulk; uptime monitors stay normal so availability checks do
  not see self-inflicted 503s. Everything else is normal.
- **Ordering.** A request is inserted behind every waiter of its class or a
  higher one, so the queue stays FIFO within a class. The head, which is
  already acquiring a slot, never moves.
- **Displacement.** When the local queue is full, or the fleet refuses a
  permit, a request takes the place of the newest waiter of the lowest lower
  class on the same gateway. The displaced waiter receives the existing
  queue-full 503 with Retry-After, and its fleet permit passes to the new
  request, so the fleet-wide bound is unchanged. A gateway never displaces
  another gateway's waiters.
- **Cost.** A request is classified only once it has to queue, so an
  unsaturated app does no lookup. Each gateway caches rules for 30 s.
- **API.** `GET`/`PUT`/`DELETE /v1/apps/{slug}/route-priorities` (apid is the
  only writer) and `gregale routes priority`.
- **Measurement.** `gateway_route_priority_queue_total{class,outcome}` with
  outcomes queued, displacing, displaced and rejected.

## Consequences

- Under saturation, critical routes wait less and are turned away last; bulk
  traffic absorbs the shedding. Nothing changes while capacity is available.
- Bulk and normal requests can be displaced after they started waiting; they
  get the same 503 + Retry-After a full queue already returns.
- Cold-wake admission (the WakeGate and the plan-ordered admission queue) and
  plan concurrency are unchanged; this ADR orders only waiters for warm
  capacity of one app.

## Rejected alternatives

- **A new edge-rule kind.** Priorities are a single per-app list read only on
  the saturated path; an edge-rule kind would add a hot-path matcher, a CHECK
  constraint change and per-host rules for an app-level property.
- **Shorter waits for bulk requests instead of displacement.** Does not help a
  critical request that arrives when the queue is already full.
