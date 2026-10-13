# FaasRouteLookupStaleServed

Severity: warn. Spec §12, ADR-190 §3.

`gatewayd-internal` keeps a last-known-good route tier. When the
authoritative route lookup against Postgres errors, it serves the request from
that tier and counts `gateway_route_lookup_stale_served_total`. The alert fires
when a node has served stale routes for 5 minutes.

## Symptom

Requests keep routing, but route changes (new deployments, domain changes,
traffic splits) stop reaching this node. Entries older than
`FAAS_GATEWAY_ROUTE_STALE_TTL` are no longer served, so a long outage turns
into 404s for apps whose cached route expired.

## Check

1. `rate(gateway_route_lookup_stale_served_total[5m])` by `instance`: one node
   or the whole fleet? One node points at its network path or pool; the fleet
   points at Postgres.
2. On the node, `journalctl -u faas-gatewayd-internal` for the lookup error.
3. `FaasDBPoolStarved` / `FaasDBPoolNearCap` for the same instance mean the
   lookup is waiting on its own pool, not on Postgres.

## Recover

Restore Postgres reachability for the node. The stale tier needs no manual
reset: the next successful lookup replaces it, and the counter stops.
