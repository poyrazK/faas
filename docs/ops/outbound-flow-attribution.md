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

Do not treat an empty query as proof no traffic occurred. The listener records
only TCP/UDP conntrack `NEW` events while running. Startup traffic before a
lease is published, a delayed event older than a reused IP's new lease,
untracked traffic, kernel event loss, collector restart, queue overflow, and
database failure can leave gaps. `vmmd` writes `outbound flow capture
coverage_gap` warnings for collector errors, unparsed events, queue overflow,
and failed writes; review those logs for every node in the requested window.
The warning records are not yet a durable coverage ledger. This collector
cannot reconstruct flows from before it was deployed.
