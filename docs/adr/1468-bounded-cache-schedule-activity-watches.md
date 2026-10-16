# ADR-1468: Bounded MCP cache schedule activity watches

Status: Accepted

## Context

Agents consuming the durable schedule activity feed need to wait for events
without issuing repeated MCP calls or losing their saved checkpoint.

## Decision

Expose a read-only watch tool using the existing validated native feed reader.
Poll once per second within a default 20 second, maximum 45 second request.
Return the first nonempty bounded page immediately. An empty timeout retains the
supplied checkpoint; an initially omitted cursor is anchored to the first scoped
empty page. Every observation rechecks current consent, account and immutable
environment identity. No locks span waits and no background subscription persists.

Cancellation and earlier request or authorization deadlines fail the request.
Only the watch's own deadline may return a successful timeout, and only after a
fully validated empty observation. Failed reads never advance the checkpoint.
Execution events remain separate from gateway convergence evidence.

## Consequences

Reconnects reuse the last consumed cursor and preserve at-least-once consumption.
The tool adds bounded native reads, no new database migration or SDK protocol.
The durable feed's writer upgrade and baseline discovery requirements still apply.
