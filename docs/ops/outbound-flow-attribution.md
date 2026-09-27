# Outbound flow attribution

`vmmd` listens for conntrack `NEW` events on multi-node compute hosts and
stores the original TCP/UDP tuple with the account, app, deployment, instance,
node, and deployment image digest. It records connection metadata only: no
payloads, DNS names, paths, headers, or secrets. The read surface is the
operator database; no customer API exposes these rows.

For an abuse report containing a public source IP and UTC time window, query:

```sql
SELECT observed_at, node_id, account_id, org_id, app_id,
       deployment_id, image_digest, instance_id,
       host(source_ip) AS private_source_ip, source_port,
       host(destination_ip) AS destination_ip, destination_port,
       protocol, egress_ip_source
FROM outbound_flow_events
WHERE egress_ip = '203.0.113.10'::inet
  AND observed_at >= '2026-09-27T12:00:00Z'::timestamptz
  AND observed_at <  '2026-09-27T12:10:00Z'::timestamptz
ORDER BY observed_at, id
LIMIT 1000;
```

`egress_ip` is copied from the app's static egress assignment or the compute
node's public IP at insertion. `egress_ip_source` says which one was used;
`unknown` means neither was available. This is an inferred public address,
not a packet-level observation of a cloud provider's NAT. `source_port` is
the guest's original port, which may differ from the external NAT port.
Use destination IP and port, the node, and a narrow time window to corroborate
a provider report. IDs remain after instance, deployment, or app deletion;
account deletion removes the rows. The scheduler's hourly retention sweep
deletes rows older than 30 days by default.

The instance runtime publication also records a historical private-IP lease.
For a captured source IP and node, compare the event timestamp with the
control-plane ownership interval:

```sql
SELECT instance_id, account_id, org_id, app_id, deployment_id,
       active_from, active_until
FROM outbound_flow_ip_leases
WHERE node_id = '00000000-0000-0000-0000-000000000000'::uuid
  AND host_ip = '10.100.0.5'::inet
  AND active_from <= '2026-09-27T12:00:00Z'::timestamptz
  AND (active_until IS NULL OR
       active_until > '2026-09-27T12:00:00Z'::timestamptz)
ORDER BY active_from, id;
```

Exactly one row supports the historical ownership join. Zero means unknown;
multiple rows mean ambiguous ownership and must not be used to blame a
customer. The ledger survives instance/app/deployment/node deletion, is
removed on account erasure, and closed rows expire after 30 days. Its times
come from control-plane publication and teardown, so they are not exact
packet-level lease boundaries. At installation, already-live leases begin at
the migration time; older ownership is not reconstructed. The collector also
keeps retired leases in memory for two minutes to resolve delayed conntrack
events after a VM stops or its address is reused.

For each node that could have used the reported public IP, check capture
coverage over the same UTC window. `public_ip` on a sample is a copy of the
node assignment at insertion; a static app egress IP can differ, so use the
`node_id` from flow rows or the historical app assignment when necessary.
Replace the node UUID and times below:

```sql
WITH bounds AS (
    SELECT '00000000-0000-0000-0000-000000000000'::uuid AS node_id,
           '2026-09-27T12:00:00Z'::timestamptz AS from_at,
           '2026-09-27T12:10:00Z'::timestamptz AS to_at
), ordered AS (
    SELECT s.*,
           lead(sampled_at) OVER w AS next_at,
           lead(session_id) OVER w AS next_session,
           lead(listening) OVER w AS next_listening,
           lead(queue_dropped_total) OVER w AS next_queue_dropped,
           lead(database_dropped_total) OVER w AS next_database_dropped,
           lead(unparsed_total) OVER w AS next_unparsed,
           lead(unattributed_total) OVER w AS next_unattributed,
           lead(stderr_total) OVER w AS next_stderr
    FROM outbound_flow_capture_samples s, bounds b
    WHERE s.node_id = b.node_id
      AND s.sampled_at >= b.from_at - interval '15 seconds'
      AND s.sampled_at <= b.to_at + interval '15 seconds'
    WINDOW w AS (ORDER BY sampled_at, id)
), checked AS (
    SELECT count(o.id) AS samples,
           coalesce(bool_or(o.sampled_at <= b.from_at), false) AS start_anchor,
           coalesce(bool_or(o.sampled_at >= b.to_at), false) AS end_anchor,
           count(*) FILTER (
               WHERE o.sampled_at < b.to_at
                 AND coalesce(o.next_at, b.to_at) > b.from_at
                 AND (o.next_at IS NULL
                   OR o.next_at - o.sampled_at > interval '10 seconds'
                   OR NOT o.listening OR NOT o.next_listening
                   OR o.session_id IS DISTINCT FROM o.next_session
                   OR o.queue_dropped_total IS DISTINCT FROM o.next_queue_dropped
                   OR o.database_dropped_total IS DISTINCT FROM o.next_database_dropped
                   OR o.unparsed_total IS DISTINCT FROM o.next_unparsed
                   OR o.unattributed_total IS DISTINCT FROM o.next_unattributed
                   OR o.stderr_total IS DISTINCT FROM o.next_stderr)
           ) AS suspect_segments
    FROM bounds b LEFT JOIN ordered o ON true
)
SELECT start_anchor AND end_anchor AND suspect_segments = 0 AS observed_coverage,
       samples, start_anchor, end_anchor, suspect_segments
FROM checked;
```

`observed_coverage = false` means the sampled record cannot support an
unbroken window. It catches missing boundary heartbeats, intervals over ten
seconds, listener downtime, session changes, increased loss counters, and
guest-source events with no resolvable owner.
Inspect the underlying sample rows (`sampled_at`, `received_at`, `reason`,
`listening`, and counters) to localize the gap. Samples are emitted every five
seconds and on state or loss changes. They survive node deletion and expire
after 30 days. An `observed_coverage = true` result is a health indication,
not proof that the kernel reported every packet or that every protocol was
tracked.

Do not treat an empty query as proof no traffic occurred. The listener records
only TCP/UDP conntrack `NEW` events while running. Startup traffic before a
lease is published, a delayed event older than a reused IP's new lease,
untracked traffic, kernel event loss, collector restart, queue overflow, and
database failure can leave gaps. `vmmd` writes `outbound flow capture
coverage_gap` warnings for collector errors, unparsed or unattributed guest
events, queue overflow,
and failed writes; those losses also advance the cumulative counters in the
coverage samples when the database is reachable. A database outage may
prevent the sample itself from being written, in which case the missing
heartbeat is the evidence of uncertain coverage. This collector cannot
reconstruct flows from before it was deployed.
