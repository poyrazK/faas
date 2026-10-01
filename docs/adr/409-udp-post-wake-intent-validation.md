# ADR-409: UDP intent validation after wake

## Status

Proposed; target resolver review and public rollout remain pending.

## Context

Scheduler admission may wait for a guest wake. A listener can be disabled, replaced or deleted, or its app changed, while admission is pending. Validation before admission alone cannot reject that stale intent at completion.

## Decision

Share app ownership, maintenance, listener identity and current manifest-port validation between pre-admission and post-wake checks. Before returning a cold-path target, re-read and validate current app/listener intent using the admission context. Reject canceled contexts and failed reads. Also reject a canceled instance-list result before warm selection or scheduler admission, even when the source returns buffered rows successfully; reject nil contexts at the resolver boundary. Do not mutate scheduler-owned instance state or attempt to undo a wake from the edge.

## Consequences

Disabling intent during a successful admission now rejects the target before forwarding. The regression uses the actual memory store with an admitter that disables the listener before returning. This is a point-in-time validation, not a database transaction spanning forwarding; supervisor retirement remains responsible for closing established peers after later intent changes. Native lifecycle, production exposure and deployed recovery qualification remain separate. Guest storage stays stateless and Linux/amd64 remains the target.
