# ADR-1115: HTTP SSE subscriptions for retained realtime channels

Status: operator preview

## Context

Receive-only clients need an HTTP transport for managed realtime, including
networks that block WebSockets. Existing execution/log SSE streams do not expose
managed realtime channel history.

## Decision

Serve GET on the managed endpoint path with `transport=sse` and one `channel`.
Reuse the resume preview gate, private history RPC, OIDC policies, application
channel authorization, managed access controls, connection admission and node
subscription bounds. No additional daemon or Postgres client is introduced.

Client cursors are decimal strings. The `after` query, when present, overrides
`Last-Event-ID`. Replay is contiguous or terminates with `resync_required`.
Messages carry base64 data and retained metadata; SSE event IDs are sequences.
No silent cursor reset occurs. SSE subscriptions do not provide ACKs, server
checkpoints, inbox delivery, presence or ephemeral signals.

Streams run for at most five minutes. Identity verification and the application
channel callback repeat at most every 30 seconds per stream; durable denies and
ticket state are checked before each message and between polls against a snapshot
at most one second old, matching WebSocket access monitoring. A definitive deny
(callback refusal, changed principal, denied channel, revoked session, ticket
scope change) sends `access_revoked` and terminates; partial in-flight writes
cannot be recalled. Lookup failures, callback errors or timeouts, history read
failures and stream expiry end the stream without an error event, so clients
reconnect and the next request repeats full authentication. Polling runs at no
more than the existing resume poll interval and at most one second between idle
checks. Heartbeat frames keep idle streams active. Each write has the existing
write deadline; the server retains one bounded history page and has no outbound
queue. Both endpoint and node connection reservations and node subscription slots
are shared with WebSockets. SSE has no WebSocket connection directory entry or
connect/disconnect callback; its generated connection ID identifies authorization
requests only. Monitor HTTP streams and reserved admission rather than assuming
WebSocket inventory includes SSE.

Browser transport uses fetch with Authorization headers, explicit allowed origins
and bounded preflight support. Tokens never enter URL parameters. The browser SDK
reconnects transient failures with bounded jittered backoff and requests a new
token each time. Authentication, protocol and resync errors are terminal. Cursor
advancement occurs after the consumer resumes iteration; duplicate delivery is
possible. The caller handles resync from an application snapshot.

## Consequences

The gateway's managed-path reverse proxy already forwards ordinary HTTP and
flushes streamed responses. Other deployed proxy buffering/timeouts still require
qualification. Automatic fallback from WebSocket is not introduced; callers choose
this receive-only transport. Runtime/fleet qualification remains deferred and
preview gates must remain off until operator acceptance.
