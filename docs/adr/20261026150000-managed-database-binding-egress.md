# ADR: A managed database binding opens its own port

Date: 2026-10-26
Status: Accepted
Amends: [ADR-361](361-tenant-egress-default-deny.md) (decision 5), [ADR-373](373-guest-dns-policy.md) (decision 2)
Related: [ADR 20261026131131](20261026131131-data-api-hosted-tls-roots-and-snapshot-sessions.md)

## Context

ADR-361 limits tenant egress to TCP 80 and 443; extra ports, such as 5432 for
a database, are Pro and Scale only and named in `apps.egress_ports`. Managed
PostgreSQL gives Free and Hobby one database each, and the managed-postgres and
Data API capabilities include Hobby, yet an app on those plans could not reach
its own database: `gregale data-api create` failed on
`plan_egress_ports_not_allowed` after creating the binding. Opening 5432 to the
whole internet for every binding would undo ADR-361's protection against
database scanning from cheap plans.

ADR-373 already routes every guest DNS answer through the node's bridge
resolver, which tells vmmd the answered addresses before replying.

## Decision

1. **Endpoints are recorded.** Every managed credential passes through the
   credential sink's `SealCredential` (ordinary delivery, rotation, environment
   clones and cutovers). It records each host name and port of the credential's
   database in `managed_postgres_database_endpoints` (no secret; IP literals
   are not recorded). A failed record fails the seal and the caller retries.
2. **The resolver knows bound names.** gatewayd-internal keeps an in-memory
   index of host → bound app → port, from ready or retiring bindings joined with
   their database's endpoints, refreshed every 10 s (a failed read keeps the
   previous index for another interval). Only an answered name in the index
   resolves the calling app, through the same source-address identity as
   service calls.
3. **vmmd opens that pair.** `AllowResolvedEgressRequest.database_port` carries
   the port when the name is one of the calling app's databases. vmmd refuses a
   port ADR-361 never opens, then adds `address . port` to the instance's
   `egress_db` set with the answer's clamped TTL, in the same nft transaction as
   `egress_resolved`, and remembers the pairs per app to seed new instances, as
   ADR-373 does for addresses. The per-netns rule
   `daddr . tcp dport @egress_db accept` sits just before the ADR-361 port
   drop: after the lateral-movement deny, the rate limits, the DNS gate and the
   SMTP drop. The set exists on every tenant namespace (`DatabaseEgress`,
   part of the prepared-namespace equality), independent of the DNS-gating
   switch.
4. **Plans.** This applies on every plan. `apps.egress_ports` keeps its meaning
   and plan gate; `data-api create` and `postgres attach` still try to add 5432
   and, refused for the plan, keep their other settings and rely on the binding.

## Consequences

- A Free or Hobby app reaches exactly the hosts of the databases it is bound
  to, on their port, and nothing else on that port.
- An app with an ADR-031 egress allowlist also reaches its bound databases: the
  binding is an explicit owner choice.
- A binding made ready less than 10 s before its first connection may fail
  that connection; libpq and Node resolve on every connect, so the retry
  succeeds once the index refreshes.
- Old components degrade safely: an older resolver sends no port (no change);
  an older vmmd ignores the field; an older prepared namespace lacks the set and
  is not reused.
- Unit tests cover rendering, vmmd admission and seeding, the RPC, the
  resolver index and the sink; the namespace rule still needs a metal run.
