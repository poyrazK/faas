# ADR-1033: Storage client leases and retirement

Status: Accepted

## Context

ADRs 968, 972 and 979 provide managed storage clients and reload coordinators.
They fence new acquisition on rotation, revocation and failed adoption, while
applications must manage retired resources themselves. Closing an old transport
at rotation can interrupt a streaming body; retaining every old client leaks
resources. A fenced factory can also return an unpublished owned candidate.

## Decision

Add request leases to the Go, Node and Python storage bindings. A lease pins one
client/bucket pair until idempotent release, including shallow copies. Released
leases cannot reacquire their pair. Applications hold leases through upload,
download and response-body closure, including error and cancellation paths.

Automatic retirement is opt-in through an application callback, preserving
caller-owned raw acquisition by default. With retirement enabled, reject raw
Current/current acquisition so all borrowers are tracked. Rotation/invalidation
retires the old record immediately; cleanup starts once its last lease releases.
Dispose returned but unpublished candidates after cancellation or invalidation.
Go also disposes valid nonnil factory results returned with an error. Factories
own partial allocations until an owned result is returned.

Require fresh, exclusively owned factory results and underlying resources.
Reject obvious identity reuse of a still tracked client without disposing the
alias. This is a diagnostic, not proof that underlying pools are independent or
that a previously disposed object cannot be reused.

Run each cleanup callback once outside state mutation. Different clients may
retire concurrently; hooks must bound their work and be thread-safe where
applicable. Go uses goroutines, Python synchronous hooks on daemon threads, and
Node supports async hooks without blocking the event loop. Sanitize callback
errors/panics and optional observer failures. Track cleanup failure as sticky;
never retry uncertain partial destruction.

Provide a bounded/cancelable retirement wait for admitted factories, retired
leases and scheduled cleanup, ignoring the active client. Shutdown must stop
application admission/reloads and stop the coordinator or invalidate the binding
before draining. A canceled wait never destroys outstanding leases or stops
cleanup; application exit policy owns resources that outlive its budget.

## Consequences

Compute workloads can adopt credential rotations without prematurely closing
long transfers or indefinitely retaining successfully retired clients. Returned
candidates rejected by epoch/cancellation fences are reclaimed. Managed clients
require application adoption of leases and correctly bounded retirement hooks.
Forgotten leases and uncooperative factories/hooks can delay draining; the SDK
does not force destruction. Keeping a client alive does not bypass credential
revocation at the gateway or prove provider readiness. Existing ACKs retain
their exact application self-attestation semantics.

No guest, API, scheduler, provider, accounting or release-policy behavior changes.
SDK tests use disposable local HTTP bodies and guest protocol fixtures; they do
not qualify real providers or a deployed release.
