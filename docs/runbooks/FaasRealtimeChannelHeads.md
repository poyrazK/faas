# Retained realtime channel heads

`FaasRealtimeChannelHeadsGrowing` warns when `managed_realtime_channel_heads`
has held more than 1,000,000 rows for an hour.

Every channel that has ever received a retained message has one head row: its
next sequence and the oldest one still retained. Heads outlive their messages
on purpose. A client reconnects with the last sequence it processed, and that
sequence must never be reissued, so a head is kept after its messages expire
(ADR-1323). Nothing deletes heads except deleting their endpoint, and the table
grows by one row (about 200 bytes with its index) per distinct retained
channel. The held-channel caps (32 per endpoint, 32 to 1,024 per account by
plan) bound how many channels hold messages at once, not how many have ever
existed, so an account that names retained channels after users, rooms or
requests keeps adding rows as old ones go quiet.

## Confirm the signal

The count comes from the PostgreSQL activity collector on the control plane
(`faas_postgres_table_live_tuples`, PostgreSQL's estimate):

```sh
grep managed_realtime_channel_heads /var/lib/node_exporter/textfile_collector/faas_postgres_activity.prom
```

```sql
SELECT count(*) AS heads,
       count(*) FILTER (WHERE oldest_sequence = next_sequence) AS empty_heads
FROM managed_realtime_channel_heads;
```

## Find who is creating them

```sql
SELECT e.account_id, e.app_id, h.endpoint_id,
       count(*) AS heads,
       count(*) FILTER (WHERE h.oldest_sequence = h.next_sequence) AS empty_heads
FROM managed_realtime_channel_heads h
JOIN managed_realtime_endpoints e ON e.id = h.endpoint_id
GROUP BY 1, 2, 3
ORDER BY heads DESC
LIMIT 20;
```

A handful of endpoints with mostly empty heads means a channel-naming pattern
that never reuses names. Sample the channel names of the top endpoint before
deciding anything:

```sql
SELECT channel, next_sequence FROM managed_realtime_channel_heads
WHERE endpoint_id = $1 ORDER BY next_sequence LIMIT 50;
```

## Respond

1. **Do not delete heads by hand.** A deleted head restarts its channel at
   sequence 1. A client that reconnects with a saved cursor then gets a
   history error instead of `resync_required`, and consumer groups, named
   cursors and read progress on that channel point at sequences that no longer
   exist. Deleting the endpoint is the only safe bulk removal.
2. If one account dominates, tell the customer: retained delivery is for
   channels clients come back to. Per-request or per-message channel names
   belong on live delivery, which creates no head.
3. If growth is broad, plan the cleanup that was deferred at GA: delete heads
   that hold nothing, have no consumer group, named cursor or read progress,
   and have been empty for 30 days, and start any later head on that endpoint
   above the highest sequence it deleted, so sequences never go backwards and
   a reconnecting client gets `resync_required`. That changes the history
   contract and needs an ADR-1323 amendment first.
