# ADR: Event delivery reliability repairs

Date: 2026-11-11
Status: Accepted
Amends: ADR-100, ADR-800, ADR-801, ADR-929,
ADR-20261022121642, ADR-20261101093512

The event investigation identified thirteen defects. Preserve existing event
identities, account/company authorization, bounded broker delivery, captured
policies, and claim generation fencing while correcting them.

## Decisions

- NATS trigger consumers are durable. A missing consumer invalidates its cached
  poller even while the shared connection remains open. Pool wrappers preserve
  broker deferral interfaces. Diagnostic URLs exclude all credential userinfo.
  Promotion of an existing ephemeral consumer uses a second update to clear
  the inherited expiry without resetting acknowledgement state. A stream
  consumer inactivity limit that prevents this is reported as incompatible.
- Kafka retains only topic/partition/offset commit metadata after settlement.
  Its fetched, uncommitted window is bounded by the natural poll batch; an open
  poison offset or a failed commit applies backpressure. Retry pending commits
  before fetching another batch. Later offsets never commit across an open one.
- Redis walks bounded pending-entry pages and reclaims entries only after the
  existing visibility interval. Active batches refresh their broker claims;
  ownership comparison and refresh are atomic, so a renewal cannot steal a
  claim that another consumer acquired. Database claim fencing stays in force.
- Operator retry marks an external trigger record as stored replay work.
  Scheduler delivery reads those records without requiring a live broker or
  the original broker handle. Existing record claims fence competing workers
  and broker redeliveries. Settlement changes the stored record; it does not
  commit, acknowledge or republish an unrelated broker message. The marker
  survives retries and restart. Missing payloads are explicitly not replayable;
  an active claim cannot be reset under its handler. Stored replay resumes
  already admitted work, so later intake filter changes cannot suppress it.
- Data API hooks capture an optional schema version on each change row. The
  owner selects it through a private SQL function; changing that selection
  does not modify queued changes. Relay and API carry it unchanged. A registered
  schema still requires a selected version and validates the actual data.
  The exported owner SQL can be reapplied to upgrade existing installations.
- Retained matching identities are recognized before current schema admission
  rules. Changed content remains a conflict. App producer metadata and company
  visibility checks also apply to change hooks and retention sample identities.
- IDs and event types cannot contain HTTP control characters. Do not silently
  rewrite identity strings to satisfy the transport.
- Permanent routing failures stop even after a subscription matches. Circuit
  probe liveness follows the current matching routing claim, including renewal,
  instead of a stale copy of its first lease deadline.

These repairs do not promote the event capability to GA. Reference-host
throughput, the controlled primary/standby failover drill, and the deployed
customer journey remain the release gate in ADR-20261029093000.
