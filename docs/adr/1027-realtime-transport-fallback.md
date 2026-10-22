# ADR-1027: Receive-only SDK transport fallback

Status: operator preview

## Context

WebSocket subscriptions and HTTP SSE are available independently. Receive-only
apps need one SDK API that handles WebSocket-blocking networks without managing
two transports and divergent cursors.

## Decision

Add `consumeRealtimeChannelWithFallback` to main and browser SDK entries. Reuse
existing v2 channel consumption and SSE streaming. Start with WebSocket; after
bounded consecutive failed connection/subscription attempts, cancel and await
that phase, then use SSE for the remainder of the invocation. No simultaneous
transport consumption, automatic upgrade back to WebSocket, multiplexing,
metadata filtering or presence/inbox/write fallback is introduced.

Both transports expose the same retained message shape with decoded bytes and
mutation metadata, excluding the WebSocket-specific messageId. Channel+sequence
is the cross-transport identity. The existing numeric cursor store is shared;
processing resolves before cursor persistence, and persistence completes before
switching. Only safe JavaScript integer sequences are supported by this unified
API. Direct SSE retains its separate decimal-string cursor contract.

Each WebSocket connection/subscription attempt has a deadline (default 5s), and
two failed attempts trigger fallback by default. Both knobs are bounded. Thirty
seconds after a subscription acknowledgement resets the failure streak. Explicit
policy/protocol close codes, protocol frames, credential provider failures and
HTTP authorization failures are terminal. Browser handshake failures hide status;
SSE may be attempted after such failures, but must authenticate independently and
terminates on 401/403. No permission is widened during fallback.

Ticket expiry uses normal WebSocket closure so renewal is distinguishable from
policy rejection. Credential providers run per reconnect. Callback/cursor failures
and retained-history resync requests never trigger transport downgrade. The same
optional onResync hook rebuilds a snapshot and persists its returned sequence
before reconnecting on either transport. onTransportChange reports selection,
not an online/health guarantee.

## Consequences

At-least-once processing still requires idempotent application handlers. Async
callbacks and cursor-store writes cannot be forcibly cancelled; transport handoff
waits for them. SSE remains selected until the caller starts a new invocation.
Explicit browser allowed origins and existing operator server preview gates are
required. Node callers supply an authenticated WebSocket factory; the default
browser factory supports OIDC JWTs and subscription tickets. Runtime verification
is deferred per the session's testing preference.
