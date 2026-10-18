# ADR-1385: Refresh authorization for retained WebSocket channels

- **Status:** accepted for implementation; deployed fleet qualification pending
- **Date:** 2026-10-15
- **Amends:** ADR-369 and ADR-1190

## Context

Retained WebSocket channels ask the application's authorization callback when a
subscription starts. The history pump and queued presence/signal frames can
outlive that decision. Removing an organization membership, banning a user, or
ending all of their Auth sessions therefore does not revoke an existing channel
subscription. SSE already refreshes the application decision every 30 seconds.

## Decision

Reuse SSE's existing 30-second positive authorization lifetime for retained
WebSocket channels. Each subscription owns one bounded authorization record.
History polling checks it even when the channel is idle. The WebSocket writer
also checks it before sending channel messages, presence, signals, read receipts,
and checkpoints, so buffered output cannot extend an expired permission grant.
Commands using an active channel subscription refresh the same grant before
changing presence, signals or read progress.
Concurrent checks share one callback; no callback runs under connection or
subscriber-index locks. Endpoint policy changes invalidate the cached decision.

A definitive callback denial removes the affected subscription and its presence
and route registration, then emits `access_revoked`. Callback failures end the
connection with a retryable close rather than asserting a permanent denial.
HTTP 401/403 are denials; other non-2xx callback responses are unavailability.
An expired grant never authorizes output while its replacement is being checked.
The idle cleanup delay includes the existing history poll interval and callback
deadline; an already written frame cannot be recalled.

Ticket subscriptions retain their existing one-second durable checks. Legacy
management subscriptions and inbox/direct messages keep their existing contracts.
This change does not enable previews or qualify a deployed fleet.
[ADR-1332](1332-session-bound-retained-realtime.md) subsequently adds periodic
retained WebSocket JWT verification and replaces Auth's subject-wide callback
check with the verified token's particular session.

## Validation

Regressions must cover removal from an idle channel, buffered output after grant
expiry, callback denial versus outage, concurrent refresh, endpoint policy/token
changes, cancellation, ticket behavior, and continued delivery to authorized
subscriptions. Run the realtime package and its race checks. Before production
qualification, exercise these cases across deployed nodes and proxy transports.
