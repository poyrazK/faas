# 1098: Python retained-channel SSE fallback

## Decision

Provide opt-in `RealtimeFallbackClient` around the native Python WebSocket factory.
After two retryable connection/subscription failures, recovery selects HTTPS SSE.
Missing WebSocket dependencies can select SSE immediately. Authentication and
configuration failures remain terminal. Probe WebSocket on the next reconnect
following a configurable cooldown; never interrupt a healthy SSE connection.

SSE is receive-only: inbox consumers, durable subscriptions, presence, read progress
and direct-message handlers are rejected in fallback configuration. Exact metadata
filters run locally, generating checkpoint events for excluded positions. Filter
fingerprints remain identical across transports. Application processing and cursor
persistence belong to the existing recovery coordinator.

Add authoritative string sequence/history bounds to the SSE `subscribed` frame.
This additive server contract supports frozen replay targets and retention-gap
snapshot recovery without falsely treating heartbeats as completed replay. Python
SSE requires the updated server; older handshakes fail explicitly. SSE messages
have no server message ID, exposed as `None`; channel sequence is their identity.

Use fresh header-only bearer tokens, HTTPS verification, no redirects or ambient
proxy credentials, bounded UTF-8 frames, strict message validation and bounded
coordinator retries. Diagnostics expose transport selection and failure counts,
without URLs, credentials, metadata or exception text.

## Validation

Static Python lint, compilation, AST/export checks and whitespace checks only.
Runtime tests and network simulations are deferred at the user's request.
