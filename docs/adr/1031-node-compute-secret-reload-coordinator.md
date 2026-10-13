# ADR-1031: Node compute secret reload coordinator

Status: Accepted

## Context

Compute storage bindings can rebuild clients from a secret map, but applications
must assemble atomic snapshot reading, startup waiting, signal serialization,
readiness and generation-fenced acknowledgements themselves. Partial application
failure can leave an earlier storage client available; cancellation can allow a
late factory to publish after application shutdown. API fetch wrappers can also
retry or decode metadata ACK responses before the reload protocol sees them.

## Decision

Add a Node-only SDK `secrets/runtime` subpath with `SecretReloadCoordinator`.
Reuse the existing guest atomic envelope and closed ACK protocol without changing
guest, API, scheduler or state ownership. Require atomic snapshot, readiness and
execution generation contracts, capturing paths/endpoint/generation once per
coordinator. Legacy template helpers retain their compatibility behavior.

Install SIGHUP synchronously before publishing readiness. Serialize bootstrap,
manual refresh and signal refresh; coalesce signal bursts with one follow-up.
Bootstrap waits up to thirty seconds only for an initially pending revision.
Apply registered consumers in order and attest to their exact revision only
after all succeed. A stale ACK rereads the authoritative envelope with up to five
application attempts. Retry transient ACK transport failures and 503 responses
with the identical body up to ten times and five seconds per request. Use a
dedicated Node metadata transport, with no global fetch wrappers or redirects,
and discard response bodies. Error messages and callback notifications are
sanitized and never include underlying exceptions or secret values.

Consumers provide application and synchronous invalidation callbacks. Every
refresh invalidates old acquisition; application failure, invalid projection,
unconfirmed ACK or shutdown disables all registered consumers. Object storage
provides a consumer adapter and an invalidation epoch that fences late/queued
candidates. Previously acquired clients remain owned by their callers.

Successful start means accepted application self-attestation, not a provider
readiness proof. Register all secret users and supply appropriate client readiness
checks. Callbacks must honor cancellation before publication and bound their own
work. Shutdown is terminal, removes only the coordinator's signal listener,
aborts waiting and never awaits arbitrary application code, destroys clients or
exits the process. Failed startup also terminates the coordinator; ordinary
reload failures can recover on a later signal/manual refresh.

## Consequences

Node compute applications can integrate storage rotation without importing a
CLI template or assembling the protocol. New acquisition may be unavailable
during reload, and consumers are not atomically published as a group. ACKs only
attest to registered callbacks and cannot prove arbitrary application adoption
or cancel a request already sent. Custom consumers remain responsible for safe
invalidation, cancellation and retired-resource lifetime. Go and Python retain
manual coordination until equivalent language-specific helpers are implemented.
Local fixtures qualify the runtime protocol; they are not production or live
provider qualification. Release policies and existing server-side ACK fences
remain independent and unchanged.
