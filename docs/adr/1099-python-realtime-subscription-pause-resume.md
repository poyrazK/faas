# 1099: Python realtime subscription pause and resume

## Decision

Add asynchronous `pause()` and `resume()` to `RealtimeChannelSubscription`.
Pause retains the handle, filter identity, completed cursor and channel capacity
reservation while removing its wire subscription. Other channels continue on the
shared socket. If this is the last active channel, existing pool cleanup closes
the socket. Resume starts a new recovery run using the same completed cursor;
`wait_ready()` establishes replay completion before live readiness is reported.

Expose recovering, live, pausing, paused, failed and closed through handle `state`.
A pause request immediately removes readiness. Concurrent pause calls share one
owned pause task; caller cancellation does not cancel that task. Resume joins a
pending pause and is idempotent for an already running subscription. Fatal
processing errors stay terminal; resume is not an error reset.

Implement the processing boundary in `RealtimeRecoveryCoordinator.pause()` so
receive-only SSE and dedicated WebSocket users can also pause, then call `run()`
again to resume. Idle transport/retry work is cancelled promptly. An active
processor is allowed to finish; its cursor advances only after success, then
session cleanup runs. Existing replay deadlines still apply. A hung live processor can keep pause pending; explicit run
cancellation or unsubscribe remains available. Pause must be requested outside
the processor/run task to avoid self-join deadlock.

Pause does not persist cursors or promise flushing a durable ACK for the final
processed event. Application effects and cursor persistence should share a
transaction or be idempotent. Resume uses ordinary retention-gap snapshot
recovery; without a snapshot loader an expired cursor fails explicitly. Presence
and read-progress observations are invalidated by session cleanup and rebound on
resume, without disposing their helpers. Explicit unsubscribe still disposes them.

Existing unsubscribe barriers protect replacement sessions from trailing frames.
If server unsubscribe fails, the existing pool may close the physical socket and
other channels recover; normal channel pause does not interrupt other channels.

## Validation

Static lint, Python compilation, SDK AST and whitespace checks. Runtime tests,
network simulations and concurrency tests remain deferred at the user's request.
