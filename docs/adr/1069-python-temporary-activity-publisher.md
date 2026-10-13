# ADR 1069: Python temporary activity publisher

Status: Accepted; runtime validation deferred.

## Context

Python clients now track activity/presence but must implement temporary-signal refresh, rapid-update coalescing and orderly clear themselves. Existing user transports and backend HTTP routes already publish named signals.

## Decision

Export a handwritten RealtimeActivityPublisher taking an async authenticated sender plus name, TTL and throttle interval. Use one running asyncio loop and one owned task. Copy JSON on ingestion, retain only the latest value, throttle updates, and refresh at half TTL until clear/close. Bound data to 2048 UTF-8 bytes, TTL to 100–30000 ms and throttle to 50 ms..half TTL. Defaults are 5000/250 ms. Bound each write to five seconds.

Clear stops scheduling, waits for the active write and sends null/TTL zero under a five-second total budget. Reject updates during clear; healthy publishers may restart afterward. aclose disposes, optionally clears remotely, cancels/joins owned work and prevents future updates. Transport failure stops refresh and exposes a sanitized error plus optional synchronous callback; suppress callback failures. Failed publishers reject updates. Never retry ephemeral writes automatically.

Provide create_managed_realtime_activity_publisher for existing channel/scoped HTTP routes. HTTP publishes as backend, not a presence user, and retains existing deploy-write/MFA checks. The generic sender supports caller-owned user transports without introducing a connection layer. Preserve helper/export generation.

## Consequences

Applications must explicitly clear activity when it stops, await close before closing transport, avoid concurrent clear/close, and provide cancellation-cooperative senders. Remote expiry handles interrupted clears; slow writes or event-loop blocking may exceed TTL. Acceptance is best effort, not subscriber acknowledgement. Backend signals remain excluded from user presence summaries. No route, grant, schema or migration changes. Static lint/compilation, AST/interface/export checks and diff checks are used; runtime validation remains deferred by user request.
