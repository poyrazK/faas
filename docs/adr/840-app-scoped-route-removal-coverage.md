# ADR-840: App-scoped route removal coverage

## Status

Accepted

## Context

ADR-839 conservatively resets fleet-wide coverage whenever any app drops
telemetry or has a backlog. Unrelated traffic can therefore invalidate a
healthy app's removal approval. Treating a mixed delivery batch as entirely
failed has the same effect even when the receiver acknowledged healthy rows.

## Decision

Gateway heartbeats retain node-wide availability, boot identity, sampling,
collection state, source time, and monotonic sequencing. Their new app-scoped
format additionally carries unacknowledged per-app loss deltas and a complete
per-app pending-row snapshot. App IDs are server-resolved request identities.
The gateway records ring overwrites against the evicted app, and publisher
failures against only unacknowledged rows. Responses remain ordered with rows;
a rejected row does not prevent reading acknowledgements for later apps.
Retries send only unacknowledged rows with their original idempotency keys.

The recorder's loss journal is bounded by ring capacity. Successful heartbeat
receipts subtract only the loss counts actually reported, preserving concurrent
losses. Failed or lost receipts retain the journal. Unattributable losses and
journal overflow increment a persistent process-wide loss counter and retain
conservative shared coverage behavior. Repeated loss reports after a lost
receipt may conservatively extend the grace deadline and duplicate advisory
loss totals; they never erase a gap.

Apid commits node status and app gaps atomically. The database records the last
app gap, cumulative reported loss, and current backlog per app/node. An omitted
app has no pending rows in that heartbeat. Clearing a backlog starts the app's
new quiet grace period at receipt time. Replays and out-of-order reports do not
modify either node or app evidence. Deleted apps are ignored; FK cleanup removes
their stored gaps. New node-local app evidence can be omitted for an idle app
because the complete snapshot and continuous node coverage establish delivery.

Removal checks, approvals, and positive traffic transitions use the target app's
gaps. Missing/stale gateway heartbeats, restarts, disabled/sampled collection,
and unattributed losses remain shared blockers. An app's gap invalidates its
existing receipt until it completes a new full grace period and obtains approval.
App-specific recovery deadlines include that app's most recent gap.

Promotion locks node membership, node coverage, and only the target app's gap
rows. The heartbeat writer obtains FK key-share locks for reported apps before
its node write lock, matching promotion's app-before-node order. This prevents
an insert's app FK from deadlocking with a promotion waiting on node coverage.
Older heartbeat callers retain the stricter fleet-wide accounting, and older
receivers ignore new fields while still processing global loss/backlog totals.
No public HTTP request or response shape changes.

## Consequences

- Unrelated app failures cannot invalidate a healthy app's observed coverage.
- Shared availability and unknown delivery failures continue to fail closed.
- Per-node process memory is bounded; durable app gaps follow app/node lifetime.
- The two-minute aggregation delay, retention margin, receipt fences, and
  observed-only limits from ADR-839 still apply.
