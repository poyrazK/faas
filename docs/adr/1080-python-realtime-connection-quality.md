# ADR 1080: Python realtime connection quality

Status: Accepted; runtime validation deferred.

## Context

Connection status does not explain delayed updates. Applications need latency/silence observations without a second heartbeat or reconnect implementation.

## Decision

Export immutable RealtimeConnectionQuality and a tracker in the existing generator-preserved status module. Observe authenticated readiness with increasing attempt IDs, incoming frames, actual heartbeat sends and correlated responses. Use monotonic timing and bounded single-probe storage. New probes replace the correlation token but retain the oldest unanswered timestamp. Ignore stale attempt/probe callbacks.

Expose unknown/healthy/degraded/offline, latest latency, receive age and oldest heartbeat pending age. Configure degradation/recovery latency (1000/500 ms defaults, finite 1–60000; recovery <= degradation), silence (30 seconds) and pending timeout (10 seconds), each finite 0.1–86400. Hysteresis keeps high-latency degradation until a sufficiently fast response. Frames clear silence only; heartbeat responses update latency and clear pending age. New attempts reset observations. Offline retains diagnostic latency but no live age.

Emit synchronous isolated callbacks on quality transitions/new attempts/valid samples. snapshot reevaluates silence without introducing a timer; callers drive it through existing ticks. Dispose marks offline and prevents new attempts. No heartbeat, transport retry, disconnect action or server failure inference is introduced.

## Consequences

Applications can show degraded live updates while retaining control of connection policy. Quiet transports or blocked event loops affect local observations; correlation must be genuine. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export checks and diff checks verify implementation; runtime validation remains deferred by user request.
