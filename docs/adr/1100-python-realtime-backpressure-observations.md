# 1100: Python realtime backpressure observations

## Decision

Add `RealtimeBackpressureMonitor` and immutable `RealtimeBackpressureStatus` for
retained WebSocket delivery. Each session/channel has its own monitor; callers can
provide one with synchronous change callbacks and configurable thresholds.
`RealtimeChannelSubscription.backpressure.snapshot()` exposes measurements, and
channel diagnostics include their whitelisted counts, state and elapsed times.
Dedicated clients accept `backpressure=monitor`; multi-channel clients require
configuration per subscription rather than a transport-wide shared monitor.

Measure queue depth, capacity, oldest queued age and cumulative overflow count.
Default busy/overloaded occupancy thresholds are 50%/90%, and age thresholds are
5/30 seconds. Either occupancy or age can elevate the state. Overflow records an
overloaded observation before existing reconnect recovery. Inactive is distinct
from a healthy active empty queue. Counters survive reconnects; queue observations
reset on disconnect, close or pause. Snapshot age uses a monotonic clock. With a
callback configured, one timer per monitor reports age threshold crossings even
when no new frames arrive; disconnect cancels it. Callback failures are isolated.

Store only one timestamp per buffered entry, bounded by the existing queue
capacity. Consecutive filtered checkpoints keep the original timestamp while
coalescing; splitting a checkpoint at the replay high-water restores its original
age to the suffix. Measurements exclude in-flight application processing, internal
WebSocket library buffers and server queues. They do not advance read markers,
change ACKs, pause delivery or change the existing overflow policy.

Add transport-independent `RealtimeProcessingStatus` through coordinator
`processing_snapshot()`. It exposes active handler age and last handler attempt
duration (including failed/cancelled attempts), excluding ACK time and queue wait.
This supplies local processing timing for SSE without inventing comparable SSE
queue depth or claiming server sequence lag. Diagnostics may include processing
observations independently of WebSocket backpressure.

## Validation

Static lint, formatting, compilation, SDK AST/export/generator preservation and
whitespace checks. Runtime timer, queue and concurrency tests remain deferred at
the user's request. No server or database change is required.
