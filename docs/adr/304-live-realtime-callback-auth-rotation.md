# ADR-304 · Live managed realtime callback credential rotation

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** An endpoint registration update changes the callback bearer
  token used by future events on existing connections. Pending durable
  callback records retain the token captured when they were queued, including
  across restart. During rotation, the callback handler should accept both
  credentials until the pending queue drains; dead letters should be reviewed
  before revoking the old token if manual replay may be needed.
- **Why:** Realtime connections can outlive an endpoint update. Keeping their
  original callback token made a token PATCH ineffective for those sockets
  until reconnect or the 24-hour maximum connection age. Rewriting queued
  records would silently change the credential context of already-created
  events and add a disk-capacity failure mode during rotation.
- **Consequences:** Active connections need an atomic callback-credential
  snapshot separate from their immutable admission policy. Endpoint
  reconciliation updates this snapshot and the outbox's credential for future
  enqueues. The outbox preserves credentials on pending and dead-letter files.
  The operations guide documents the overlap period.
- **Rejected alternatives:** Closing every connection on credential update
  creates unnecessary client disruption. Rewriting all queued records changes
  the auth context of persisted events and can exceed the pending-byte limit.
