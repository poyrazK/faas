# Managed realtime channel routing

## Meaning

`FaasManagedRealtimeChannelRouteFallbackHigh` warns when one enabled-routing
fallback reason accounts for more than 25% of route decisions across at least
100 decisions in each ten-minute window for five minutes. The `decision` label
shows whether the route store is unavailable, the shared directory read failed,
the requested channel is isolated because of the endpoint route-row cap, or one
or more active nodes lack a current snapshot. The alert excludes
`routing_disabled`; a deliberate routing rollout or rollback does not count as
degradation.

`FaasManagedRealtimeChannelRouteRebuildUnsuccessful` warns after at least two
rebuild start errors or incomplete/failed reconciliation passes in thirty
minutes, sustained for five minutes. This indicates route overflow repair is
not keeping up. Affected channels remain on fleet-wide publish broadcast,
which is safe for delivery but increases per-publish work for those channels.

These warnings are ticket-tier performance signals. They do not mean a
subscriber was skipped: route failures and unready snapshots fall back to
broadcast.

## Check

Compare routing decisions and recipient counts in Prometheus:

```promql
sum by (decision) (rate(apid_realtime_channel_route_publish_decisions_total[5m]))
histogram_quantile(0.95, sum by (decision, le) (rate(apid_realtime_channel_route_publish_recipients_bucket[5m])))
sum by (outcome) (increase(apid_realtime_channel_route_rebuild_checks_total[15m]))
sum by (outcome) (increase(apid_realtime_channel_route_reconcile_passes_total[15m]))
```

The publish metrics intentionally omit endpoint and node identifiers. Inspect
the shared overflow and readiness rows on the control-plane database to find
the affected state:

```sql
SELECT endpoints.id AS endpoint_id,
       endpoints.app_id,
       COALESCE(overflow.overflow_all, false) AS overflow_all,
       array_agg(overflow_channels.channel ORDER BY overflow_channels.channel)
           FILTER (WHERE overflow_channels.channel IS NOT NULL) AS overflow_channels,
       min(overflow_channels.next_rebuild_at) AS channel_next_rebuild_at,
       COALESCE(overflow.rebuilding, false) AS rebuilding,
       overflow.rebuild_generation,
       overflow.next_rebuild_at,
       overflow.rebuild_started_at
FROM managed_realtime_endpoints AS endpoints
LEFT JOIN managed_realtime_channel_route_overflow AS overflow
       ON overflow.endpoint_id = endpoints.id
LEFT JOIN managed_realtime_channel_route_overflow_channels AS overflow_channels
       ON overflow_channels.endpoint_id = endpoints.id
WHERE overflow.endpoint_id IS NOT NULL OR overflow_channels.endpoint_id IS NOT NULL
GROUP BY endpoints.id, endpoints.app_id, overflow.overflow_all,
         overflow.rebuilding, overflow.rebuild_generation,
         overflow.next_rebuild_at, overflow.rebuild_started_at
ORDER BY COALESCE(overflow.next_rebuild_at, min(overflow_channels.next_rebuild_at)) NULLS LAST;

SELECT nodes.id,
       nodes.name,
       COALESCE(node_state.snapshot_generation, -1) AS snapshot_generation,
       generation.generation AS current_generation,
       node_state.updated_at
FROM compute_nodes AS nodes
CROSS JOIN managed_realtime_channel_route_generation AS generation
LEFT JOIN managed_realtime_channel_route_node_state AS node_state
       ON node_state.node_id = nodes.id
WHERE nodes.active = true
ORDER BY snapshot_generation, nodes.name;
```

Check apid logs for rebuild and directory errors:

```sh
journalctl -u faas-apid --since '-30m' --no-pager \
  | grep -iE 'managed realtime channel route|database|timeout'
```

## Recover

- `route_store_unavailable`: confirm every apid replica has the channel-routing
  state-store support and required migrations. Routing can only be enabled
  after all replicas have the compatible version.
- `directory_error` or rebuild start errors: restore apid-to-PostgreSQL
  connectivity first. Confirm queries succeed and inspect apid logs before
  restarting a service.
- `unready_fallback` or incomplete passes: compare active nodes with the
  current snapshot generation. Check the affected node's realtime service and
  private control route, then allow a fresh connection snapshot to complete.
  A brief fallback during node activation or rollout is expected.
- `overflow`: `overflow_all=true` means the endpoint is in a rebuild window or
  still has a legacy endpoint-wide marker. Otherwise `overflow_channels` lists
  the channels using full broadcast. `channel_next_rebuild_at` shows when those
  channel markers will be retried; rebuild retries occur every five minutes.
  Gregale isolates the channels with the largest route sets until the remaining
  indexed rows fit the 10,000-row endpoint budget. Reduce an oversized live
  subscription footprint through normal endpoint operations; do not delete
  directory rows by hand.

If repeated directory failures make the extra lookups harmful, temporarily
disable `FAAS_REALTIME_CHANNEL_ROUTING_ENABLED` and restart every apid replica.
Publish then returns to full-fleet broadcast, so watch node publish load while
routing is disabled. Re-enable routing after storage and node snapshots are
healthy.

Close the warning after the fallback share drops, recipient fanout returns to
the expected level, and rebuild errors or incomplete passes stop increasing.
