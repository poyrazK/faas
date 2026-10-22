# ADR 1066: Python worker graceful draining

Status: Accepted; runtime validation deferred.

## Context

Deployment shutdown should let healthy handlers finish without signalling them to stop or abandoning leases. The existing stop event deliberately interrupts processing cooperatively.

## Decision

Add optional `drain_event` to both Python worker entry points. Latch a separate internal drain event; never clear it. Stop issuing claims after observing drain, wake admission/poll waits and flush terminal batches immediately. Keep handlers, renewal, lease/deadline monitoring, processing timeouts and heartbeats active. Do not set context stop events merely for drain. Wait for active handler tasks including confirmed settlements and callbacks before exiting. An in-flight claim is not cancelled solely for drain, avoiding lost responses with unknown leases; process returned leases normally. Pre-set drain returns without HTTP calls.

Emit one draining lifecycle event and final shutdown reason drained on successful drain. Preserve failure, cancellation, blocker and urgent-stop precedence. A separate observer wakes drain-aware polling; monitoring waits do not wake on drain, preventing renewal spin loops. Join both signal observers and existing owned tasks in cleanup. New terminal batches during drain flush immediately.

Stop events and task cancellation override drain through existing cooperative stop/release rules. Infrastructure/lease loss remains fatal and retention/capacity blockers remain recovery errors. Drain does not change readiness heartbeat semantics, pause the server group, or drain its entire backlog. Clearing the caller event does not resume consumption.

## Consequences

Drain duration includes in-flight long polling and all owned work; no automatic deadline is imposed. Noncooperative handlers can delay it indefinitely. Applications can use processing timeouts or explicit urgent stop signals. No routes, permissions, generated schema or migration change. Static lint, compilation, signature inspection and diff checks are used; runtime validation remains deferred at user request.
