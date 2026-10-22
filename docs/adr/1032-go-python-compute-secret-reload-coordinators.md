# ADR-1032: Go and Python compute secret reload coordinators

Status: Accepted

## Context

ADR-1031 provides a Node coordinator for atomic guest snapshots and fenced
application acknowledgements. Go and Python storage bindings still require
manual orchestration and hold their acquisition lock throughout client
construction. A slow readiness check can block new requests and shutdown; a late
candidate can reactivate a client after invalidation.

## Decision

Add equivalent Go and Python SDK coordinators, using the existing atomic
envelope, readiness and closed revision/status/generation ACK protocol. Capture
the four guest settings once, require them without legacy fallback, install
SIGHUP before readiness, and serialize initial, manual and signal application.
Coalesce signals with one pending follow-up. Wait up to thirty seconds for an
initial version; application callbacks own their separate readiness budgets.

Require every secret user to register application and synchronous, idempotent,
quick invalidation callbacks. Provide storage consumer adapters and immutable
snapshots. Disable all consumers before reading or applying a refresh and on
application failure, malformed snapshots, unconfirmed ACK or cancellation. ACK
applied only after every callback succeeds. Failed application can ACK only a
known valid revision as failed. A stale ACK rereads the authoritative projection
with at most five applications; transport errors and 503 responses retry an
identical metadata body up to ten times with five seconds per request. Dedicated
native transports do not use API wrappers, ambient proxy settings, redirects or
response-body decoding. Errors and snapshot representations omit credentials.

Separate each binding's state lock from its serialized factory gate. Invalidation
increments an epoch; late and already queued candidates cannot publish after
that epoch changes. New acquisition and invalidation remain prompt during
construction. Keep factories serialized even if they ignore cancellation.
Applications own previously acquired clients and retired-resource cleanup.

Go's Start context owns the workload lifetime. Cancelling an admitted manual
refresh is terminal to prevent overlapping or partially adopted callbacks;
cancelling before admission leaves another refresh alone. Stop cancels waiting,
unsubscribes its own SIGHUP listener and invalidates without joining factories.
Python runs synchronous application callbacks on a daemon worker, keeps direct
binding factories on their calling thread, and requires main-thread startup.
It refuses an existing callable SIGHUP handler. Stop disables immediately from
any thread; main-thread Stop restores the prior handler only while still owned.
External cancellation has a separate watcher so a blocked callback cannot delay
invalidation. Native Python HTTP can drain an uninterruptible DNS/connect call
after its deadline; it checks cancellation before starting a subsequent POST.

Preserve the new handwritten Python helper across generation and every failure
path. Run deterministic regeneration in an owned local fixture, avoiding deletion
or formatting of the package imported by the test runner and child workloads.

## Consequences

Go and Python compute applications can adopt bucket credential rotations and
revocations through their SDKs without assembling a guest protocol. Constructors
and ACKs remain application self-attestation; they do not prove provider
readiness, arbitrary consumer adoption or group-wide atomic publication.
Custom callbacks must honor cancellation before publication, fence late work,
and bound their own latency. Failed startup and shutdown are terminal; ordinary
refresh failures can recover on another signal/manual refresh. The coordinators
do not install termination handlers, close servers, exit, or change guest,
API, scheduler, accounting, release policies or ownership boundaries. Local
signal/file/metadata fixtures qualify the SDK lifecycle without deployment or
provider calls.
