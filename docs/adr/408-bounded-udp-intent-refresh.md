# ADR-408: Bounded UDP intent refresh

## Status

Proposed; supervisor review and public rollout remain pending.

## Context

An unbounded durable-state read can prevent the UDP edge from processing later listener changes. Cancellation during the initial read previously counted as a reconciliation failure, unlike cancellation during periodic reads.

## Decision

Each listener-intent read receives a child context bounded by the five-second UDPListenerReadTimeout in pkg/api/limits.go. Reject results returned after the read deadline or parent cancellation before applying any socket changes. A read failure preserves the last successfully validated socket set; the periodic loop reports the failure and retries. Initial read failures fail startup. Parent cancellation during startup is clean shutdown and does not emit readiness or increment reconciliation failures. State-source implementations must honor context cancellation; the supervisor does not spawn detached reads to hide a non-cooperative source.

## Consequences

Portable tests establish that the source receives a bounded deadline and that initial-read cancellation joins startup without readiness, binding or failure metrics. Existing tests retain replacement ordering, retirement cancellation and shared peer admission. This does not prove database failure recovery on a deployed edge or native KVM lifecycle/leak acceptance. Storage remains stateless and Linux/amd64 remains the target.
