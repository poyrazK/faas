# ADR-311 · Realtime callback outbox admission failure handling

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Distinguish failures to admit a callback into the durable
  outbox from delivery errors for events already persisted. If a message cannot
  be durably admitted, stop reading the socket and close it with WebSocket
  status 1013. Count non-capacity admission failures and page on any occurrence.
- **Why:** A full outbox already stopped socket reads, but filesystem errors
  such as a failed temporary-file write or directory sync returned ordinary
  callback errors. The connection loop continued reading, so frames could be
  consumed without ever being saved.
- **Consequences:** Transient node storage failures are visible to operators,
  and clients receive a retryable close instead of having more frames consumed
  silently. Delivery failures after successful persistence retain the existing
  replay behavior. Connect callbacks remain synchronous because their result
  controls connection admission.
- **Rejected alternatives:** Treating every callback delivery error as an
  admission failure would close sockets even when the event is already durable
  and awaiting replay.
