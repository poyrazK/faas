# FaasEdgeDNS: edgednsd and the direct-entry records

`edgednsd` runs on the control plane (ADR-20261102081618 §3). It points the
managed app host names' proxied A records at the compute nodes whose edges
are ready. Cloudflare does not health-check those records, so edgednsd is the
failover:
- a node is withdrawn after 3 failed readiness probes in a row (about 6 s),
  or at once when it is drained;
- it comes back after 5 answers in a row;
- the last record is never removed.

Useful series: `edge_dns_node_in{node}`, `edge_dns_records{name}`,
`edge_dns_probe_failures_total{node}`, `edge_dns_errors_total{op}` and
`edge_dns_changes_total{name,action,dry_run}`. Logs:
`journalctl -u faas-edgednsd`.

## FaasEdgeDNSNoReadyNode (page)

No compute edge has answered for 2 minutes, but records exist. edgednsd kept
the last ones, so Cloudflare still sends app traffic to edges that may fail.

1. Check the edges on each node: `curl -s http://<node private address>:9109/readyz`
   from the control plane, and `systemctl status faas-gatewayd-public-edge` on
   the node.
2. If the edges are fine but the probe fails, check the network path from the
   control plane to port 9109. The compute firewall admits the control plane
   there (ADR-1343 §5).
3. To move traffic back to the tunnel while you fix it, put the wildcard's
   CNAME back with `deploy/scripts/cloudflare-app-dns.sh wildcard`. The tunnel
   keeps running while `public_edge.entry` is `both`.

## FaasEdgeDNSNodeWithdrawn

One node's edge stopped answering, and edgednsd removed its address. The other
nodes carry its traffic. Check that node's edge as in step 1 above. Its record
returns by itself after five answers in a row.

## FaasEdgeDNSWritesFailing

edgednsd's Cloudflare calls fail, so the records stay as they are and a node
that fails now is not withdrawn. The log names the call and Cloudflare's error.

- **Authentication errors:** the token in
  `/etc/faas/secrets/edgednsd/cf-token` is wrong, expired or lacks Zone > DNS:
  Edit on the zone. Rotate it through the `edgednsd_service` role.
- **Rate limits or 5xx:** these are Cloudflare's. They clear by themselves;
  check Cloudflare's status page.
- **Record quota:** a Free zone holds 200 records (1,000 for zones created
  before September 2024).

## FaasEdgeDNSDown

Prometheus cannot scrape edgednsd. Traffic flows on the records as they were,
but there is no failover. Check `systemctl status faas-edgednsd` and its log on
the control plane. The `edgednsd_service` role refuses a host without its
database URL, its token, its names or its node addresses.
