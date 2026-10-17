# ADR-1541 · Negotiated Node post-response execution completion

- **Status:** accepted; runner/adapter foundation, production activation disabled
- **Date:** 2026-10-16
- **Decision:** Separate HTTP response completion from execution completion in
  persistent Node adapters. A worker advertises `completion_version: 1` in its
  ready handshake. Trusted runner wiring may then include a `background`
  envelope policy (`timeout_ms`, `max_tasks`). This contract uses request IDs
  even when interpreter concurrency is one. Capable workers use ID routing
  for legacy requests too; those still complete at the response and use
  ADR-1540's cancellation acknowledgement fence. An unsupported adapter fails
  before receiving the invocation. Without the policy, adapters retain their
  existing response protocol and legacy `waitUntil` scaffold.
- **Why:** The scaffold only observed rejected promises. A response released
  its worker slot while detached promises could still run, and the legacy
  buffered-response path drained before returning HTTP. The terminal-only
  host tail path has no production registration path that pins these promises.
  It cannot be treated as proof of post-response instance lifetime.
- **Limits:** One `BackgroundRuntime` per runner derives task timeouts,
  per-invocation active-task limits and aggregate admission from
  `pkg/api/limits.go`: Free/Hobby/Pro/Scale timeouts are 5/15/30/60 seconds;
  aggregate task capacities are 4/16/64/256; the structural invocation cap is
  16. All interpreter processes share the runtime's counter. Accepted tasks
  remain counted until settlement or acknowledged cancellation/worker death.
  Timeout alone never frees admission. No new quotas are introduced.
- **Activation boundary:** Production runner mains leave the runtime nil.
  Request bodies, headers, environment variables and manifests cannot enable
  this contract. This decision neither emits legacy host terminal events nor
  updates `instances.tail_count`. It does not implement VM pinning, snapshot
  quiescence, fleet telemetry, Python background execution or durable work.

## Protocol and lifetime

The Node adapter emits request-tagged `background` frames, each containing a
positive, monotonically increasing task ID and `register`, `timeout` or
`settled` event. Settlement has the closed outcome set `completed`, `failed`,
`timeout`. Duplicate IDs, inconsistent terminal events and completion with
active tasks fail closed. Task IDs are never reused; only active tasks occupy
the bounded map. Control frames never enter the HTTP body.

`ctx.waitUntil(value)` observes a real promise immediately, including rejection.
Registration is synchronous and does not promise that the computation has not
already begun: a promise passed to it may already have side effects. The runner
can refuse aggregate capacity after the handler has run. Before response headers
are sent, that refusal yields a terminal `handler_error` rather than a retryable
infrastructure error. After HTTP completes it cancels execution without replaying
the handler. The per-invocation adapter limit also throws a terminal handler
error. During an unfinished stream, a control failure aborts the body under
the existing stream error contract; its status cannot be changed retroactively.
Applications still need idempotency for requests whose response is incomplete
or unknown. Rejection of an admitted task leaves an already sent response unchanged.

Each promise has its plan timeout from registration. After a complete response
or streamed `end`, one fixed post-response deadline also bounds nested
registrations; registering a child never extends it. Registration closes after
execution completes, cancellation, or this deadline. Timeout aborts that
invocation's `ctx.signal`. Promise cancellation is cooperative: the adapter
waits for actual settlement, including abort cleanup, rather than resolving
execution using `Promise.race`. It emits `complete` only after all admitted
promises and their registered children have settled. Arbitrary detached promises
and a rejected registration receive no lifetime guarantee.

The HTTP owner consumes response frames and control frames sequentially. Once
the buffered or streamed body finishes, it hands its reader to a bounded
background context independent of HTTP handler return. HTTP/1 response EOF and
HTTP/2 END_STREAM therefore do not wait for background completion. The worker
slot remains occupied until `complete`. Healthy release without that frame is
converted to cancellation. Disconnect during the response also cancels the
invocation; returning a completed response does not.

Cancellation suppresses frames, aborts the signal, and acknowledges only after
handler, stream and registered promise cleanup have completed (ADR-1540). The
existing five-second cancellation grace retires an uncooperative worker and
interrupts its neighbours. Unconsumed task outcomes are recorded as interrupted
after acknowledgement or retirement; the runner does not invent successful
settlement. A closed stdout pipe is not an acknowledgement: release waits for
process exit even if the demultiplexer retires the worker concurrently.
A recycle changes worker generation and retains busy old workers
until their executions finish. This remains within the existing four-process
footprint. Worker death after HTTP completion never automatically replays work.

Runner lifecycle events contain invocation/task identifiers and closed-set
phases/outcomes; they contain no promise values, exception messages, SQL or
request payloads. These process-local observations are not host metering or
fleet qualification.

## Qualification and next activation gate

Portable tests drive the generated Node adapter through the production runner
and H2C listener. Explicit gates prove early HTTP/1 and HTTP/2 completion for
buffered, streamed and Fetch responses, neighbour context isolation, nested
tasks, rejection, timeout/disconnect cleanup, aggregate and structural limits,
single-slot negotiation, worker replacement, recycle retention and no replay.
See the [acceptance procedure](../ops/function-backend-concurrency-acceptance.md).

Production activation requires an owner-mediated, acknowledged VM lifetime
registration path before HTTP ownership is released, idempotent terminal
accounting across transport failure/restart, and a VM-wide quiescence barrier
covering every interpreter process. Checkpoint, idle reaping and worker-pool
draining must agree with that registration state. Native KVM evidence must
exercise real capture/restore/thaw, deadline expiry, lost acknowledgements,
worker/runner death and resource leak checks. Until that gate exists and passes,
keep production activation disabled. Durable queues/workflows remain the
appropriate mechanism for work that must survive process or VM replacement.

**Rejected alternatives:** Holding the HTTP handler open delays HTTP framing
completion, even after flushing a body. Treating abort or a timeout race as
completion admits replacement work during cleanup. Counting only one worker's
tasks misses other interpreters. Sending terminal events without acknowledged
host registration can release an unowned count and cannot pin a VM.
