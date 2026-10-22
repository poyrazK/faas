# ADR-1254: Consent-scoped management MCP resource subscriptions

## Status

Accepted. Extends ADR-1250 and ADR-1253.

## Decision

Support MCP 2026-07-28 `subscriptions/listen` for canonical app release, passive
health and scoped operational budget resources. Explicit requests alone begin
polling. Connection context and inventory resources are not subscribable. Legacy
clients retain resource reads; initialization does not advertise legacy subscription
support, and legacy `resources/subscribe` is rejected. No persistent legacy session
is introduced into the stateless OAuth bridge.

Each listen lasts at most two minutes, accepts at most four distinct resource URIs,
and has a five-second interval after each completed poll. Limit each management
connection/grant to sixteen reserved resource slots and eight listen streams, with
at most four listen streams per SDK session. Polls use bounded customer API reads;
no VM probes, mutations, stored cursors, or implicit connection-time polling occur.
Acknowledgement metadata reports `gregale_subscription_expires_at`. Clients explicitly
renew after expiry; SDK clients unsubscribe before subscribing again to the same URI.

Pin immutable app/project/environment IDs before acknowledgement. Recheck identity,
consent and safe projections while polling and immediately before every recipient's
URI-only `notifications/resources/updated`. Hash meaningful resource state locally;
suppress observation-clock and moving-window timestamp changes while retaining health
freshness transitions. Coalesce duplicate publishers per recipient. No resource contents,
logs, credential values, or native diagnostics are copied into update notifications.

The SDK owns subscription registration, acknowledgement, stream routing and cleanup.
Use SSE on listen POSTs and propagate modern HTTP disconnection to the listen context.
Bound notifications by the recipient's live request context, not the SDK broadcaster's
detached context. Keep listener reservations until SDK unregistration completes to avoid
stale cleanup deleting a replacement listen. Expiry, unsubscribe, disconnect, API errors,
identity changes and grant revocation stop polling. Remote leases cannot exceed the
initiating access token, grant or issued credential expiry. Revocation permanently closes
that management instance to new subscriptions, including requests racing with revocation.

## Consequences

Clients can invalidate cached context and reread validated resources on change.
These are sampled observations, not a durable event log; intermediate transitions
can coalesce or be missed between polls. Restart requires explicit resubscription.
A failing resource ends its listen stream, including other resources in that stream.

The pinned Codex app-server exposes resource reads but no resource-subscribe method;
its event-stream API is a different contract. SDK checks cover actual resource
notifications and lifecycle behavior. Actual Codex checks continue to verify existing
stdio/OAuth discovery, resources and tools; no Codex subscription UI is claimed.
