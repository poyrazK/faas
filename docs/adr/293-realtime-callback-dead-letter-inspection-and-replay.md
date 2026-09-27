# ADR-293 · Realtime callback dead-letter inspection and replay

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Add paginated metadata listing and explicit replay operations
  to realtimed's private management API. Listing is ordered by event ID and
  excludes callback payloads, URLs, and bearer credentials. Replay preserves
  the event ID and enqueue time, resets its retry state, and returns the event
  to the bounded pending outbox. Reject replay when pending capacity is full
  or a later callback for that connection is already in flight.
- **Why:** Dead letters were durable and bounded, but operators could only
  inspect files directly and had no supported way to retry a corrected
  callback. A metadata endpoint improves discovery without copying sensitive
  callback contents through management responses.
- **Consequences:** Node-local tooling can list and replay retained callbacks
  over the DAC-protected Unix socket. A replayed event can be delivered again,
  so handlers must deduplicate by the preserved event ID. Replay does not run
  automatically, and existing retention limits continue to apply.
- **Rejected alternatives:** Returning full records from the management API
  would expose payloads and bearer credentials. Automatic replay would retry
  poison callbacks without operator review and undermine the dead-letter
  boundary.
