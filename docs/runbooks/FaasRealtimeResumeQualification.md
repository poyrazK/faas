# Managed realtime resume qualification

Use this runbook to qualify the gated resumable-channel preview on a
nonproduction two-node deployment. It covers the client reconnect contract and
the private history path between `realtimed` and `apid`; it does not approve
pricing or customer promotion.

## Preconditions

- Use a dedicated staging account, app, endpoint, and channel with no customer
  traffic. Record their IDs before starting.
- Run the same candidate build of `apid` and `realtimed` on both realtime nodes.
- Verify both nodes can reach apid's private history listener. For split-box
  deployments, verify the history RPC uses the configured mTLS certificates.
- Enable `FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1` on staging apid replicas and
  `FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1` on the two staging realtime nodes.
- Configure the test endpoint for `oidc_jwt`, an exact allowed browser origin
  when using browsers, and an application `authorize-channel` callback that
  grants only the test principal and channel.
- Confirm Prometheus scrapes both realtime nodes and captures the resume
  metrics described in [realtime operations](../ops/realtime.md).

Do not enable these flags on customer-facing nodes as part of this exercise.
Keep normal live-only publish available for the rest of the deployment.

## Baseline and reconnect

1. Record the candidate versions, node names, apid history target type, current
   preview flags, and the baseline for active subscriptions, history-read
   results, read latency, resyncs, replayed payload bytes, and slow-consumer
   disconnects.
2. Connect the test client through node A. Subscribe to the test channel and
   persist the acknowledged cursor.
3. Publish retained messages while the client is connected. Confirm each
   sequence is observed once in order and acknowledge the latest sequence.
4. Close the client, publish additional retained messages, and reconnect
   through node B from the last acknowledged cursor. Confirm the client receives
   every later sequence in order, then acknowledge the latest sequence.
5. Restart node B while the client is connected and repeat the reconnect
   through node A. Confirm node-local subscription gauges move with the socket
   and the durable history remains authoritative across both owner changes.

Expected signals: successful history reads and replay counters increase;
`realtimed_current_resume_subscriptions` returns to zero on the former owner
and rises on the new owner. Queue admission does not prove client receipt, so
compare the client-observed sequences with the published sequences.

## Failure cases

Run each case against the dedicated test endpoint and restore the dependency or
policy before continuing.

### Private history reader unavailable

Temporarily block or stop only the staging history listener reachable by one
realtime node. Subscribe through that node and verify it returns
`history_read_failed` without creating a channel subscription or sending a
partial page. If the listener fails during an active subscription, verify the
server sends the error and closes that socket with a retryable reason. Restore
the listener, reconnect from the last acknowledged cursor through the other
node, and confirm the missing sequence range replays in order.

Expected signals: `realtimed_resume_history_reads_total{result="error"}` rises
on the affected node and the history-read latency histogram records the failed
attempt. No sequence may be silently skipped.

### Endpoint revocation

While a test client is subscribed, disable or delete the endpoint through the
authenticated management API. Verify existing sockets close on both nodes and
new handshakes are rejected. Re-enable or recreate the dedicated endpoint only
after recording the result.

Expected signals: active resume subscriptions return to zero. A subsequent
client must not receive retained data until endpoint policy and channel
authorization grant access again.

### Retention expiry

Use an isolated staging database or schema and only the recorded test endpoint
and channel. Age its test messages beyond the retention interval, then allow
the normal history reaper to run. Do not modify rows for any other endpoint.
Reconnect from a cursor below the new floor and confirm the client receives
`resync_required` with the current bounds; it must rebuild state before
subscribing with a fresh cursor.

Expected signals: `realtimed_resume_resync_required_total{reason="cursor_expired"}`
rises. The client must not receive a partial replay presented as complete.

### Slow client

Use a dedicated test client that stops reading while the test publisher sends a
bounded burst of retained messages. Confirm the output queue fills, the socket
closes with the retryable slow-consumer reason, and reconnecting from the last
acknowledged cursor replays the unacknowledged range.

Expected signals: `realtimed_resume_slow_consumer_disconnects_total` rises;
the client can resume without a sequence gap or silent loss.

## Finish and evidence

After the cases, disable both preview flags on the staging deployment, restart
the affected services, and remove the dedicated endpoint and test data. Record
the case outcomes, observed p95 history-read latency, metric deltas, any
unexpected disconnects, and the candidate build IDs. Keep thresholds advisory
until repeated staging runs establish a baseline; cursor-expiry resyncs are an
expected recovery signal and should not page on their own.

Stop the exercise and leave the preview disabled if a reader outage exposes a
partial history page, a sequence is skipped without `resync_required`, endpoint
revocation leaves an existing socket usable, or a slow-client close cannot
resume from the last acknowledged cursor.
