# ADR 1064: Python worker lifecycle hooks

Status: Accepted; runtime validation deferred.

## Context

Python applications need to connect SDK-managed realtime workers to their monitoring without handling payloads or lease credentials in telemetry callbacks.

## Decision

Export immutable `RealtimeWorkerEvent` and add optional `RealtimeWorkerOptions.on_event`, a synchronous callback returning None. Report claimed, settled, lease_lost and shutdown. Claim events carry the observed group UUID, sequence and attempt. Settlement/loss durations use the monotonic clock from the beginning of claim processing, including settlement queue/transport time. Confirmed settlements carry action/status. Shutdown carries stopped/cancelled/failed/blocked and the observed group UUID if available. Include no payload, metadata, worker identity, fences, tokens, credentials, failure codes or exception details.

Invoke callbacks inline without awaiting them or creating telemetry tasks. Suppress ordinary exceptions and callback-raised CancelledError so monitoring failures cannot change delivery/cancellation semantics. Close accidentally returned coroutine objects. Callbacks must enqueue into an application's bounded nonblocking queue and return quickly; blocking callbacks can delay the event loop and renewal. Do not suppress process-control BaseExceptions such as KeyboardInterrupt or SystemExit.

Emit settlement only after validated server confirmation, individually for successful batch entries. Receipt retries produce no duplicate telemetry. Uncertain outcomes do not produce confirmations. Lease loss emits at most once per claim, including immediate local expiry, renewal failure and batch lease-lost outcomes. Shutdown emits after owned-task cleanup and on an already-set stop event. Preserve generator barrel exports and handwritten module retention.

## Consequences

Monitoring remains best-effort process-local observation, not durable audit or exactly-once external delivery. Consumers should avoid unbounded metric labels. No routes, permissions, schema or migration change. Verify with Python lint/compilation, AST/interface/export inspection and diff checks. Runtime tests remain deferred by user request.
