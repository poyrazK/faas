# ADR-208 · Acknowledged routing handoff for zero-downtime service rollouts

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** A readiness-gated service rollout completes through an explicit
  four-stage handoff: publish candidate-only weights while retaining the
  predecessor as live, collect a generation-tagged acknowledgement from every
  active serving gateway, observe every predecessor instance at zero in-flight
  requests for a post-ack quiet window, then supersede and park the
  predecessor. A rollout never parks healthy predecessor capacity to make room
  for its candidate.
- **Why:** Readiness alone proves that the candidate can serve; it does not
  prove that every gateway has stopped selecting the predecessor or that
  requests selected before the route refresh have finished. The old code
  finalised the deployment and parked the predecessor before the asynchronous
  `deployment_changed` refresh reached gateways. Under physical capacity
  pressure it also parked one healthy predecessor first and retried admission,
  contradicting ADR-199's requirement that the rollout hold when the bounded
  surge cannot fit. Both paths create avoidable outage windows.

## Handoff protocol

1. `BeginServiceRolloutCutover` atomically sets the ready candidate to 100%
   traffic and its live siblings in the same scope to 0%. It does not change
   deployment status or rollout state, so the predecessor remains recoverable
   and the `rolling_out` row is a durable retry record.
2. schedd allocates a monotonic `deployment_route_generation`, subscribes to
   `deployment_route_ack`, and repeatedly publishes
   `deployment_route_changed`. Each active compute node with a serving gateway
   URL must acknowledge by node name only after refreshing both deployment
   weights and live instance targets.
3. After all acknowledgements, schedd requires fresh vmmd telemetry, received
   by schedd after the final acknowledgement, to report zero in-flight requests
   for every running predecessor replica across a two-second quiet window. The
   scheduler-local receipt clock avoids cross-node clock-skew ambiguity;
   missing or stale telemetry is not interpreted as zero.
4. Only then does `FinalizeServiceRollout` supersede the predecessor and mark
   the candidate complete; schedd parks the predecessor replicas for rollback
   snapshot reuse.

Every timeout is fail-safe: the predecessor remains live and the rollout
remains `rolling_out`. A startup and 30-second database sweep replays
unfinished service rollouts through schedd's bounded deployment-reconcile
pool, including after restarts. The sweep rotates through the recovery set so
the oldest unavailable handoffs cannot starve newer rows; retries never create
one goroutine per stuck rollout. PG notifications remain an acceleration and
acknowledgement transport, not the durable source of truth.

Legacy single-box installations with no named serving gateways keep the
existing notification refresh path and skip the fleet acknowledgement and
telemetry barriers. This exception is explicit: it preserves local developer
and test installs but is not a fleet zero-downtime guarantee. A configured
multi-node fleet with no active registered serving gateway fails closed and
retains the predecessor instead of falling into this exception.

## Capacity rule

The candidate may consume ADR-199's one-instance rollout grant, but every
physical RAM, transactional node, vCPU, and sustained-CPU gate remains in
force. If the candidate cannot be admitted, reconciliation keeps the healthy
predecessor running and leaves the rollout pending. It never manufactures
headroom by parking the instance that currently carries production traffic.

## Consequences

- Deployment status, gateway route adoption, and request draining are now
  separate observable phases rather than one optimistic finalisation step.
- A disconnected gateway or telemetry gap delays completion and temporarily
  retains both generations. This costs capacity, but preserves availability.
- Long-lived requests can hold the predecessor beyond the 25-second attempt;
  later reconciliation retries instead of terminating them at the deadline.
- The guarantee covers controlled rollout cutovers. Simultaneous host loss,
  application-level state incompatibility, and gateway processes absent from
  the compute-node registry remain separate failure domains.

## Rejected alternatives

- **Sleep for a fixed grace period:** time does not prove route adoption and
  cannot distinguish an idle predecessor from missing telemetry.
- **Finalize, notify, then drain:** an asynchronous cache refresh leaves a
  window where a gateway selects a deployment whose instances are retiring.
- **Park one predecessor to admit the candidate:** restores progress on an
  exact-fit node by creating the outage the rollout exists to avoid and
  violates ADR-199's capacity-pressure semantics.
- **Treat connection count as the barrier:** open keep-alive or unrelated
  connections are not the same as active requests; vmmd's per-instance
  in-flight request count is the relevant signal.
