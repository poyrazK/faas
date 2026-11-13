# ADR-20261113180307: Bind execution workflows to independent Node clients

- **Status:** accepted
- **Date:** 2026-11-13
- **Milestone:** SDK developer and agent integration
- **Amends:** [ADR-20261113173610](20261113173610-node-sdk-client-isolation.md)

## Context

Independent Node clients bind generated services to private configuration, but
execution convenience helpers still use shared `OpenAPI` and global fetch.
Agents need to submit, observe, reconnect, and collect a receipt through the
same client without accidentally switching accounts or endpoints.

## Decision

Expose `runExecution` and `watchExecution` on the isolated preview client.
Capture the client's immutable configuration at helper invocation, including
before a watch iterator is consumed. Use it for generated submission and final
receipt requests and for every SSE connection. Configuration updates affect
future helpers. A new watcher with a retained cursor can adopt rotated credentials.

Extract service binding and permit the internal client builder to attach helpers
before freezing the client. Execution helpers bind generated Runs methods to a
fixed configuration and use its private transport for event requests. Retain base
path prefixes and encode execution IDs as one path segment.

Share the execution loop with the existing standalone exports and legacy preview
`FaaSClient` methods. Their process-wide configuration contract stays available.
The default public factory continues to exclude these preview helpers.

Reconnect only the event request, retaining its cursor and skipping overlapping
identified frames. Never resubmit because a stream disconnected. Surface initial
connection, authorization, malformed-event, and callback failures. Caller abort
signals cancel submission, reconnect waits, pending stream reads, and the final
receipt request; they do not request server-side execution cancellation. Release
stream bodies on early exit and failure. The shared SSE reader cancels a pending
read on abort even when a custom fetch has not connected the signal to its body.

## Consequences and verification

Client-bound workflows can run concurrently alongside legacy helpers. Iterator
cursors are in memory; recovery across process restarts requires callers to retain
execution IDs and successfully processed cursors. Cursorless events may repeat.

Local HTTP tests exercise concurrent origins, credentials, endpoint prefixes,
resumed cursors, terminal artifacts, and one submission per logical workflow.
Additional tests cover configuration rotation, lazy iterators, detached methods,
cancellation at each I/O phase, stream cleanup, typed errors, and public/preview
type boundaries. Installed-package tests exercise the published runtime and types.
