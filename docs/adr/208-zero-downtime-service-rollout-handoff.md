# ADR-208 · Acknowledged routing handoff for zero-downtime service rollouts

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** A readiness-gated service rollout completes through an explicit
  four-stage handoff: publish candidate-only weights while retaining the
  predecessor as live, collect a generation-tagged acknowledgement from every
  active serving gateway, observe every predecessor instance at zero in-flight
  requests for a post-ack quiet window, then supersede and park the
  predecessor. When two complete cohorts do not fit, verified traffic steps
  withdraw only the predecessor replicas whose capacity has been replaced,
  through the same acknowledgement and drain barriers.
- **Why:** Readiness alone proves that the candidate can serve; it does not
  prove that every gateway has stopped selecting the predecessor or that
  requests selected before the route refresh have finished. The old code
  finalised the deployment and parked the predecessor before the asynchronous
  `deployment_changed` refresh reached gateways. Under physical capacity
  pressure it also parked one healthy predecessor first and retried admission,
  contradicting ADR-199's requirement that the rollout hold when the bounded
  surge cannot fit. Both paths create avoidable outage windows.

## Handoff protocol

With enough admission headroom, the complete cohort follows the protocol below.
At a concurrency or physical-capacity ceiling, a partially ready cohort instead
uses `BeginServiceRolloutStep`. The candidate receives
`floor(100 * ready / desired)` percent; its pinned predecessor retains the
complement. The traffic writer repeats readiness and policy checks for this
exact step. Each binding request records its percentage and a fresh request ID,
so an earlier check cannot authorize a larger share or the final cutover.

Candidate readiness rounds its weighted target up; predecessor allocation
rounds its complementary target down, retaining at least one ready predecessor
until the final cutover. The store locks the generations and replica identities,
rechecks both cohorts, and atomically records excess ready predecessor replicas
as `draining` with their IDs in the durable handoff. Those rows stop accepting
new routes but keep their RAM and concurrency reservations. Only after gateway
acknowledgements and fresh quiet telemetry for those specific replicas may
schedd destroy them and admit the next candidate replicas. A restart repeats
these barriers for the recorded identities. Merely publishing a smaller
predecessor traffic weight never authorizes an ordinary surplus park.

An abort retains outstanding withdrawals until reverse routing and drains
complete. The predecessor always retains a ready recipient; final promotion
still requires the full candidate target. Missing capacity, ACKs, readiness,
telemetry or successful destruction holds reservations for retry. Converge the APID and schedd fleet before admitting new bounded service
rollouts. Percentage-specific requests require the updated APID worker; current
writers reject a full-cutover call against a pending partial-step request.

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

Operator abort uses the same protocol in reverse. The API records an
idempotent `abort/pending` intent instead of changing traffic or rollout state.
schedd restores the recorded predecessor's weight while retaining the
candidate as live, waits for the predecessor route generation to be
acknowledged, drains requests selected on the candidate, and only then marks
the candidate aborted and superseded. Generic `advance` and `promote` recovery
remain invalid for zero-step service rollouts.

The deployment's bounded `service_rollout_handoff` JSON object persists the
action, phase, predecessor, route generation, expected/acknowledged/missing
gateways, retry count, last error, and barrier timestamps. API, CLI, and the
Safe-releases dashboard expose that object. schedd also emits a closed-label
phase-duration histogram; timeout/error observations page through the
`FaasServiceRolloutHandoffBlocked` alert. These are operational projections of
the database state, not a second coordination mechanism.

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
- **Park a predecessor before the candidate serves verified traffic:** restores
  progress on an exact-fit node by creating the outage the rollout exists to avoid and
  violates ADR-199's capacity-pressure semantics.
- **Treat connection count as the barrier:** open keep-alive or unrelated
  connections are not the same as active requests; vmmd's per-instance
  in-flight request count is the relevant signal.

## Service readiness and deployment settings (2026-11-05)

A RUNNING row alone does not authorize a rollout advance. The scheduler and
traffic writer require every configured primary app and primary ingress probe
to have a ready observation for the exact app, instance, wake and node. Missing
or withdrawn evidence holds the predecessor. Unready running replicas still
reserve capacity, preventing duplicate admissions while probes recover.
Deployments without continuous probes retain their boot readiness contract.

The ten-minute service readiness budget starts at the first transition to LIVE,
after build and artifact preparation. Repeated activation or recovery does not
extend it. The Postgres transition trigger also covers older writers during a
rolling upgrade; MemStore mirrors that transition.

Rollout and recovery resolve each deployment's pinned environment settings and
frozen runtime, including execution mode and desired replica count. Desired
head edits do not rewrite a deployed generation's recovery revision. Background
recovery listing and claims include pinned service generations even when the
shared app uses request mode.

Runtime admission captures the same pinned VM shape used by schedd. A shared
RAM/runtime/protocol edit cannot invalidate a scoped replacement boot. Current
application-standard enrollment, control settings, signers and artifact fences
retain their existing authorization sources.

## Preserving capacity and readiness during handoff (2026-11-05, amended 2026-11-06)

A dark candidate contributes no serving capacity. Its ready replicas must not
reduce the predecessor's declared target. The controller repairs the serving
generation first under the existing concurrency grant, node, CPU and physical-
memory gates. When two complete cohorts cannot fit, a ready partial candidate
may take a verified traffic share. Only then may the durable withdrawal, route
ACK and request-drain barriers retire the replaced predecessor capacity. The
first surge must still fit, and final promotion requires the complete candidate
cohort. Candidate readiness never manufactures extra quota or requires room for
two complete cohorts.

Readiness remains reversible after weights are published. Route-ACK and drain
waits monitor the candidate's required probes. A withdrawal persists an abort
intent before restoring traffic and resumes the existing reverse ACK/drain
protocol, including binding-release enforcement and restart fencing. Loss found
by the final traffic writer uses the same recovery. A later ready observation
cannot erase that durable abort direction.

A reverse handoff also requires a usable recipient. Missing continuous probes
retain the boot readiness contract; they do not permit routing to zero running
service replicas. When the predecessor lost capacity during promotion, schedd
repairs it under the existing admission gates before reversing traffic. A failed
repair keeps the surviving candidate and durable abort intent. The traffic
writer repeats the recipient check before routing and before final teardown,
so a gateway acknowledgement alone cannot authorize removal of the last usable
candidate. Once a recipient is ready, the existing binding, ACK and drain
barriers still close the reverse handoff.
