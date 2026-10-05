# ADR-507 · Wait for the initial secret revision

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** The Node secret reload starter waits for its first versioned
  projection before applying credentials or posting an application ACK. During
  this bootstrap step only, a valid secret map paired with the platform's empty
  initial revision means pending. Poll with exponential delay from 100 ms to
  two seconds and a default 30-second deadline, configurable through the helper's
  startupTimeoutMs option. Both atomic and older separate-file projections use
  this rule; the latter retains its before/after revision check. Once a valid
  revision is observed, all further reads use strict validation, including reads
  after stale ACKs. The deadline bounds initial projection availability, not
  database setup or the existing bounded ACK transport retries.
- **Why:** Guest-init prepares an empty revision before the first successful
  host fetch. Immediate rejection could exhaust the starter's restart budget
  while the host was merely delayed. Handler readiness alone does not make the
  projection ready to apply.
- **Consequences:** Install the reload and shutdown handlers before awaited
  initialization and write the handler-ready marker before waiting. Keep HTTP
  serving disabled until credentials are applied and their ACK is accepted.
  Startup and subsequent signal reloads remain serialized. SIGTERM and SIGINT
  cancel pending reads, waits, application completion and ACK transport; cancelled
  operations do not send failed ACKs or begin serving. Driver work already in
  flight is drained without installing a candidate pool after shutdown. Timeout
  or invalid data produces a sanitized startup failure. No secret values or raw
  errors enter logs or ACK payloads. Existing images require a starter redeploy;
  no guest, API, database or SDK contract changes.
- **Validation:** Test delayed first publication and recovery, capped backoff,
  permanent pending timeout, cancellation during read/wait/apply/ACK, rotation
  and stale ACKs, legacy startup, strict reloads after bootstrap and malformed
  pending data. Exercise the actual starter in a child process with controlled
  database and HTTP dependencies to verify readiness, serialization, serving
  order, shutdown and sanitized failure. Run these tests from the materialized
  embedded template as well as its source.
- **Rejected alternatives:** Accepting or acknowledging an empty revision would
  lose version fencing. Retrying every parse or read error would hide broken
  projections. A fixed delay cannot distinguish a ready revision from a host
  outage. Waiting indefinitely would leave failed startups unresolved.
