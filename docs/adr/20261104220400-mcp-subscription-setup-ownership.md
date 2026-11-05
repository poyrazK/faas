# ADR-20261104220400: MCP subscription setup ownership

## Status

Accepted; extends ADR-20261104201707 and ADR-20261104212930.

## Context

Node Task/resource notification streams checked their process limit before
asynchronous task resource lookups, but reserved capacity and observed
disconnects only afterward.
Concurrent setups could exceed the limit, and disconnected requests could later
consume stream slots indefinitely in open auth mode. Asynchronous resource update
adapters could also return unsubscribe handles after cleanup had already run,
leaving backend listeners allocated.

## Decision

1. Reserve a stream slot synchronously before the first asynchronous lookup or
   registration, and observe response closure, request aborts and authentication
   expiry throughout setup. Pending setups count toward the existing process
   limit. Release the slot on cancellation and every post-reservation error.
2. Stop setup after cancellation. A resource registration or Task subscription
   that finishes late is disposed immediately rather than added to a closed
   stream. Cleanup owns each accepted handle once, removes transport listeners
   and queued messages, and continues when a disposal throws or rejects.
3. Application drain closes pending setups immediately. Established streams keep
   their terminal completion response, and empty-filter completion during normal
   setup still follows its acknowledgement. A disconnected response receives no
   further error or acknowledgement writes.

No platform schema, wire format or limit value changes are introduced. Async
resource adapters retain the `subscribe(uri, listener)` interface and return an
unsubscribe function directly or through a promise.

## Validation

Local HTTP regressions cover 160 concurrent setups against the 128-stream limit,
disconnects while task lookups succeed or fail, capacity reclamation before a
lookup finishes, late pub/sub registrations after disconnect or drain, late Task
subscription disposal, combined-filter validation failures, and synchronous or
asynchronous disposal errors. Every teardown case verifies that all 128 process
slots remain available afterward. The full Node suite and SDK interoperability
checks cover existing stream acknowledgements, updates and completion behavior.
