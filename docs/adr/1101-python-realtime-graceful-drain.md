# 1101: Python retained-channel graceful draining

## Decision

Add `RealtimeRecoveryCoordinator.drain(deadline_seconds=30)` for dedicated native
WebSocket retained channels and `RealtimeChannelSubscription.drain()` for channels
on a shared socket. Return immutable `RealtimeDrainResult`: completion, completed
cursor, durable confirmed cursor, final ACK send status, deadline expiry,
unfinished buffered/in-flight event count and a safe error reason.

Freeze local admission at the last received position. Keep the wire subscription
active while processing already admitted events: server ACK validation requires
an active subscription. Frames received after the cutoff are validated but not
added to the application queue. Their events remain available for later replay;
they are excluded from unfinished local work. Filtered checkpoint entries count
as queue entries, not individual messages, and preserve existing coalescing.

After the final yielded event is processed, the iterator sends its ACK and waits
for durable checkpoint confirmation when configured. An empty frozen queue sends
a baseline ACK if needed, then terminates recovery using an internal drain marker.
Session cleanup closes a dedicated socket or removes the shared channel through
the existing unsubscribe barrier. Other channels normally continue uninterrupted.
Shared unsubscribe failure retains the existing physical-socket recovery behavior.
Direct-message, presence and read helper processing is outside the retained drain.

A channel handle becomes unready with state `draining`, then closes, disposes its
helpers and releases its registration. Repeated calls join the same owned result.
Pause/resume cannot race a requested drain. A coordinator can be run again after
its completed drain, retaining its cursor; closed channel handles require a new
subscription. Call drain outside application processors to avoid self-join.

The deadline covers the awaited recovery run, including its cleanup. On expiry,
cancel the run and join its owned cleanup with the existing timeout. Cooperative
application cancellation remains required; this is not a hard wall-clock bound
for a handler that suppresses cancellation. Cancellation of a drain waiter leaves
the owned drain running. Existing replay and checkpoint deadlines still apply.
Processing failure or transport loss during drain never reconnects to admit more
work. An incomplete result preserves the last successful processing cursor;
application effects/cursor transactions and idempotency remain caller concerns.

Final ACK send is a transport write assertion. Durable confirmation is separately
reported. Local queue exhaustion alone cannot produce success: require the drain
marker, successful final ACK, and durable confirmation when applicable. Preserve
run errors even when the channel wrapper captures them instead of rethrowing.

SSE and inbox consumers reject drain explicitly. Their completion/checkpoint
contracts need separate implementations; no guarantee is inferred from closing
an HTTP response or sending an unconfirmed inbox ACK.

## Validation

Static lint, formatting, Python compilation, SDK AST, public exports, generator
preservation and whitespace checks only. Runtime ACK, deadline and concurrency
checks remain deferred at the user's request. No server/schema changes required.
